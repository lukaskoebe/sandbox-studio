import { useEffect, useState } from "react"
import { toast } from "sonner"
import { Button } from "@/components/ui/button"
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog"
import {
  Field,
  FieldDescription,
  FieldError,
  FieldLabel,
} from "@/components/ui/field"
import { Input } from "@/components/ui/input"
import { Spinner } from "@/components/ui/spinner"
import { useInvalidateSandboxes } from "@/components/sandbox-lifecycle"
import { formatMiB } from "@/components/sandbox-details"
import { $api, errorMessage, type Sandbox } from "@/lib/api/client"
import type { components } from "@/lib/api/schema.gen"

type Applied = components["schemas"]["ResizeApplied"]

/** What a sandbox's VM has now and what it starts with, which differ while a resize waits. */
export function sandboxSize(sb: Sandbox) {
  const waits = sb.status === "running" || sb.status === "suspended"
  const now = {
    cpus: (waits && sb.liveCpus) || sb.cpus,
    memoryMiB: (waits && sb.liveMemoryMiB) || sb.memoryMiB,
  }
  return {
    now,
    next: { cpus: sb.cpus, memoryMiB: sb.memoryMiB },
    waiting: now.cpus !== sb.cpus || now.memoryMiB !== sb.memoryMiB,
  }
}

/** "2 CPU · 8 GiB", then what waits for a restart: "· 12 GiB after restart". */
export function sizeLabel(sb: Sandbox) {
  const { now, next } = sandboxSize(sb)
  const label = `${now.cpus} CPU · ${formatMiB(now.memoryMiB)}`
  const later = [
    next.cpus !== now.cpus && `${next.cpus} CPU`,
    next.memoryMiB !== now.memoryMiB && formatMiB(next.memoryMiB),
  ].filter(Boolean)
  return later.length > 0
    ? `${label} · ${later.join(" · ")} after restart`
    : label
}

/** Sets a sandbox's CPUs and memory. A running sandbox takes a change live where it can. */
export function ResizeSandboxDialog({
  env,
  sandbox,
  open,
  onOpenChange,
}: {
  env: string
  sandbox: Sandbox
  open: boolean
  onOpenChange: (open: boolean) => void
}) {
  const invalidate = useInvalidateSandboxes()
  const resize = $api.useMutation(
    "put",
    "/api/environments/{env}/sandboxes/{id}/resources",
    {
      // A failed step may have changed some resources, so the sandbox is refetched either way.
      onSettled: invalidate,
      onSuccess: (resized, { body }) => {
        toast.success(
          body.restart && !sandboxSize(resized).waiting
            ? `Restarted with ${sizeLabel(resized)}.`
            : appliedMessage(resized.applied)
        )
        onOpenChange(false)
      },
    }
  )
  const pending = resize.isPending

  function handleOpenChange(nextOpen: boolean) {
    // The request stays on screen until it succeeds or fails. A reopened dialog starts clean.
    if (!nextOpen && pending) return
    resize.reset()
    onOpenChange(nextOpen)
  }

  return (
    <Dialog open={open} onOpenChange={handleOpenChange}>
      <DialogContent showCloseButton={!pending}>
        {open && (
          <ResizeForm
            env={env}
            sandbox={sandbox}
            pending={pending}
            restarting={pending && resize.variables.body.restart === true}
            failure={resize.isError ? errorMessage(resize.error) : undefined}
            onSubmit={(cpus, memoryMiB, restart) =>
              resize.mutate({
                params: { path: { env, id: sandbox.id } },
                body: { cpus, memoryMiB, restart },
              })
            }
            onCancel={() => handleOpenChange(false)}
          />
        )}
      </DialogContent>
    </Dialog>
  )
}

/** msb's forecast for the values in the form, from a dry run a moment after they change. */
function usePlan(
  env: string,
  sandbox: Sandbox,
  cpus: number,
  memoryMiB: number,
  enabled: boolean
): Applied | undefined {
  const plan = $api.useMutation(
    "put",
    "/api/environments/{env}/sandboxes/{id}/resources"
  )
  const { mutate } = plan
  useEffect(() => {
    if (!enabled) return
    const timer = setTimeout(
      () =>
        mutate({
          params: { path: { env, id: sandbox.id } },
          body: { cpus, memoryMiB, dryRun: true },
        }),
      250
    )
    return () => clearTimeout(timer)
  }, [env, sandbox.id, cpus, memoryMiB, enabled, mutate])
  const asked = plan.variables?.body
  const current = asked?.cpus === cpus && asked.memoryMiB === memoryMiB
  return enabled && current ? plan.data?.applied : undefined
}

