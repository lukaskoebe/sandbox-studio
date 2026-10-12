import { useQueryClient } from "@tanstack/react-query"
import { toast } from "sonner"
import { PlayIcon, StopIcon } from "@phosphor-icons/react"
import { Button } from "@/components/ui/button"
import { Spinner } from "@/components/ui/spinner"
import { phase } from "@/components/status-badge"
import { $api, errorMessage, type Sandbox } from "@/lib/api/client"

/** Refetches the sandbox and the list after a change to it. */
export function useInvalidateSandboxes() {
  const queryClient = useQueryClient()
  return () => {
    queryClient.invalidateQueries({
      queryKey: ["get", "/api/environments/{env}/sandboxes"],
    })
    queryClient.invalidateQueries({
      queryKey: ["get", "/api/environments/{env}/sandboxes/{id}"],
    })
  }
}

/**
 * The start, stop, suspend and resume mutations of one sandbox. Suspend pauses a running
 * sandbox in place (processes, terminals and agent TUIs freeze); resume continues it.
 */
export function useSandboxLifecycle(env: string, sb: Sandbox) {
  const invalidate = useInvalidateSandboxes()
  const path = { params: { path: { env, id: sb.id } } }
  const onError = (what: string) => (err: unknown) =>
    toast.error(`Could not ${what} the sandbox`, {
      description: errorMessage(err),
    })
  const start = $api.useMutation(
    "post",
    "/api/environments/{env}/sandboxes/{id}/start",
    { onSettled: invalidate, onError: onError("start") }
  )
  const stop = $api.useMutation(
    "post",
    "/api/environments/{env}/sandboxes/{id}/stop",
    { onSettled: invalidate, onError: onError("stop") }
  )
  const suspend = $api.useMutation(
    "post",
    "/api/environments/{env}/sandboxes/{id}/suspend",
    { onSettled: invalidate, onError: onError("suspend") }
  )
  const resume = $api.useMutation(
    "post",
    "/api/environments/{env}/sandboxes/{id}/resume",
    { onSettled: invalidate, onError: onError("resume") }
  )
  return { path, start, stop, suspend, resume, phase: phase(sb) }
}

/** Starts a stopped or failed sandbox. */
export function StartButton({
  env,
  sandbox,
}: {
  env: string
  sandbox: Sandbox
}) {
  const { path, start } = useSandboxLifecycle(env, sandbox)
  return (
    <Button disabled={start.isPending} onClick={() => start.mutate(path)}>
      {start.isPending ? <Spinner /> : <PlayIcon />}
      Start
    </Button>
  )
}

/** Stops a running, booting or suspended sandbox; its disk is kept. */
export function StopButton({
  env,
  sandbox,
}: {
  env: string
  sandbox: Sandbox
}) {
  const { path, stop } = useSandboxLifecycle(env, sandbox)
  return (
    <Button
      variant="outline"
      disabled={stop.isPending}
      onClick={() => stop.mutate(path)}
    >
      {stop.isPending ? <Spinner /> : <StopIcon />}
      Stop
    </Button>
  )
}
