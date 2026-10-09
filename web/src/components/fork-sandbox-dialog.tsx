import { useState } from "react"
import { useQueryClient } from "@tanstack/react-query"
import { useNavigate } from "@tanstack/react-router"
import { GitForkIcon, WarningIcon } from "@phosphor-icons/react"
import { Button } from "@/components/ui/button"
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
  DialogTrigger,
} from "@/components/ui/dialog"
import {
  Field,
  FieldDescription,
  FieldError,
  FieldLabel,
} from "@/components/ui/field"
import { Input } from "@/components/ui/input"
import { Spinner } from "@/components/ui/spinner"
import { $api, errorMessage, type Sandbox } from "@/lib/api/client"

const namePattern = /^[a-z0-9]([a-z0-9-]{0,38}[a-z0-9])?$/

/** Copies a sandbox into a new one with the same template, then opens the copy. */
export function ForkSandboxDialog({
  env,
  sandbox,
}: {
  env: string
  sandbox: Sandbox
}) {
  const [open, setOpen] = useState(false)
  const queryClient = useQueryClient()
  const navigate = useNavigate()
  const fork = $api.useMutation(
    "post",
    "/api/environments/{env}/sandboxes/{id}/fork",
    {
      onSuccess: async (forked) => {
        await queryClient.invalidateQueries({
          queryKey: ["get", "/api/environments/{env}/sandboxes"],
        })
        setOpen(false)
        navigate({
          to: "/e/$env/sandboxes/$id",
          params: { env, id: forked.id },
        })
      },
    }
  )
  const pending = fork.isPending
  const available = sandbox.status === "running" || sandbox.status === "stopped"

  function handleOpenChange(nextOpen: boolean) {
    // A copy in progress stays on screen until it succeeds or fails.
    if (!nextOpen && pending) return
    if (nextOpen) fork.reset()
    setOpen(nextOpen)
  }

  return (
    <Dialog open={open} onOpenChange={handleOpenChange}>
      <DialogTrigger
        render={
          <Button
            variant="ghost"
            size="sm"
            disabled={!available || pending}
            title={
              available
                ? "Copy this sandbox into a new one"
                : "Only a running or stopped sandbox can be forked"
            }
          />
        }
      >
        <GitForkIcon />
        Fork
      </DialogTrigger>
      <DialogContent
        className="max-h-[calc(100dvh-2rem)] overflow-y-auto"
        showCloseButton={!pending}
      >
        <ForkForm
          sandbox={sandbox}
          pending={pending}
          failure={fork.isError ? errorMessage(fork.error) : undefined}
          onSubmit={(name) =>
            fork.mutate({
              params: { path: { env, id: sandbox.id } },
              body: { name },
            })
          }
          onCancel={() => handleOpenChange(false)}
        />
      </DialogContent>
    </Dialog>
  )
}

function ForkForm({
  sandbox,
  pending,
  failure,
  onSubmit,
  onCancel,
}: {
  sandbox: Sandbox
  pending: boolean
  /** Why the last copy failed. It stays visible until the dialog closes. */
  failure?: string
  onSubmit: (name: string) => void
  onCancel: () => void
}) {
  const [name, setName] = useState(() => forkName(sandbox.name))
  const invalid = name !== "" && !namePattern.test(name)

  return (
    <form
      className="contents"
      onSubmit={(event) => {
        event.preventDefault()
        if (!name || invalid || pending) return
        onSubmit(name)
      }}
    >
      <DialogHeader>
        <DialogTitle>Fork {sandbox.name}</DialogTitle>
        <DialogDescription>
          Copies this sandbox into a new one with the same template and
          resources, then starts it.
        </DialogDescription>
      </DialogHeader>

      <section className="rounded-md border p-3" aria-label="What is copied">
        <dl className="grid grid-cols-[7rem_minmax(0,1fr)] gap-x-4 gap-y-2">
          <dt className="text-muted-foreground">Copied</dt>
          <dd>/workspace</dd>
          <dt className="text-muted-foreground">Not copied</dt>
          <dd>Docker images, containers and volumes</dd>
          <dt className="text-muted-foreground">Identity</dt>
          <dd>The fork gets its own identity and its own network approvals.</dd>
        </dl>
        {sandbox.status === "running" && (
          <p className="mt-3 flex gap-2 text-amber-700 dark:text-amber-400">
            <WarningIcon className="mt-0.5 size-3 shrink-0" />
            This sandbox is running, so it is copied live. Files written during
            the copy may be inconsistent.
          </p>
        )}
      </section>

      <Field data-invalid={invalid || undefined}>
        <FieldLabel htmlFor="fork-sandbox-name">Name</FieldLabel>
        <Input
          id="fork-sandbox-name"
          autoFocus
          autoComplete="off"
          placeholder="my-project-fork"
          value={name}
          aria-invalid={invalid}
          disabled={pending}
          onChange={(event) => setName(event.target.value.toLowerCase())}
        />
        {invalid ? (
          <FieldError>
            Lowercase letters, digits and dashes; up to 40 characters.
          </FieldError>
        ) : (
          <FieldDescription>
            Also the new sandbox&apos;s hostname.
          </FieldDescription>
        )}
      </Field>

      {pending ? (
        <p
          role="status"
          className="flex items-center gap-2 text-muted-foreground"
        >
          <Spinner />
          Copying workspace… This can take several minutes.
        </p>
      ) : (
        failure && (
          <p role="alert" className="text-destructive">
            Could not fork the sandbox: {failure}
          </p>
        )
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
        <Button type="submit" disabled={!name || invalid || pending}>
          {pending && <Spinner />}
          Fork
        </Button>
      </DialogFooter>
    </form>
  )
}

/** "<name>-fork", shortened so the result stays within the 40-character limit. */
function forkName(name: string) {
  return `${name.slice(0, 35).replace(/-+$/, "")}-fork`
}
