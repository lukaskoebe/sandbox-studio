import { useCallback, useSyncExternalStore } from "react"
import { useNavigate } from "@tanstack/react-router"
import { $api } from "@/lib/api/client"
import { newThreadWith } from "@/lib/new-thread"

/**
 * Whether "Who should do it?" (components/new-chat-dialog.tsx) is open. New chat on the Chats
 * page, ⌘K and /chats/new ask for it; with one teammate it goes straight to its home.
 */
let open = false
const listeners = new Set<() => void>()

export function setNewChatOpen(next: boolean) {
  if (open === next) return
  open = next
  for (const l of listeners) l()
}

export const openNewChat = () => setNewChatOpen(true)

export function useNewChatOpen() {
  return useSyncExternalStore(
    (onChange) => {
      listeners.add(onChange)
      return () => listeners.delete(onChange)
    },
    () => open
  )
}

/** Teammates with the one whose thread moved last first, then the rest in their order. */
export function lastUsedFirst<T extends { id: string }>(
  teammates: T[],
  chats: { teammateId: string; updatedAt: string }[]
): T[] {
  const last = new Map<string, string>()
  for (const c of chats)
    if ((last.get(c.teammateId) ?? "") < c.updatedAt)
      last.set(c.teammateId, c.updatedAt)
  return [...teammates].sort((a, b) =>
    (last.get(b.id) ?? "").localeCompare(last.get(a.id) ?? "")
  )
}

/**
 * Starts a new chat: with one teammate its home opens with the composer focused, with more
 * "Who should do it?" asks first.
 */
export function useStartNewChat(env: string) {
  const navigate = useNavigate()
  const teammates = $api.useQuery("get", "/api/environments/{env}/teammates", {
    params: { path: { env } },
  })
  const only = teammates.data?.length === 1 ? teammates.data[0].id : undefined
  return useCallback(() => {
    if (!only) return openNewChat()
    newThreadWith(only)
    void navigate({
      to: "/e/$env/teammates/$id",
      params: { env, id: only },
    })
  }, [env, navigate, only])
}
