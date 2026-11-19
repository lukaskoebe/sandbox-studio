import { useQueryClient } from "@tanstack/react-query"
import { toast } from "sonner"
import { $api, errorMessage } from "@/lib/api/client"

/**
 * Whether chats show every step of an agent's work as its own card, from Settings → General.
 * Off, the default, folds each run of steps between the agent's messages into one row. It reads
 * false until the setting loads.
 */
export function useChatStepsSetting() {
  const queryClient = useQueryClient()
  const setting = $api.useQuery("get", "/api/system/chat-steps", undefined, {
    staleTime: 30_000,
  })
  const set = $api.useMutation("put", "/api/system/chat-steps", {
    onSuccess: (data) =>
      queryClient.setQueryData(["get", "/api/system/chat-steps"], data),
    onError: (err) =>
      toast.error("Could not change how chats show steps", {
        description: errorMessage(err),
      }),
  })
  return {
    every: set.isPending ? set.variables.body.every : !!setting.data?.every,
    loaded: setting.data !== undefined,
    pending: set.isPending,
    setEvery: (every: boolean) => set.mutate({ body: { every } }),
  }
}
