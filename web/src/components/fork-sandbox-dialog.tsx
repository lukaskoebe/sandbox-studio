import { useState } from "react"
import { useQueryClient } from "@tanstack/react-query"
import { useNavigate } from "@tanstack/react-router"
import { WarningIcon } from "@phosphor-icons/react"
import { Button } from "@/components/ui/button"
import { Checkbox } from "@/components/ui/checkbox"
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
import { TeammateField } from "@/components/teammate-field"
import { $api, errorMessage, type Sandbox } from "@/lib/api/client"

const namePattern = /^[a-z0-9]([a-z0-9-]{0,38}[a-z0-9])?$/

/** Copies a sandbox into a new one with the same template, then opens the copy. Opened from the options menu. */
export function ForkSandboxDialog({
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
  const queryClient = useQueryClient()
  const navigate = useNavigate()
  const fork = $api.useMutation(
    "post",
    "/api/environments/{env}/sandboxes/{id}/fork",
    {
      onSuccess: (forked) => {
        queryClient.invalidateQueries({
          queryKey: ["get", "/api/environments/{env}/sandboxes"],
        })
        onOpenChange(false)
        navigate({
          to: "/e/$env/sandboxes/$id",
          params: { env, id: forked.id },
        })
      },
    }
  )
  const pending = fork.isPending

  function handleOpenChange(nextOpen: boolean) {
    // The request stays on screen until it succeeds or fails.
    if (!nextOpen && pending) return
    if (nextOpen) fork.reset()
    onOpenChange(nextOpen)
  }

  return (
    <Dialog open={open} onOpenChange={handleOpenChange}>
      <DialogContent showCloseButton={!pending}>
        <ForkForm
          env={env}
          sandbox={sandbox}
          pending={pending}
          failure={fork.isError ? errorMessage(fork.error) : undefined}
          onSubmit={(name, options, teammateId) =>
            fork.mutate({
              params: { path: { env, id: sandbox.id } },
              body: { name, ...options, teammateId },
            })
          }
          onCancel={() => handleOpenChange(false)}
        />
      </DialogContent>
    </Dialog>
  )
}

function ForkForm({
  env,
  sandbox,
  pending,
  failure,
  onSubmit,
  onCancel,
}: {
  env: string
  sandbox: Sandbox
  pending: boolean
  /** Why the last copy failed. It stays visible until the dialog closes. */
  failure?: string
  onSubmit: (name: string, options: ForkChoices, teammateId: string) => void
  onCancel: () => void
}) {
  const [name, setName] = useState(() => forkName(sandbox.name))
  const [teammateId, setTeammateId] = useState(sandbox.teammateId ?? "")
  const [choices, setChoices] = useState<ForkChoices>(
    () => sandbox.forkDefaults ?? { docker: false, tools: true, script: false }
  )
  const invalid = name !== "" && !namePattern.test(name)
  // Docker's copy needs the computer stopped, so it's off for as long as it isn't.
  const dockerBlocked = sandbox.status !== "stopped"
  const docker = choices.docker && !dockerBlocked

  return (
    <form
      className="contents"
      onSubmit={(event) => {
        event.preventDefault()
        if (!name || invalid || pending) return
        onSubmit(name, { ...choices, docker }, teammateId)
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
          <dd>/workspace and /home/agent</dd>
          <dt className="text-muted-foreground">Not copied</dt>
          <dd>
            {!docker && "Docker images, containers and volumes; "}
            software installed elsewhere, such as apt packages
            {!choices.tools && (
              <>
                {" "}
                and tools from <code className="font-mono">mise use -g</code>
              </>
            )}
          </dd>
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

      <ForkChoiceFields
        choices={{ ...choices, docker }}
        dockerNote={
          dockerBlocked
            ? "Stop this computer to include its Docker images."
            : undefined
        }
        disabled={pending}
        // What's shown for Docker while it's blocked isn't the choice: that comes back with a stop.
        onChange={(next) =>
          setChoices({
            ...next,
            docker: dockerBlocked ? choices.docker : next.docker,
          })
        }
      />

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

      <TeammateField
        env={env}
        id="fork-sandbox-teammate"
        value={teammateId}
        onChange={setTeammateId}
        disabled={pending}
        description="The home is copied as it is, so its harness history and logins come along whoever owns the fork."
      />

      {failure && (
        <p role="alert" className="text-destructive">
          Could not fork the sandbox: {failure}
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
        <Button type="submit" disabled={!name || invalid || pending}>
          {pending && <Spinner />}
          Fork
        </Button>
      </DialogFooter>
    </form>
  )
}

/** What a fork does beyond copying the workspace and the home. */
export type ForkChoices = {
  docker: boolean
  tools: boolean
  script: boolean
}

/** The checkboxes for a fork's choices, in the fork dialog and a base's defaults. */
export function ForkChoiceFields({
  choices,
  dockerNote,
  disabled,
  onChange,
}: {
  choices: ForkChoices
  /** Why Docker can't be chosen right now. */
  dockerNote?: string
  disabled?: boolean
  onChange: (choices: ForkChoices) => void
}) {
  return (
    <div className="grid gap-2">
      <label className="flex items-start gap-2">
        <Checkbox
          checked={choices.docker}
          onCheckedChange={(docker) => onChange({ ...choices, docker })}
          disabled={disabled || !!dockerNote}
          className="mt-0.5"
        />
        <span>
          Copy Docker&apos;s images, containers and volumes
          <span className="block text-muted-foreground">
            {dockerNote ?? "Takes as long as they are big."}
          </span>
        </span>
      </label>
      <label className="flex items-start gap-2">
        <Checkbox
          checked={choices.tools}
          onCheckedChange={(tools) => onChange({ ...choices, tools })}
          disabled={disabled}
          className="mt-0.5"
        />
        <span>
          Copy the tools installed with{" "}
          <code className="font-mono">mise use</code>
        </span>
      </label>
      <label className="flex items-start gap-2">
        <Checkbox
          checked={choices.script}
          onCheckedChange={(script) => onChange({ ...choices, script })}
          disabled={disabled}
          className="mt-0.5"
        />
        <span>
          Run <code className="font-mono">~/.config/studio/on-fork.sh</code>
          <span className="block text-muted-foreground">
            In a terminal named <code className="font-mono">on-fork</code>, once
            the new computer is up.
          </span>
        </span>
      </label>
    </div>
  )
}

/** "<name>-fork", shortened so the result stays within the 40-character limit. */
function forkName(name: string) {
  return `${name.slice(0, 35).replace(/-+$/, "")}-fork`
}
