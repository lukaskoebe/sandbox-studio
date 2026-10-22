import { useMemo } from "react"
import { phase } from "@/components/status-badge"
import { $api } from "@/lib/api/client"

export type Presence = "working" | "waiting" | "idle" | "offline"

export const presenceLabels: Record<Presence, string> = {
  working: "Working",
  waiting: "Waiting for you",
  idle: "Idle",
  offline: "Offline",
}

/**
 * Each teammate's presence in an environment, from the chats and approvals the UI loads anyway.
 * A teammate is waiting for you while one of its approvals is pending: a browser action or
 * credential, a memory share, or a tool permission in one of its chats. It is working while one
 * of its chats runs. It is offline while none of its sandboxes runs: stopped, suspended or
 * failed, it can't answer until one starts. Otherwise it is idle.
 */
export function usePresence(env: string): (teammateId: string) => Presence {
  const chats = $api.useQuery("get", "/api/environments/{env}/chats", {
    params: { path: { env } },
  })
  const approvals = $api.useQuery(
    "get",
    "/api/approvals",
    { params: { query: { status: "pending" } } },
    { refetchInterval: 15000 }
  )
  const sandboxes = $api.useQuery("get", "/api/environments/{env}/sandboxes", {
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
    const working = new Set(
      (chats.data ?? [])
        .filter((c) => c.state === "running")
        .map((c) => c.teammateId)
    )
    const owners = new Set<string>()
    const up = new Set<string>()
    for (const sb of sandboxes.data ?? []) {
      if (!sb.teammateId) continue
      owners.add(sb.teammateId)
      const p = phase(sb)
      if (p === "ready" || p === "booting" || p === "creating")
        up.add(sb.teammateId)
    }
    return (teammateId: string): Presence =>
      waiting.has(teammateId)
        ? "waiting"
        : working.has(teammateId)
          ? "working"
          : owners.has(teammateId) && !up.has(teammateId)
            ? "offline"
            : "idle"
  }, [env, chats.data, approvals.data, sandboxes.data])
}
