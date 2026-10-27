import { useState } from "react"
import { useQueryClient } from "@tanstack/react-query"
import { ArrowClockwiseIcon } from "@phosphor-icons/react"
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
import { Spinner } from "@/components/ui/spinner"
import { formatMiB } from "@/components/sandbox-details"
import { TemplateResources } from "@/components/template-resources"
import {
  $api,
  errorMessage,
  type Sandbox,
  type Template,
} from "@/lib/api/client"
import { templateStack } from "@/lib/lineage"
import { formatAge } from "@/lib/utils"

/**
 * Moves a sandbox onto the current base image or another ready template. The workspace and
 * home are kept; the rest of the VM is rebuilt.
 */
export function RebaseSandboxDialog({
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
  const rebase = $api.useMutation(
    "post",
    "/api/environments/{env}/sandboxes/{id}/rebase",
    {
      onSuccess: async () => {
        await Promise.all([
          queryClient.invalidateQueries({
            queryKey: ["get", "/api/environments/{env}/sandboxes"],
          }),
          queryClient.invalidateQueries({
            queryKey: ["get", "/api/environments/{env}/sandboxes/{id}"],
          }),
        ])
        // The rebase runs in the background; the sandbox page follows its steps.
        onOpenChange(false)
        toast.success("Rebase started")
      },
    }
  )
  const pending = rebase.isPending

  function handleOpenChange(nextOpen: boolean) {
    // The request only checks and accepts the rebase; it stays on screen until answered.
    if (!nextOpen && pending) return
    if (nextOpen) rebase.reset()
    onOpenChange(nextOpen)
  }

  return (
    <Dialog open={open} onOpenChange={handleOpenChange}>
      <DialogContent showCloseButton={!pending}>
        <RebaseForm
          env={env}
          sandbox={sandbox}
          pending={pending}
          failure={rebase.isError ? errorMessage(rebase.error) : undefined}
          onSubmit={(templateId) =>
            rebase.mutate({
              params: { path: { env, id: sandbox.id } },
              body: { templateId },
            })
          }
          onCancel={() => handleOpenChange(false)}
        />
      </DialogContent>
    </Dialog>
  )
}

function RebaseForm({
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
  /** Why the last rebase failed. It stays visible until the dialog closes. */
  failure?: string
  /** The template to rebase onto, or "" for the current base image. */
  onSubmit: (templateId: string) => void
  onCancel: () => void
}) {
  const [picked, setPicked] = useState<string>()
  const templates = $api.useQuery("get", "/api/environments/{env}/templates", {
    params: { path: { env } },
  })
  const updates = $api.useQuery("get", "/api/system/updates")
  const choices = (templates.data ?? []).filter(
    (template) => template.id !== sandbox.templateId
  )
  const stack = templateStack(sandbox.templateName)
  // The sandbox's template rebuilt on the current base, the newest first.
  const rebuilt = choices
    .filter((t) => !t.baseOutdated && stack && templateStack(t.name) === stack)
    .sort((a, b) => b.createdAt.localeCompare(a.createdAt))
    .at(0)
  // A sandbox without a template goes onto the current base; one with a template onto that
  // template rebuilt, when there is one.
  const fallback = sandbox.templateId ? rebuilt?.id : ""
  const choice = picked ?? fallback
  const selected = choices.find((template) => template.id === choice)
  const ready = choice === "" || !!selected

  return (
    <form
      className="contents"
      onSubmit={(event) => {
        event.preventDefault()
        if (!ready || pending || choice === undefined) return
        onSubmit(choice)
      }}
    >
      <DialogHeader>
        <DialogTitle>Rebase {sandbox.name}</DialogTitle>
        <DialogDescription>
          A new generation from a newer image, with your work copied over.
        </DialogDescription>
      </DialogHeader>

      <fieldset className="grid gap-2" disabled={pending}>
        <legend className="mb-2 font-medium">Onto</legend>
        <div className="grid divide-y border">
          <Choice
            value=""
            checked={choice === ""}
            onSelect={() => setPicked("")}
            title="The current base image"
            detail={[
              updates.data?.current,
              "what new sandboxes get",
              sandbox.templateId && "without the template's tools",
            ]
              .filter(Boolean)
              .join(" · ")}
          />
          {templates.isPending ? (
            <div
              className="grid min-h-12 place-items-center"
              aria-label="Loading templates"
            >
              <Spinner />
            </div>
          ) : templates.isError && !templates.data ? (
            <div className="flex items-center gap-3 p-3" role="alert">
              <p className="min-w-0 flex-1 break-words text-muted-foreground">
                Templates unavailable: {errorMessage(templates.error)}
              </p>
              <Button
                type="button"
                variant="outline"
                size="sm"
                onClick={() => void templates.refetch()}
              >
                <ArrowClockwiseIcon />
                Retry
              </Button>
            </div>
          ) : (
            choices.map((template) => (
              <Choice
                key={template.id}
                value={template.id}
                checked={choice === template.id}
                onSelect={() => setPicked(template.id)}
                title={`Template ${template.name || template.id.slice(0, 8)}`}
                detail={templateDetail(template, template === rebuilt)}
              />
            ))
          )}
        </div>
        {sandbox.templateId && !rebuilt && sandbox.baseOutdated && (
          <p className="text-muted-foreground">
            To keep the template&apos;s tools on the current base, rebuild{" "}
            <span className="font-mono">{sandbox.templateName}</span> in
            Settings → Templates first.
          </p>
        )}
      </fieldset>

      {selected ? (
        <TemplateResources resources={selected.resources} />
      ) : (
        choice === "" && (
          <p className="text-muted-foreground">
            Keeps its resources: {sandbox.cpus} CPU ·{" "}
            {formatMiB(sandbox.memoryMiB)}.
          </p>
        )
      )}

      <section
        className="grid gap-x-6 gap-y-3 sm:grid-cols-2"
        aria-label="What the rebase changes"
      >
        <div className="grid content-start gap-1">
          <h3 className="font-mono text-caption font-semibold text-(--st-running)">
            Kept
          </h3>
          <ul className="grid gap-0.5">
            <li className="font-mono">/workspace</li>
            <li>
              <span className="font-mono">/home/agent</span>, logins, harness
              sessions
            </li>
            <li>chats and checkpoints</li>
          </ul>
        </div>
        <div className="grid content-start gap-1">
          <h3 className="font-mono text-caption font-semibold text-destructive">
            Lost
          </h3>
          <ul className="grid gap-0.5">
            <li>Docker images and volumes</li>
            <li>
              apt packages, tools from{" "}
              <span className="font-mono">mise use -g</span>
            </li>
            <li>running terminals</li>
          </ul>
        </div>
      </section>
      <p className="text-muted-foreground">
        {sandbox.status === "running"
          ? "It restarts after the copy."
          : "It stays stopped."}{" "}
        If the rebase fails, it stays as it is.
      </p>

      {failure && !pending && (
        <p role="alert" className="text-destructive">
          Could not rebase the sandbox: {failure}
        </p>
      )}

      <DialogFooter>
        <Button
          type="button"
          variant="outline"
          disabled={pending}
          onClick={onCancel}
        >
          Cancel
        </Button>
        <Button type="submit" disabled={!ready || pending}>
          {pending && <Spinner />}
          Rebase
        </Button>
      </DialogFooter>
    </form>
  )
}

/** One target as a radio row: its name, then what it is. */
function Choice({
  value,
  checked,
  onSelect,
  title,
  detail,
}: {
  value: string
  checked: boolean
  onSelect: () => void
  title: string
  detail: string
}) {
  const id = `rebase-onto-${value || "base"}`
  return (
    <label className="flex cursor-pointer items-start gap-2 px-3 py-2.5 transition-colors hover:bg-muted/40 has-checked:bg-primary/5 has-checked:shadow-[inset_2px_0_0_var(--color-primary)] has-focus-visible:outline-2 has-focus-visible:-outline-offset-2 has-focus-visible:outline-ring has-disabled:cursor-default">
      <input
        type="radio"
        name="rebase-onto"
        value={value}
        checked={checked}
        onChange={onSelect}
        aria-labelledby={id}
        aria-describedby={`${id}-detail`}
        className="mt-[0.1875rem] size-3.5 shrink-0 appearance-none rounded-full border border-input bg-card outline-none checked:border-primary checked:bg-primary checked:shadow-[inset_0_0_0_3px_var(--color-card)] disabled:opacity-50"
      />
      <span className="grid min-w-0 gap-0.5">
        <span id={id} className="truncate font-medium">
          {title}
        </span>
        <span id={`${id}-detail`} className="text-muted-foreground">
          {detail}
        </span>
      </span>
    </label>
  )
}

/** When a template was built, and on which base. */
function templateDetail(template: Template, rebuilt: boolean) {
  const age = formatAge(template.createdAt)
  if (rebuilt) return `rebuilt on the current base ${age}`
  return template.baseOutdated
    ? `created ${age} · on an older base`
    : `created ${age}`
}
