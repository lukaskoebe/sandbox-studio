import { useQueryClient } from "@tanstack/react-query"
import { toast } from "sonner"
import { PauseIcon, PlayIcon } from "@phosphor-icons/react"
import { Button } from "@/components/ui/button"
import { Spinner } from "@/components/ui/spinner"
import { phase } from "@/components/status-badge"
import { $api, errorMessage, type Sandbox } from "@/lib/api/client"

/**
 * Suspend pauses a running sandbox in place (processes, terminals and agent TUIs freeze);
 * Resume continues it where it stopped. Shown only when one of them applies.
 */
export function SuspendResumeButton({
  env,
  sandbox: sb,
}: {
  env: string
  sandbox: Sandbox
}) {
  const queryClient = useQueryClient()
  const invalidate = () => {
    queryClient.invalidateQueries({
      queryKey: ["get", "/api/environments/{env}/sandboxes"],
    })
    queryClient.invalidateQueries({
      queryKey: ["get", "/api/environments/{env}/sandboxes/{id}"],
    })
  }
  const path = { params: { path: { env, id: sb.id } } }
  const suspend = $api.useMutation(
    "post",
    "/api/environments/{env}/sandboxes/{id}/suspend",
    {
      onSettled: invalidate,
      onError: (err) =>
        toast.error("Could not suspend the sandbox", {
          description: errorMessage(err),
        }),
    }
  )
  const resume = $api.useMutation(
    "post",
    "/api/environments/{env}/sandboxes/{id}/resume",
    {
      onSettled: invalidate,
      onError: (err) =>
        toast.error("Could not resume the sandbox", {
          description: errorMessage(err),
        }),
    }
  )
  const p = phase(sb)

  if (p === "suspended") {
    return (
      <Button
        variant="ghost"
        size="sm"
        disabled={resume.isPending}
        onClick={() => resume.mutate(path)}
      >
        {resume.isPending ? <Spinner /> : <PlayIcon />}
        Resume
      </Button>
    )
  }
  if (p !== "ready") return null
  return (
    <Button
      variant="ghost"
      size="sm"
      title="Pause the VM in place; everything continues on resume"
      disabled={suspend.isPending}
      onClick={() => suspend.mutate(path)}
    >
      {suspend.isPending ? <Spinner /> : <PauseIcon />}
      Suspend
    </Button>
  )
}
