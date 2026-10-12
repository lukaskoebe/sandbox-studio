import { useMemo } from "react"
import { $api } from "@/lib/api/client"

export type Presence = "working" | "waiting" | "idle"

export const presenceLabels: Record<Presence, string> = {
  working: "Working",
  waiting: "Waiting for you",
  idle: "Idle",
}

/**
 * Each teammate's presence in an environment, from the chats and approvals the UI loads anyway.
 * A teammate is waiting for you while one of its approvals is pending: a browser action or
 * credential, a memory share, or a tool permission in one of its chats. It is working while one
 * of its chats runs. Otherwise it is idle.
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
        a.memoryShare?.teammateId ??
        chatTeammate.get(a.chatPermission?.chatId ?? "")
      if (teammate) waiting.add(teammate)
    }
    const working = new Set(
      (chats.data ?? [])
        .filter((c) => c.state === "running")
        .map((c) => c.teammateId)
    )
    return (teammateId: string): Presence =>
      waiting.has(teammateId)
        ? "waiting"
        : working.has(teammateId)
          ? "working"
          : "idle"
  }, [env, chats.data, approvals.data])
}
