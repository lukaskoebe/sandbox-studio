import { useEffect, useReducer } from "react"
import { errorMessage, fetchClient } from "@/lib/api/client"
import type { components } from "@/lib/api/schema.gen"

export type Chat = components["schemas"]["Chat"]
export type ChatItem = components["schemas"]["Item"]
export type ChatTurn = components["schemas"]["Turn"]
export type ToolBody = components["schemas"]["ToolBody"]
export type PermissionOption = components["schemas"]["PermissionOption"]

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
  /** The last rev seen, where a reconnect resumes. */
  rev: number
}

type Action =
  | { type: "reset" }
  | { type: "loaded"; chat: Chat; turns: ChatTurn[]; items: ChatItem[] }
  | { type: "failed"; error: string; gone: boolean }
  | { type: "live"; live: boolean }
  | { type: "chat"; chat: Chat; rev: number }
  | { type: "turn"; turn: ChatTurn; rev: number }
  | { type: "item"; item: ChatItem; rev: number }

const initial: State = {
  status: "loading",
  gone: false,
  live: false,
  turns: new Map(),
  items: new Map(),
  rev: 0,
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
        rev: a.chat.rev,
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
        turns: upsert(s.turns, a.turn),
        rev: Math.max(s.rev, a.rev),
      }
    case "item":
      return {
        ...s,
        items: upsert(s.items, a.item),
        rev: Math.max(s.rev, a.rev),
      }
  }
}

/**
 * A chat kept current from its stream: getChat for the log, then the SSE stream from the
 * log's rev. Every event carries its rev; after a disconnect the stream reopens from the
 * last one, so nothing is missed and nothing is applied twice out of order.
 */
export function useChat(env: string, chatId: string) {
  const [state, dispatch] = useReducer(reduce, initial)

  useEffect(() => {
    // An object, so the async callbacks see the cleanup's write.
    const done = { closed: false }
    let source: EventSource | undefined
    let retry: ReturnType<typeof setTimeout> | undefined
    let rev = 0
    dispatch({ type: "reset" })

    const connect = () => {
      if (done.closed) return
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
        { params: { path: { env, chat: chatId } } }
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
        })
      }
      connect()
    }

    // Studio unreachable: say so and keep trying.
    const reload = () =>
      void load().catch((err: unknown) => {
        if (done.closed) return
        dispatch({ type: "failed", error: errorMessage(err), gone: false })
        retry = setTimeout(reload, 5000)
      })
    reload()
    return () => {
      done.closed = true
      clearTimeout(retry)
      source?.close()
    }
  }, [env, chatId])

  const items = [...state.items.values()].sort((a, b) => a.seq - b.seq)
  const turns = [...state.turns.values()].sort((a, b) => a.seq - b.seq)
  return { ...state, items, turns }
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
