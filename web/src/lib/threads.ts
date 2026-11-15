import type { Approval, Terminal } from "@/lib/api/client"
import type { Chat } from "@/lib/chats"

/**
 * A teammate's threads (docs/design/teammate-computer.md): its chats, and its agent working in
 * a terminal with no chat behind it, which Start work's "in a terminal" starts.
 */
export type ThreadStatus = "working" | "waiting" | "terminal" | "done" | "ended"

/**
 * The approvals a chat waits on: its own permission asks, and its computer's other asks while
 * it is the teammate's latest chat there, as a push it asks for is.
 */
export function chatApprovals(
  chat: Chat,
  approvals: Approval[],
  chats: Chat[]
): Approval[] {
  const latest = chats
    .filter(
      (c) => c.teammateId === chat.teammateId && c.sandboxId === chat.sandboxId
    )
    .reduce<Chat | undefined>(
      (a, c) => (!a || c.updatedAt > a.updatedAt ? c : a),
      undefined
    )
  return approvals.filter((a) =>
    a.chatPermission
      ? a.chatPermission.chatId === chat.id
      : latest?.id === chat.id &&
        a.sandboxId === chat.sandboxId &&
        (!a.teammate || a.teammate.id === chat.teammateId)
  )
}

export function chatStatus(chat: Chat, waiting: number): ThreadStatus {
  if (waiting > 0) return "waiting"
  if (chat.state === "running") return "working"
  if (chat.state === "terminal") return "terminal"
  if (chat.state === "orphaned") return "ended"
  return "done"
}

export const statusLabels: Record<ThreadStatus, string> = {
  working: "working",
  waiting: "waiting",
  terminal: "in the terminal",
  done: "done",
  ended: "ended",
}

/** A teammate's agent in a terminal of one of its computers, with no chat behind it. */
export type AgentTerminal = Terminal & { sandboxId: string }

export function agentTerminals(
  list:
    | readonly { sandboxId: string; terminals?: readonly Terminal[] | null }[]
    | null
    | undefined,
  computers: Set<string>
): AgentTerminal[] {
  return (list ?? []).flatMap(({ sandboxId, terminals }) =>
    computers.has(sandboxId)
      ? (terminals ?? [])
          .filter((t) => t.harness && !t.chat)
          .map((t) => ({ ...t, sandboxId }))
      : []
  )
}

/** How long a finished thread stays among the sidebar's recent ones. */
const recentMs = 24 * 60 * 60 * 1000

/** How many finished threads the sidebar shows under a teammate besides the live ones. */
const recentCount = 2

/**
 * The chats the sidebar lists under a teammate, newest first: every one that runs, waits or is
 * in the terminal, then the two latest others of the last day, and the open one wherever it is.
 */
export function sidebarChats(
  chats: Chat[],
  waiting: (chat: Chat) => number,
  open?: string,
  now = Date.now()
): Chat[] {
  const sorted = [...chats].sort((a, b) =>
    b.updatedAt.localeCompare(a.updatedAt)
  )
  const live = (c: Chat) =>
    c.state === "running" || c.state === "terminal" || waiting(c) > 0
  const recent = sorted
    .filter((c) => !live(c) && now - Date.parse(c.updatedAt) < recentMs)
    .slice(0, recentCount)
  return sorted.filter((c) => live(c) || recent.includes(c) || c.id === open)
}
