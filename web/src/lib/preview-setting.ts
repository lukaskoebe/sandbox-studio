import { useQueryClient } from "@tanstack/react-query"
import { toast } from "sonner"
import { $api, errorMessage } from "@/lib/api/client"

/**
 * Whether Studio's pages offer previews of sandbox ports, from Settings → General, and whether
 * Home shows the one-time notice that they are off. Both read false until the setting loads.
 * The preview API works either way; only the pages hide the ways in.
 */
export function usePreviewSetting() {
  const queryClient = useQueryClient()
  const setting = $api.useQuery("get", "/api/system/previews", undefined, {
    staleTime: 30_000,
  })
  const set = $api.useMutation("put", "/api/system/previews", {
    onSuccess: (data) =>
      queryClient.setQueryData(["get", "/api/system/previews"], data),
    onError: (err) =>
      toast.error("Could not change previews", {
        description: errorMessage(err),
      }),
  })
  return {
    enabled: !!setting.data?.enabled,
    notice: !!setting.data?.notice,
    loaded: setting.data !== undefined,
    pending: set.isPending,
    setEnabled: (enabled: boolean) => set.mutate({ body: { enabled } }),
  }
}
