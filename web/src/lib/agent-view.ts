import { useQueryClient } from "@tanstack/react-query"
import { $api } from "@/lib/api/client"

/** How agent sessions open by default; empty until the user chooses. */
export type AgentView = "chat" | "terminal" | ""

/** The agent view preference and a setter; the setter updates every reader at once. */
export function useAgentView() {
  const queryClient = useQueryClient()
  const query = $api.useQuery("get", "/api/settings/ui")
  const set = $api.useMutation("put", "/api/settings/ui", {
    onSuccess: (data) => {
      queryClient.setQueryData(["get", "/api/settings/ui"], data)
      // A chosen work mode is a step of the guided setup.
      queryClient.invalidateQueries({ queryKey: ["get", "/api/setup"] })
    },
  })
  const view: AgentView | undefined = query.data
    ? query.data.asked
      ? query.data.agentView
      : ""
    : undefined
  return {
    view,
    isPending: query.isPending,
    set: {
      isPending: set.isPending,
      mutate: (
        agentView: "chat" | "terminal",
        opts?: { onError?: (err: unknown) => void }
      ) => set.mutate({ body: { agentView } }, opts),
    },
  }
}
