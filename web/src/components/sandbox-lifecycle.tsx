import { useQueryClient } from "@tanstack/react-query"
import { toast } from "sonner"
import { PlayIcon, StopIcon } from "@phosphor-icons/react"
import { Button } from "@/components/ui/button"
import { Spinner } from "@/components/ui/spinner"
import { phase, phaseDot } from "@/components/status-badge"
import { StatusDot } from "@/components/status-dot"
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

/**
 * What brings a sandbox that is down back: Start for a stopped or crashed one, Resume for a
 * suspended one. A failed create has no guest to start, a sandbox microsandbox no longer
 * knows ("absent") has no VM to start, and a running sandbox needs neither.
 */
export function wakeAction(sb: Sandbox): "start" | "resume" | undefined {
  const p = phase(sb)
  if (p === "suspended") return "resume"
  if (p === "stopped" || sb.status === "crashed") return "start"
  return undefined
}

/** Start or Resume, whichever brings the sandbox back; nothing while it runs. */
export function WakeButton({
  env,
  sandbox,
  size,
  variant,
}: {
  env: string
  sandbox: Sandbox
  size?: "sm" | "xs"
  variant?: "default" | "outline"
}) {
  const { path, start, resume } = useSandboxLifecycle(env, sandbox)
  const action = wakeAction(sandbox)
  if (!action) return null
  const mutation = action === "resume" ? resume : start
  return (
    <Button
      size={size}
      variant={variant}
      disabled={mutation.isPending}
      onClick={(e) => {
        // The button sits on cards that are links.
        e.preventDefault()
        e.stopPropagation()
        mutation.mutate(path)
      }}
    >
      {mutation.isPending ? <Spinner /> : <PlayIcon />}
      {action === "resume" ? "Resume" : "Start"}
    </Button>
  )
}

/**
 * Above a chat's composer while the chat's sandbox is down: messages can't go until it runs
 * again, so the banner says so before anything is typed, with the button that brings it back.
 */
export function SandboxStateBanner({
  env,
  sandbox,
}: {
  env: string
  sandbox?: Sandbox
}) {
  if (!sandbox) return null
  const action = wakeAction(sandbox)
  const p = phase(sandbox)
  if (sandbox.status === "absent")
    return (
      <div
        role="status"
        className="flex items-center gap-2 rounded-lg border bg-muted/50 px-3 py-1.5 text-xs"
      >
        <StatusDot {...phaseDot(p)} compact />
        <span className="min-w-0 flex-1">
          microsandbox lost the record of{" "}
          <span className="font-mono">{sandbox.name}</span>&apos;s VM, so it
          can&apos;t start.{" "}
          <span className="text-muted-foreground">Its disks are kept.</span>
        </span>
      </div>
    )
  if (!action) return null
  const what =
    p === "suspended"
      ? "is suspended"
      : p === "failed"
        ? "stopped unexpectedly"
        : "is stopped"
  return (
    <div
      role="status"
      className="flex items-center gap-2 rounded-lg border bg-muted/50 px-3 py-1.5 text-xs"
    >
      <StatusDot {...phaseDot(p)} compact />
      <span className="min-w-0 flex-1">
        <span className="font-mono">{sandbox.name}</span> {what}.{" "}
        <span className="text-muted-foreground">
          {action === "resume" ? "Resume" : "Start"} it to send a message.
        </span>
      </span>
      <WakeButton env={env} sandbox={sandbox} size="sm" />
    </div>
  )
}