function ResizeForm({
  env,
  sandbox,
  pending,
  restarting,
  failure,
  onSubmit,
  onCancel,
}: {
  env: string
  sandbox: Sandbox
  pending: boolean
  restarting: boolean
  /** Why the last change failed. It stays visible until the dialog closes. */
  failure?: string
  onSubmit: (cpus: number, memoryMiB: number, restart: boolean) => void
  onCancel: () => void
}) {
  const size = sandboxSize(sandbox)
  const [cpus, setCPUs] = useState(String(sandbox.cpus))
  const [memory, setMemory] = useState(String(sandbox.memoryMiB))
  const cpuCount = Number(cpus)
  const memoryMiB = Number(memory)
  const cpusValid =
    Number.isInteger(cpuCount) && cpuCount >= 1 && cpuCount <= 64
  const memoryValid = Number.isInteger(memoryMiB) && memoryMiB >= 512
  const unchanged = cpuCount === sandbox.cpus && memoryMiB === sandbox.memoryMiB
  const valid = cpusValid && memoryValid
  const running = sandbox.status === "running"
  const plan = usePlan(env, sandbox, cpuCount, memoryMiB, valid && !unchanged)
  // Restarting helps when part of the change, or of an earlier one, waits for the next start.
  const offerRestart =
    running &&
    valid &&
    (unchanged
      ? size.waiting
      : plan?.cpus === "nextStart" ||
        plan?.memoryMiB === "nextStart" ||
        // What this change leaves as it is may still wait from before.
        (plan?.cpus === "unchanged" && size.now.cpus !== cpuCount) ||
        (plan?.memoryMiB === "unchanged" && size.now.memoryMiB !== memoryMiB))

  return (
    <form
      className="contents"
      onSubmit={(event) => {
        event.preventDefault()
        if (!valid || unchanged || pending) return
        onSubmit(cpuCount, memoryMiB, false)
      }}
    >
      <DialogHeader>
        <DialogTitle>Resources of {sandbox.name}</DialogTitle>
        <DialogDescription>
          {running
            ? "Running. A change applies now where the VM has room for it, the rest when it next starts."
            : sandbox.status === "suspended"
              ? "Suspended. A change applies when it next starts; resuming keeps what it has."
              : "A change applies when it next starts."}
        </DialogDescription>
      </DialogHeader>

      <Field data-invalid={!cpusValid || undefined}>
        <FieldLabel htmlFor="resize-cpus">CPUs</FieldLabel>
        <Input
          id="resize-cpus"
          type="number"
          inputMode="numeric"
          min={1}
          max={64}
          step={1}
          value={cpus}
          aria-invalid={!cpusValid}
          disabled={pending}
          onChange={(event) => setCPUs(event.target.value)}
        />
        {cpusValid ? (
          <FieldDescription>
            {forecast(
              sandbox,
              plan?.cpus,
              cpuCount !== sandbox.cpus,
              `${size.now.cpus}`,
              size.next.cpus !== size.now.cpus ? `${size.next.cpus}` : "",
              `${cpuCount} CPUs`
            )}
          </FieldDescription>
        ) : (
          <FieldError>Enter a whole number from 1 to 64.</FieldError>
        )}
      </Field>

      <Field data-invalid={!memoryValid || undefined}>
        <FieldLabel htmlFor="resize-memory">Memory (MiB)</FieldLabel>
        <Input
          id="resize-memory"
          type="number"
          inputMode="numeric"
          min={512}
          step={512}
          value={memory}
          aria-invalid={!memoryValid}
          disabled={pending}
          onChange={(event) => setMemory(event.target.value)}
        />
        {memoryValid ? (
          <FieldDescription>
            {forecast(
              sandbox,
              plan?.memoryMiB,
              memoryMiB !== sandbox.memoryMiB,
              formatMiB(size.now.memoryMiB),
              size.next.memoryMiB !== size.now.memoryMiB
                ? formatMiB(size.next.memoryMiB)
                : "",
              formatMiB(memoryMiB)
            )}
          </FieldDescription>
        ) : (
          <FieldError>Enter a whole number of at least 512.</FieldError>
        )}
      </Field>

      {offerRestart && (
        <p className="text-muted-foreground">
          <strong className="font-medium text-foreground">
            Save and restart
          </strong>{" "}
          applies everything now. Running programs and terminal sessions end;
          files stay.
        </p>
      )}

      {failure && (
        <p role="alert" className="text-destructive">
          Could not change the resources: {failure}
        </p>
      )}

      <DialogFooter>
        <Button
          type="button"
          variant="outline"
          disabled={pending}
          onClick={onCancel}
        >
          Close
        </Button>
        {offerRestart && (
          <Button
            type="button"
            variant="outline"
            disabled={pending}
            onClick={() => onSubmit(cpuCount, memoryMiB, true)}
          >
            {restarting && <Spinner />}
            Save and restart
          </Button>
        )}
        <Button type="submit" disabled={!valid || unchanged || pending}>
          {pending && !restarting && <Spinner />}
          Save
        </Button>
      </DialogFooter>
    </form>
  )
}

/** One resource's line: what the VM has, and how the value in the field would apply. */
function forecast(
  sandbox: Sandbox,
  applied: Applied["cpus"] | undefined,
  changed: boolean,
  now: string,
  afterRestart: string,
  asked: string
) {
  const has =
    sandbox.status === "running" || sandbox.status === "suspended"
      ? `Has ${now} now${afterRestart ? `, ${afterRestart} after a restart` : ""}.`
      : `Starts with ${now}.`
  if (!changed) return has
  switch (applied) {
    case "live":
      return `${has} ${asked} applies now.`
    case "nextStart":
      return sandbox.status === "running"
        ? `${has} ${asked} needs a restart: the VM booted without room for it.`
        : `${has} ${asked} applies when it next starts.`
    default:
      return has
  }
}

/** Says, in words, how the changed resources applied. */
function appliedMessage(applied: Applied) {
  const names = { cpus: "CPUs", memoryMiB: "memory" } as const
  const keys = (["cpus", "memoryMiB"] as const).filter(
    (key) => applied[key] !== "unchanged"
  )
  const live = keys
    .filter((key) => applied[key] === "live")
    .map((key) => names[key])
  const later = keys
    .filter((key) => applied[key] === "nextStart")
    .map((key) => names[key])
  const sentences: string[] = []
  if (live.length > 0) sentences.push(`The ${live.join(" and ")} changed live.`)
  if (later.length > 0)
    sentences.push(
      `The ${later.join(" and ")} ${later.length > 1 ? "apply" : "applies"} on the next start.`
    )
  return sentences.join(" ") || "No change."
}
