import { useCallback, useEffect, useReducer, useRef } from "react"
import { errorMessage, fetchClient } from "@/lib/api/client"
import type { components } from "@/lib/api/schema.gen"
import { markChatSeen } from "@/lib/chat-seen"

export type Chat = components["schemas"]["Chat"]
export type ChatItem = components["schemas"]["Item"]
export type ChatTurn = components["schemas"]["Turn"]
export type ToolBody = components["schemas"]["ToolBody"]
export type PermissionOption = components["schemas"]["PermissionOption"]
export type QueuedPrompt = components["schemas"]["QueuedPrompt"]

/** How many turns a chat loads at a time, from its last; the server also stops at 500 items. */
const pageTurns = 40

/** At most this many bytes of attachments per prompt, as the server enforces. */
export const maxAttachmentBytes = 10 << 20

/** What a chat's state means for the person using it. */
export const chatStateLabels: Record<Chat["state"], string> = {
  idle: "Ready",
  running: "Working",
  terminal: "Open in the terminal",
  orphaned: "Ended",
}

/** A chat's title, or a stand-in until the agent names it. */
export function chatTitle(c: Chat) {
  return c.title || "New chat"
}

type State = {
  status: "loading" | "ready" | "error"
  error?: string
  /** Gone: the chat was deleted. */
  gone: boolean
  /** Whether the live stream is connected. */
  live: boolean
  chat?: Chat
  turns: Map<string, ChatTurn>
  items: Map<string, ChatItem>
  /** Prompts that go once the running turn ends, in order. */
  queued: QueuedPrompt[]
  /** The last rev seen, where a reconnect resumes. */
  rev: number
  /**
   * The seq of the first turn whose items are loaded; the turns before it have only their
   * header. Undefined once every item is.
   */
  from?: number
  /** Whether earlier turns are loading. */
  loadingEarlier: boolean
}

type Action =
  | { type: "reset" }
  | {
      type: "loaded"
      chat: Chat
      turns: ChatTurn[]
      items: ChatItem[]
      queued: QueuedPrompt[]
      from?: number
    }
  | { type: "loadingEarlier"; loading: boolean }
  | {
      type: "earlier"
      chatId: string
      turns: ChatTurn[]
      items: ChatItem[]
      from?: number
    }
  | { type: "failed"; error: string; gone: boolean }
  | { type: "live"; live: boolean }
  | { type: "chat"; chat: Chat; rev: number }
  | { type: "turn"; turn: ChatTurn; rev: number }
  | { type: "item"; item: ChatItem; rev: number }
  | { type: "queue"; queued: QueuedPrompt[] }

const initial: State = {
  status: "loading",
  gone: false,
  live: false,
  turns: new Map(),
  items: new Map(),
  queued: [],
  rev: 0,
  loadingEarlier: false,
}

/** Keeps the newer of two versions of a row: rows only move forward in rev. */
function upsert<T extends { id: string; rev: number }>(
  m: Map<string, T>,
  row: T
) {
  const old = m.get(row.id)
  if (old && old.rev > row.rev) return m
  const next = new Map(m)
  next.set(row.id, row)
  return next
}

/** A turn's newer version, which keeps its message when it comes from the stream without. */
function upsertTurn(m: Map<string, ChatTurn>, turn: ChatTurn) {
  const prompt = m.get(turn.id)?.prompt
  return upsert(m, turn.prompt || !prompt ? turn : { ...turn, prompt })
}

