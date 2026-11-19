import { diffLines } from "@/components/chat/tool-call-card"
import type { ChatItem, ChatTurn } from "@/lib/chats"

/**
 * What the chat log shows, in order: an item as it is, or a run of steps, the tool calls,
 * thoughts and settled asks between two of the agent's messages, folded into one row.
 */
export type Entry =
  | {
      kind: "item"
      item: ChatItem
      /** A failed step that ended its request: it shows outside the fold, open. */
      ended?: boolean
    }
  | Run

export type Run = {
  kind: "run"
  /** The first step's ID, which keeps the row's open state while the run grows. */
  key: string
  turnId: string
  items: ChatItem[]
  /** When the item before the run was last written, where the run's time starts. */
  from?: string
  /** The run is the request's latest work, still going on. */
  live: boolean
  /** It waits for an ask of the agent's to be answered. */
  waiting: boolean
}

/**
 * Whether an item is a step of the agent's work. An ask that waits for you isn't: it stays
 * outside the fold until it is answered.
 */
export function isStep(i: ChatItem) {
  if (i.kind === "tool" || i.kind === "thought") return true
  if (i.kind === "permission" || i.kind === "question")
    return i.status !== "pending"
  return false
}

const pendingAsk = (e: Entry) =>
  e.kind === "item" &&
  (e.item.kind === "permission" || e.item.kind === "question") &&
  e.item.status === "pending"

/**
 * Folds each run of steps between the agent's messages into one entry. The agent's messages,
 * yours, plans, markers and asks that wait stay as they are. A run that is all one thought
 * stays a thought, which folds already. `openTurn` is the turn still underway.
 */
export function foldSteps(
  items: ChatItem[],
  turns: Map<string, ChatTurn>,
  openTurn?: string
): Entry[] {
  const out: Entry[] = []
  let run: ChatItem[] = []
  let from: string | undefined
  const flush = () => {
    if (run.length === 1 && run[0].kind === "thought")
      out.push({ kind: "item", item: run[0] })
    else if (run.length)
      out.push({
        kind: "run",
        key: run[0].id,
        turnId: run[0].turnId,
        items: run,
        from,
        live: false,
        waiting: false,
      })
    run = []
  }
  items.forEach((it, i) => {
    if (run.length && it.turnId !== run[0].turnId) flush()
    if (!isStep(it)) {
      flush()
      out.push({ kind: "item", item: it })
      return
    }
    if (!run.length) {
      const before = i > 0 ? items[i - 1] : undefined
      from = before?.turnId === it.turnId ? before.updatedAt : undefined
    }
    run.push(it)
  })
  flush()

  // What follows each run in its turn decides whether it is still going on, and whether its
  // last step ended the request.
  for (let k = out.length - 1; k >= 0; k--) {
    const r = out[k]
    if (r.kind !== "run") continue
    const after: Entry[] = []
    for (let j = k + 1; j < out.length; j++) {
      const e = out[j]
      const id = e.kind === "run" ? e.turnId : e.item.turnId
      if (id !== r.turnId) break
      after.push(e)
    }
    if (r.turnId === openTurn && after.every(pendingAsk)) {
      r.live = true
      r.waiting = after.length > 0
      continue
    }
    const last = r.items.at(-1)!
    const final = after.every(
      (e) =>
        e.kind === "item" &&
        (e.item.kind === "marker" || e.item.kind === "plan")
    )
    if (
      final &&
      turns.get(r.turnId)?.endedAt &&
      last.kind === "tool" &&
      last.tool?.status === "failed"
    ) {
      const ended: Entry = { kind: "item", item: last, ended: true }
      if (r.items.length === 1) out[k] = ended
      else {
        r.items = r.items.slice(0, -1)
        out.splice(k + 1, 0, ended)
      }
    }
  }
  return out
}

/** A file a run edited, and the lines it added and removed there. */
export type EditedFile = { path: string; added: number; removed: number }

/** The files a run's steps edited, in the order it first edited them. */
export function editedFiles(items: ChatItem[]): EditedFile[] {
  const files = new Map<string, EditedFile>()
  for (const i of items) {
    if (i.kind !== "tool") continue
    for (const c of i.tool?.content ?? []) {
      if (c.type !== "diff" || !c.path) continue
      const f = files.get(c.path) ?? { path: c.path, added: 0, removed: 0 }
      for (const r of diffLines(c.oldText ?? "", c.newText ?? "")) {
        if (r.op === "+") f.added++
        else if (r.op === "-") f.removed++
      }
      files.set(c.path, f)
    }
  }
  return [...files.values()]
}

const plural = (n: number, one: string, many: string) => (n === 1 ? one : many)

/** What a run's steps did, in words: "Thought, searched once, read 2 files". */
export function stepSummary(items: ChatItem[]): string {
  const counts = new Map<string, number>()
  for (const i of items) {
    const key =
      i.kind === "tool"
        ? i.tool?.kind === "think"
          ? "thought"
          : (i.tool?.kind ?? "other")
        : i.kind === "question"
          ? "permission"
          : i.kind
    counts.set(key, (counts.get(key) ?? 0) + 1)
  }
  const words = [...counts].map(([k, n]) => {
    switch (k) {
      case "thought":
        return "thought"
      case "read":
        return `read ${n} ${plural(n, "file", "files")}`
      case "search":
        return n === 1 ? "searched once" : `searched ${n} times`
      case "execute":
        return `ran ${n} ${plural(n, "command", "commands")}`
      case "edit":
        return `edited ${n} ${plural(n, "time", "times")}`
      case "delete":
        return `deleted ${n} ${plural(n, "file", "files")}`
      case "move":
        return `moved ${n} ${plural(n, "file", "files")}`
      case "fetch":
        return `fetched ${n} ${plural(n, "page", "pages")}`
      case "switch_mode":
        return "switched mode"
      case "permission":
        return n === 1 ? "asked you once" : `asked you ${n} times`
      default:
        return `used ${n} ${plural(n, "tool", "tools")}`
    }
  })
  const text = words.join(", ")
  return text.charAt(0).toUpperCase() + text.slice(1)
}

/** A time span as "8s", "1m 52s" or "2h 5m". */
export function spanLabel(ms: number) {
  const s = Math.max(0, Math.round(ms / 1000))
  if (s < 60) return `${s}s`
  const m = Math.floor(s / 60)
  if (m < 60) return `${m}m ${String(s % 60).padStart(2, "0")}s`
  return `${Math.floor(m / 60)}h ${m % 60}m`
}

/** A running clock as "0:52" or "12:05". */
export function clockLabel(ms: number) {
  const s = Math.max(0, Math.floor(ms / 1000))
  const m = Math.floor(s / 60)
  return `${m}:${String(s % 60).padStart(2, "0")}`
}
