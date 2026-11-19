import { useRef, useState } from "react"
import { useQueryClient } from "@tanstack/react-query"
import { toast } from "sonner"
import { PlayIcon, WrenchIcon } from "@phosphor-icons/react"
import { Button } from "@/components/ui/button"
import { Spinner } from "@/components/ui/spinner"
import { phase, phaseDot } from "@/components/status-badge"
import { StatusDot } from "@/components/status-dot"
import { MoveDisksButton } from "@/components/move-disks"
import { $api, errorMessage, fetchClient, type Sandbox } from "@/lib/api/client"

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
  const repair = $api.useMutation(
    "post",
    "/api/environments/{env}/sandboxes/{id}/repair-vm",
    { onSettled: invalidate, onError: onError("repair") }
  )
  return { path, start, stop, suspend, resume, repair, phase: phase(sb) }
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

/**
 * Starts a stopped sandbox, or resumes a suspended one, as soon as the user types to its agent,
 * so that it is up, or nearly, by the time the message goes: wake() on input, once per
 * sandbox until it fails, and ready() before sending, which waits for the start it began.
 * One that can't start by itself (a lost VM, disks in the old layout) is left to its banner.
 */
export function useWakeOnType(env: string, sandbox?: Sandbox) {
  const invalidate = useInvalidateSandboxes()
  const waking = useRef<{ id: string; done: Promise<void> }>(undefined)
  const [wakingId, setWakingId] = useState<string>()
  const wake = () => {
    if (!sandbox || waking.current?.id === sandbox.id) return
    const action = wakeAction(sandbox)
    if (!action || sandbox.oldDiskLayout || sandbox.status === "absent") return
    const id = sandbox.id
    const name = sandbox.name
    const params = { params: { path: { env, id } } }
    const done = (
      action === "resume"
        ? fetchClient.POST(
            "/api/environments/{env}/sandboxes/{id}/resume",
            params
          )
        : fetchClient.POST(
            "/api/environments/{env}/sandboxes/{id}/start",
            params
          )
    )
      .then(({ error }) => {
        if (error) throw error
      })
      .catch((err: unknown) => {
        toast.error(`Could not ${action} ${name}`, {
          description: errorMessage(err),
        })
        // Typing again tries again.
        if (waking.current?.done === done) waking.current = undefined
      })
      .finally(() => {
        setWakingId((w) => (w === id ? undefined : w))
        invalidate()
      })
    waking.current = { id, done }
    setWakingId(id)
  }
  const ready = () =>
    waking.current && waking.current.id === sandbox?.id
      ? waking.current.done
      : undefined
  return { wake, ready, waking: !!sandbox && wakingId === sandbox.id }
}

/**
 * Why a sandbox whose VM microsandbox no longer knows ("absent") can't start, typically a
 * damaged microsandbox database rebuilt without it, and what to do: Repair when its disks
 * are still on hand.
 */
export function lostVM(sb: Sandbox) {
  return sb.repairable
    ? "microsandbox lost the record of this sandbox's VM, so it can't start. Its disks are kept: Repair gives the VM a new record around them, unchanged, and starts it."
    : "microsandbox lost the record of this sandbox's VM, and its disks aren't in microsandbox's home either, so it can't start."
}

/** Repair, for a sandbox whose VM microsandbox lost while its disks are kept. */
export function RepairButton({
  env,
  sandbox,
  size,
  className,
}: {
  env: string
  sandbox: Sandbox
  size?: "sm" | "xs"
  className?: string
}) {
  const { path, repair } = useSandboxLifecycle(env, sandbox)
  if (sandbox.status !== "absent" || !sandbox.repairable) return null
  return (
    <Button
      size={size}
      className={className}
      disabled={repair.isPending}
      onClick={(e) => {
        e.preventDefault()
        e.stopPropagation()
        repair.mutate(path)
      }}
    >
      {repair.isPending ? <Spinner /> : <WrenchIcon />}
      {repair.isPending ? "Repairing…" : "Repair"}
    </Button>
  )
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
  waking,
}: {
  env: string
  sandbox?: Sandbox
  /** The composer below started it as the user typed (useWakeOnType). */
  waking?: boolean
}) {
  if (!sandbox) return null
  if (waking)
    return (
      <div
        role="status"
        className="flex items-center gap-2 rounded-lg border bg-muted/50 px-3 py-1.5 text-xs"
      >
        <Spinner className="size-3" />
        <span className="min-w-0 flex-1">
          Starting <span className="font-mono">{sandbox.name}</span>{" "}
          <span className="text-muted-foreground">
            for your message; it goes once the computer is up.
          </span>
        </span>
      </div>
    )
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
          <span className="text-muted-foreground">
            {sandbox.repairable
              ? "Its disks are kept: repair it to start it again."
              : "Its disks aren't in microsandbox's home either."}
          </span>
        </span>
        <RepairButton env={env} sandbox={sandbox} size="sm" />
      </div>
    )
  if (!action) return null
  if (sandbox.oldDiskLayout && action === "start")
    return (
      <div
        role="status"
        className="flex items-center gap-2 rounded-lg border bg-muted/50 px-3 py-1.5 text-xs"
      >
        <StatusDot {...phaseDot(p)} compact />
        <span className="min-w-0 flex-1">
          <span className="font-mono">{sandbox.name}</span> is stopped.{" "}
          <span className="text-muted-foreground">
            microsandbox must move its disks to the new layout before it starts.
          </span>
        </span>
        <MoveDisksButton env={env} sandbox={sandbox} size="sm" />
      </div>
    )
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
          Typing a message {action === "resume" ? "resumes" : "starts"} it.
        </span>
      </span>
      <WakeButton env={env} sandbox={sandbox} size="sm" />
    </div>
  )
}
