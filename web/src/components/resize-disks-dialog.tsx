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
import { formatMiB } from "@/components/sandbox-details"
import { $api, errorMessage, type Sandbox } from "@/lib/api/client"
import type { components } from "@/lib/api/schema.gen"
import { formatBytes } from "@/lib/utils"

type DiskName = components["schemas"]["Disk"]["name"]

/**
 * Gives a sandbox's workspace, Docker and home disks new sizes. A disk can't change its size
 * in place, so Studio moves the sandbox to a new generation, as a rebase onto its own
 * template does, and copies the three disks over.
 */
export function ResizeDisksDialog({
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
    "/api/environments/{env}/sandboxes/{id}/disks",
    {
      onSuccess: () => {
        invalidate()
        // The resize runs in the background; the sandbox page follows its steps.
        onOpenChange(false)
        toast.success("Disk resize started")
      },
    }
  )
  const pending = resize.isPending

  function handleOpenChange(nextOpen: boolean) {
    // The request only checks and accepts the resize; it stays on screen until answered.
    if (!nextOpen && pending) return
    if (nextOpen) resize.reset()
    onOpenChange(nextOpen)
  }

  return (
    <Dialog open={open} onOpenChange={handleOpenChange}>
      <DialogContent showCloseButton={!pending}>
        {open && (
          <DisksForm
            sandbox={sandbox}
            pending={pending}
            failure={resize.isError ? errorMessage(resize.error) : undefined}
            onSubmit={(body) =>
              resize.mutate({
                params: { path: { env, id: sandbox.id } },
                body,
              })
            }
            onCancel={() => handleOpenChange(false)}
          />
        )}
      </DialogContent>
    </Dialog>
  )
}

type Sizes = components["schemas"]["DiskSizes"]

function DisksForm({
  sandbox,
  pending,
  failure,
  onSubmit,
  onCancel,
}: {
  sandbox: Sandbox
  pending: boolean
  /** Why the last request failed. It stays visible until the dialog closes. */
  failure?: string
  onSubmit: (sizes: Sizes) => void
  onCancel: () => void
}) {
  const disks = [
    {
      name: "workspace",
      label: "Workspace (/workspace)",
      key: "workspaceMiB",
      mib: sandbox.workspaceMiB,
    },
    {
      name: "docker",
      label: "Docker images, containers and volumes",
      key: "dockerMiB",
      mib: sandbox.dockerMiB,
    },
    // A sandbox of a template built before the home disk has its home on the root disk.
    ...(sandbox.homeMiB
      ? [
          {
            name: "home",
            label: "Home (/home/agent)",
            key: "homeMiB",
            mib: sandbox.homeMiB,
          } as const,
        ]
      : []),
  ] as const satisfies {
    name: DiskName
    label: string
    key: keyof Sizes
    mib: number
  }[]
  const [values, setValues] = useState<Record<string, string>>(() =>
    Object.fromEntries(disks.map((d) => [d.key, gib(d.mib)]))
  )
  const asked = disks.map((d) => {
    const value = Number(values[d.key])
    const valid = Number.isFinite(value) && value >= 1
    // A size the form shows rounded stays as it is until it is edited.
    const mib = values[d.key] === gib(d.mib) ? d.mib : Math.round(value * 1024)
    return { ...d, valid, mib: valid ? mib : 0, was: d.mib }
  })
  const valid = asked.every((d) => d.valid)
  const changed = asked.filter((d) => d.valid && d.mib !== d.was)
  const running = sandbox.status === "running"

  return (
    <form
      className="contents"
      onSubmit={(event) => {
        event.preventDefault()
        if (!valid || changed.length === 0 || pending) return
        onSubmit(Object.fromEntries(changed.map((d) => [d.key, d.mib])))
      }}
    >
      <DialogHeader>
        <DialogTitle>Disks of {sandbox.name}</DialogTitle>
        <DialogDescription>
          A new generation with these sizes, with the workspace, the home and
          Docker&apos;s data copied over.{" "}
          {running && "The computer stops for the copy and starts again. "}
          What was installed elsewhere, such as apt packages, is lost.
        </DialogDescription>
      </DialogHeader>

      {asked.map((d) => {
        const low = sandbox.lowDisks?.find((l) => l.name === d.name)
        return (
          <Field key={d.key} data-invalid={!d.valid || undefined}>
            <FieldLabel htmlFor={`disk-${d.name}`}>{d.label} (GiB)</FieldLabel>
            <Input
              id={`disk-${d.name}`}
              type="number"
              inputMode="decimal"
              min={1}
              step={1}
              value={values[d.key]}
              aria-invalid={!d.valid}
              disabled={pending}
              onChange={(event) =>
                setValues((v) => ({ ...v, [d.key]: event.target.value }))
              }
            />
            {d.valid ? (
              <FieldDescription>
                {low
                  ? `${formatBytes(low.free)} of ${formatBytes(low.total)} free now.`
                  : `${formatMiB(d.was)} now.`}
                {d.mib < d.was &&
                  " A smaller disk works as long as what it holds fits."}
              </FieldDescription>
            ) : (
              <FieldError>Enter a size of at least 1 GiB.</FieldError>
            )}
          </Field>
        )
      })}

      <p className="text-muted-foreground">
        Tmux sessions end. While the copy runs, the data takes twice its space
        on this machine; a big Docker disk takes a while to copy.
      </p>

      {failure && (
        <p role="alert" className="text-destructive">
          Could not resize the disks: {failure}
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
        <Button
          type="submit"
          disabled={!valid || changed.length === 0 || pending}
        >
          {pending && <Spinner />}
          Resize
        </Button>
      </DialogFooter>
    </form>
  )
}

/** A size in MiB as GiB for the form: whole where it is, else to a tenth. */
function gib(mib: number) {
  const value = mib / 1024
  return String(Number.isInteger(value) ? value : Math.round(value * 10) / 10)
}