function reduce(s: State, a: Action): State {
  switch (a.type) {
    case "reset":
      return initial
    case "loaded":
      return {
        ...s,
        status: "ready",
        error: undefined,
        chat: a.chat,
        turns: new Map(a.turns.map((t) => [t.id, t])),
        items: new Map(a.items.map((i) => [i.id, i])),
        queued: a.queued,
        rev: a.chat.rev,
        from: a.from,
      }
    case "loadingEarlier":
      return { ...s, loadingEarlier: a.loading }
    case "earlier": {
      if (s.chat?.id !== a.chatId) return s
      let turns = s.turns
      for (const t of a.turns) turns = upsertTurn(turns, t)
      let items = s.items
      for (const i of a.items) items = upsert(items, i)
      return { ...s, turns, items, from: a.from, loadingEarlier: false }
    }
    case "failed":
      return { ...s, status: "error", error: a.error, gone: a.gone }
    case "live":
      // Connecting again after a failed reload clears its error.
      return a.live && s.chat
        ? { ...s, live: true, status: "ready", error: undefined }
        : { ...s, live: a.live }
    case "chat":
      return { ...s, chat: a.chat, rev: Math.max(s.rev, a.rev) }
    case "turn":
      return {
        ...s,
        turns: upsertTurn(s.turns, a.turn),
        rev: Math.max(s.rev, a.rev),
      }
    case "item":
      return {
        ...s,
        items: upsert(s.items, a.item),
        rev: Math.max(s.rev, a.rev),
      }
    case "queue":
      return { ...s, queued: a.queued }
  }
}

/**
 * A chat kept current from its stream: getChat for the log, then the SSE stream from the
 * log's rev. Every event carries its rev; after a disconnect the stream reopens from the
 * last one, so nothing is missed and nothing is applied twice out of order. Without a chat
 * id there is nothing to follow, so the log stays empty.
 *
 * The stream closes while the tab is hidden and reopens from the rev when it is back: over
 * HTTP/1.1 the browser has six connections to Studio for all its tabs, and streams held open
 * by tabs in the background leave every other request waiting.
 */
