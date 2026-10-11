/**
 * STUB: endpoints from docs/design/chat-mode.md that the backend doesn't serve yet. They are
 * built on the chat-backend2 branch; once it is merged and schema.gen.ts has them, replace
 * each function with its $api/fetchClient call and delete this file.
 *
 * - getUISettings / setUISettings: GET and PUT /api/settings/ui, `{agentView}`. Until the
 *   endpoint exists, the choice is kept in this browser's localStorage.
 * - openChatInTerminal / returnToChat: POST /api/environments/{env}/chats/{chat}/terminal
 *   and /return. Until they exist, they fail with a message saying so.
 */
import { useQueryClient, useMutation, useQuery } from "@tanstack/react-query"

/** How agent sessions open by default; empty until the user chooses. */
export type AgentView = "chat" | "terminal" | ""

export type UISettings = { agentView: AgentView }

/** Problem-style error, so errorMessage and errorStatus read it like an API error. */
class PendingError extends Error {
  constructor(
    public detail: string,
    public status: number
  ) {
    super(detail)
  }
}

const localKey = "studio.agentView"

function localSettings(): UISettings {
  const v = localStorage.getItem(localKey)
  return { agentView: v === "chat" || v === "terminal" ? v : "" }
}

async function request<T>(path: string, init?: RequestInit): Promise<T | null> {
  const res = await fetch(path, {
    ...init,
    headers: { "Content-Type": "application/json", ...init?.headers },
  })
  if (res.status === 404 || res.status === 405) return null
  if (res.status === 401) window.dispatchEvent(new Event("studio-auth-expired"))
  if (!res.ok) {
    const body = (await res.json().catch(() => ({}))) as { detail?: string }
    throw new PendingError(body.detail ?? res.statusText, res.status)
  }
  return res.status === 204 ? ({} as T) : ((await res.json()) as T)
}

export async function getUISettings(): Promise<UISettings> {
  return (await request<UISettings>("/api/settings/ui")) ?? localSettings()
}

export async function setUISettings(s: UISettings): Promise<UISettings> {
  const saved = await request<UISettings>("/api/settings/ui", {
    method: "PUT",
    body: JSON.stringify(s),
  })
  if (saved) return saved
  localStorage.setItem(localKey, s.agentView)
  return s
}

const uiKey = ["pending", "/api/settings/ui"]

/** The agent view preference and a setter; the setter updates every reader at once. */
export function useAgentView() {
  const queryClient = useQueryClient()
  const query = useQuery({ queryKey: uiKey, queryFn: getUISettings })
  const set = useMutation({
    mutationFn: (agentView: AgentView) => setUISettings({ agentView }),
    onSuccess: (data) => queryClient.setQueryData(uiKey, data),
  })
  return { view: query.data?.agentView, isPending: query.isPending, set }
}

/** Hands an idle chat to its harness's TUI; returns the tmux session to show. */
export async function openChatInTerminal(
  env: string,
  chat: string
): Promise<{ name: string }> {
  const res = await request<{ name: string }>(
    `/api/environments/${encodeURIComponent(env)}/chats/${encodeURIComponent(chat)}/terminal`,
    { method: "POST" }
  )
  if (!res)
    throw new PendingError(
      "Opening a chat in the terminal isn't available in this version of Studio yet.",
      501
    )
  return res
}

/** Ends the chat's TUI and continues the conversation in the chat. */
export async function returnToChat(env: string, chat: string): Promise<void> {
  const res = await request<object>(
    `/api/environments/${encodeURIComponent(env)}/chats/${encodeURIComponent(chat)}/return`,
    { method: "POST" }
  )
  if (!res)
    throw new PendingError(
      "Continuing in the chat isn't available in this version of Studio yet.",
      501
    )
}
