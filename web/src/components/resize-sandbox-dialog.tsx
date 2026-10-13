import { useState } from "react"
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
import { $api, errorMessage, type Sandbox } from "@/lib/api/client"
import type { components } from "@/lib/api/schema.gen"

type Applied = components["schemas"]["ResizeApplied"]

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
      onSuccess: (resized) => {
        toast.success(appliedMessage(resized.applied))
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
        <ResizeForm
          sandbox={sandbox}
          pending={pending}
          failure={resize.isError ? errorMessage(resize.error) : undefined}
          onSubmit={(cpus, memoryMiB) =>
            resize.mutate({
              params: { path: { env, id: sandbox.id } },
              body: { cpus, memoryMiB },
            })
          }
          onCancel={() => handleOpenChange(false)}
        />
      </DialogContent>
    </Dialog>
  )
}

function ResizeForm({
  sandbox,
  pending,
  failure,
  onSubmit,
  onCancel,
}: {
  sandbox: Sandbox
  pending: boolean
  /** Why the last change failed. It stays visible until the dialog closes. */
  failure?: string
  onSubmit: (cpus: number, memoryMiB: number) => void
  onCancel: () => void
}) {
  const [cpus, setCPUs] = useState(String(sandbox.cpus))
  const [memory, setMemory] = useState(String(sandbox.memoryMiB))
  const cpuCount = Number(cpus)
  const memoryMiB = Number(memory)
  const cpusValid =
    Number.isInteger(cpuCount) && cpuCount >= 1 && cpuCount <= 64
  const memoryValid = Number.isInteger(memoryMiB) && memoryMiB >= 512
  const unchanged = cpuCount === sandbox.cpus && memoryMiB === sandbox.memoryMiB
  const valid = cpusValid && memoryValid

  return (
    <form
      className="contents"
      onSubmit={(event) => {
        event.preventDefault()
        if (!valid || unchanged || pending) return
        onSubmit(cpuCount, memoryMiB)
      }}
    >
      <DialogHeader>
        <DialogTitle>Resources of {sandbox.name}</DialogTitle>
        <DialogDescription>
          A running sandbox takes a change to its CPUs or memory live where its
          VM has room for it. Anything else applies the next time the sandbox
          starts.
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
            Now {sandbox.cpus}. Between 1 and 64.
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
          step={1}
          value={memory}
          aria-invalid={!memoryValid}
          disabled={pending}
          onChange={(event) => setMemory(event.target.value)}
        />
        {memoryValid ? (
          <FieldDescription>
            Now {sandbox.memoryMiB} MiB. A size above the ceiling of{" "}
            {sandbox.maxMemoryMiB} MiB raises it.
          </FieldDescription>
        ) : (
          <FieldError>Enter a whole number of at least 512.</FieldError>
        )}
      </Field>

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
        <Button type="submit" disabled={!valid || unchanged || pending}>
          {pending && <Spinner />}
          Apply
        </Button>
      </DialogFooter>
    </form>
  )
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
    sentences.push(`The ${later.join(" and ")} applies on the next start.`)
  return sentences.join(" ") || "No change."
}