export function useChat(env: string, chatId: string | undefined) {
  const [state, dispatch] = useReducer(reduce, initial)

  useEffect(() => {
    dispatch({ type: "reset" })
    if (!chatId) return
    // An object, so the async callbacks see the cleanup's write.
    const done = { closed: false }
    let source: EventSource | undefined
    let retry: ReturnType<typeof setTimeout> | undefined
    let rev = 0

    const connect = () => {
      if (done.closed) return
      source?.close()
      source = undefined
      if (document.visibilityState === "hidden") return
      source = new EventSource(
        `/api/environments/${encodeURIComponent(env)}/chats/${encodeURIComponent(chatId)}/stream?after=${rev}`
      )
      source.onopen = () => dispatch({ type: "live", live: true })
      const on = (name: "chat" | "turn" | "item") =>
        source?.addEventListener(name, (e: MessageEvent<string>) => {
          const id = Number(e.lastEventId)
          if (Number.isFinite(id)) rev = Math.max(rev, id)
          const data: unknown = JSON.parse(e.data)
          if (name === "chat") dispatch({ type: name, chat: data as Chat, rev })
          if (name === "turn")
            dispatch({ type: name, turn: data as ChatTurn, rev })
          if (name === "item")
            dispatch({ type: name, item: data as ChatItem, rev })
        })
      on("chat")
      on("turn")
      on("item")
      // The queue is whole each time, and not part of the log, so it moves no rev.
      source.addEventListener("queue", (e: MessageEvent<string>) =>
        dispatch({
          type: "queue",
          queued: (JSON.parse(e.data) as QueuedPrompt[] | null) ?? [],
        })
      )
      // EventSource would retry with the URL's original ?after=, so reconnect by hand
      // from the last rev; a deleted chat answers 404, which load reports.
      source.onerror = () => {
        source?.close()
        dispatch({ type: "live", live: false })
        if (!done.closed) retry = setTimeout(reload, 2000)
      }
    }

    const load = async () => {
      const { data, error, response } = await fetchClient.GET(
        "/api/environments/{env}/chats/{chat}",
        { params: { path: { env, chat: chatId }, query: { last: pageTurns } } }
      )
      if (done.closed) return
      if (!data) {
        const gone = response.status === 404
        dispatch({
          type: "failed",
          error: gone ? "This chat was deleted." : errorMessage(error),
          gone,
        })
        if (!gone) retry = setTimeout(reload, 5000)
        return
      }
      if (rev === 0) {
        rev = data.chat.rev
        dispatch({
          type: "loaded",
          chat: data.chat,
          turns: data.turns ?? [],
          items: data.items ?? [],
          queued: data.queued ?? [],
          from: data.from || undefined,
        })
      }
      connect()
    }

    // Studio unreachable: say so and keep trying.
    const reload = () => {
      retry = undefined
      void load().catch((err: unknown) => {
        if (done.closed) return
        dispatch({ type: "failed", error: errorMessage(err), gone: false })
        retry = setTimeout(reload, 5000)
      })
    }
    // Hidden: close the stream. Back in view: reopen it from the rev, or load the log if it
    // never loaded. A retry already waiting does either by itself.
    const visibility = () => {
      if (done.closed) return
      if (document.visibilityState === "hidden") {
        source?.close()
        source = undefined
        return
      }
      if (source || retry !== undefined) return
      if (rev === 0) reload()
      else connect()
    }
    reload()
    document.addEventListener("visibilitychange", visibility)
    return () => {
      done.closed = true
      clearTimeout(retry)
      source?.close()
      document.removeEventListener("visibilitychange", visibility)
    }
  }, [env, chatId])

  // A chat followed on a page in view is seen (lib/chat-seen.ts), up to its last change. Right
  // after a switch to another chat, the state is still the last chat's.
  const seenRev = state.chat?.id === chatId ? state.rev : undefined
  useEffect(() => {
    if (!chatId || seenRev === undefined) return
    const mark = () => {
      if (document.visibilityState === "visible") markChatSeen(chatId, seenRev)
    }
    mark()
    document.addEventListener("visibilitychange", mark)
    return () => document.removeEventListener("visibilitychange", mark)
  }, [chatId, seenRev])

  // Loads the turns before the first loaded one, once at a time.
  const from = state.from
  const loading = useRef(false)
  const loadEarlier = useCallback(async () => {
    if (!chatId || from === undefined || loading.current) return
    loading.current = true
    dispatch({ type: "loadingEarlier", loading: true })
    try {
      const { data, error } = await fetchClient.GET(
        "/api/environments/{env}/chats/{chat}",
        {
          params: {
            path: { env, chat: chatId },
            query: { last: pageTurns, before: from },
          },
        }
      )
      if (!data) throw new Error(errorMessage(error))
      dispatch({
        type: "earlier",
        chatId,
        turns: data.turns ?? [],
        items: data.items ?? [],
        from: data.from || undefined,
      })
    } catch (err) {
      dispatch({ type: "loadingEarlier", loading: false })
      throw err
    } finally {
      loading.current = false
    }
  }, [env, chatId, from])

  const turns = [...state.turns.values()].sort((a, b) => a.seq - b.seq)
  // Items the stream brings for a turn not loaded yet wait for it, so the thread doesn't show
  // them out of their place.
  const turnSeq = new Map(turns.map((t) => [t.id, t.seq]))
  const items = [...state.items.values()]
    .filter(
      (i) => from === undefined || (turnSeq.get(i.turnId) ?? from) >= from
    )
    .sort((a, b) => a.seq - b.seq)
  /** The turns whose items aren't loaded, first to last. */
  const earlier = from === undefined ? [] : turns.filter((t) => t.seq < from)
  return { ...state, items, turns, earlier, loadEarlier }
}

/** Reads a file as base64 for a prompt attachment. */
export function readBase64(file: File): Promise<string> {
  return new Promise((resolve, reject) => {
    const r = new FileReader()
    r.onload = () => {
      const url = String(r.result)
      resolve(url.slice(url.indexOf(",") + 1))
    }
    r.onerror = () => reject(r.error ?? new Error("Could not read the file"))
    r.readAsDataURL(file)
  })
}

/** How many files a turn changed, counting those left off the list. */
export function changeCount(t: ChatTurn) {
  return (t.changes?.files?.length ?? 0) + (t.changes?.more ?? 0)
}
