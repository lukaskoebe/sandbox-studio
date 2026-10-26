import { useMemo } from "react"
import { phase } from "@/components/status-badge"
import { usePendingApprovals } from "@/hooks/use-pending-approvals"
import { $api } from "@/lib/api/client"

export type Presence = "working" | "waiting" | "idle" | "offline"

export const presenceLabels: Record<Presence, string> = {
  working: "Working",
  waiting: "Waiting for you",
  idle: "Idle",
  offline: "Offline",
}

/** A terminal that says what a teammate does: one its harness works in, or one a program runs in. */
export type TerminalActivity = {
  sandboxId: string
  name: string
  label: string
  program: string
  state: "working" | "busy"
}

/** A teammate's presence, and what it is busy with. */
export type Activity = {
  presence: Presence
  /** The chat that runs, while one does. */
  chat?: { id: string; title: string }
  /**
   * While no chat runs, the terminal its harness works in; while it is idle, one a program
   * runs in, such as a dev server, which isn't work.
   */
  terminal?: TerminalActivity
}

/**
 * Each teammate's presence in an environment, from the chats, approvals and terminals the UI
 * loads anyway. A teammate is waiting for you while one of its approvals is pending: a browser
 * action or credential, a memory share, or a tool permission in one of its chats. It is
 * working while one of its chats runs, or while an agent harness prints in one of its
 * terminals. It is offline while none of its sandboxes runs: stopped, suspended or failed, it
 * can't answer until one starts. Otherwise it is idle.
 */
export function useActivity(env: string): (teammateId: string) => Activity {
  const chats = $api.useQuery("get", "/api/environments/{env}/chats", {
    params: { path: { env } },
  })
  const approvals = usePendingApprovals()
  const sandboxes = $api.useQuery("get", "/api/environments/{env}/sandboxes", {
    params: { path: { env } },
  })
  const terminals = $api.useQuery("get", "/api/environments/{env}/terminals", {
    params: { path: { env } },
  })
  return useMemo(() => {
    const chatTeammate = new Map(
      (chats.data ?? []).map((c) => [c.id, c.teammateId])
    )
    const waiting = new Set<string>()
    for (const a of approvals.data ?? []) {
      if (a.environmentId !== env) continue
      const teammate =
        a.browserAction?.teammateId ??
        a.browserCredential?.teammateId ??
        a.browserControl?.teammateId ??
        a.memoryShare?.teammateId ??
        chatTeammate.get(a.chatPermission?.chatId ?? "")
      if (teammate) waiting.add(teammate)
    }
    const running = new Map<string, { id: string; title: string }>()
    for (const c of chats.data ?? []) {
      if (c.state === "running" && !running.has(c.teammateId))
        running.set(c.teammateId, { id: c.id, title: c.title })
    }
    const owners = new Set<string>()
    const up = new Set<string>()
    const ownerOf = new Map<string, string>()
    for (const sb of sandboxes.data ?? []) {
      if (!sb.teammateId) continue
      owners.add(sb.teammateId)
      ownerOf.set(sb.id, sb.teammateId)
      const p = phase(sb)
      if (p === "ready" || p === "booting" || p === "creating")
        up.add(sb.teammateId)
    }
    // Per teammate the working terminal that printed last, else the busy one that did.
    const busiest = new Map<string, TerminalActivity & { activity: number }>()
    for (const { sandboxId, terminals: list } of terminals.data ?? []) {
      const teammate = ownerOf.get(sandboxId)
      if (!teammate) continue
      for (const t of list ?? []) {
        if (t.state === "idle" || !t.program) continue
        const seen = busiest.get(teammate)
        const rank = (s: string) => (s === "working" ? 1 : 0)
        if (
          seen &&
          (rank(seen.state) > rank(t.state) ||
            (seen.state === t.state && seen.activity >= (t.activity ?? 0)))
        )
          continue
        busiest.set(teammate, {
          sandboxId,
          name: t.name,
          label: t.label,
          program: t.program,
          state: t.state,
          activity: t.activity ?? 0,
        })
      }
    }
    return (teammateId: string): Activity => {
      const chat = running.get(teammateId)
      const found = busiest.get(teammateId)
      const terminal = found && {
        sandboxId: found.sandboxId,
        name: found.name,
        label: found.label,
        program: found.program,
        state: found.state,
      }
      if (waiting.has(teammateId)) return { presence: "waiting", chat }
      if (chat) return { presence: "working", chat }
      if (terminal?.state === "working")
        return { presence: "working", terminal }
      if (owners.has(teammateId) && !up.has(teammateId))
        return { presence: "offline" }
      return { presence: "idle", terminal }
    }
  }, [env, chats.data, approvals.data, sandboxes.data, terminals.data])
}

/** Each teammate's presence in an environment; see useActivity. */
export function usePresence(env: string): (teammateId: string) => Presence {
  const activity = useActivity(env)
  return useMemo(() => (id: string) => activity(id).presence, [activity])
}

/**
 * What a teammate does, after its presence: "in Fix broken links", "opencode in api",
 * "npm run dev running". A terminal whose label is its program goes unnamed.
 */
export function activityDetail(a: Activity): string | undefined {
  if (a.presence === "working" && a.chat) return `in ${a.chat.title}`
  const t = a.terminal
  if (!t) return undefined
  if (t.label !== t.program) return `${t.program} in ${t.label}`
  return t.state === "working"
    ? `${t.program} in a terminal`
    : `${t.program} running`
}
