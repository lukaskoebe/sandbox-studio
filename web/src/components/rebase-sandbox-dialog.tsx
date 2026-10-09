import { useState } from "react"
import { useQueryClient } from "@tanstack/react-query"
import {
  ArrowClockwiseIcon,
  ArrowsClockwiseIcon,
  WarningIcon,
} from "@phosphor-icons/react"
import { toast } from "sonner"
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
import { Field, FieldLabel } from "@/components/ui/field"
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select"
import { Spinner } from "@/components/ui/spinner"
import { TemplateResources } from "@/components/template-resources"
import {
  $api,
  errorMessage,
  type Sandbox,
  type Template,
} from "@/lib/api/client"
import { formatAge } from "@/lib/utils"

/** Moves a sandbox onto another ready template. The workspace is kept; the rest of the VM is rebuilt. */
export function RebaseSandboxDialog({
  env,
  sandbox,
}: {
  env: string
  sandbox: Sandbox
}) {
  const [open, setOpen] = useState(false)
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
        setOpen(false)
        toast.success("Sandbox rebased")
      },
    }
  )
  const pending = rebase.isPending
  const available = sandbox.status === "running" || sandbox.status === "stopped"

  function handleOpenChange(nextOpen: boolean) {
    // A rebase in progress stays on screen until it succeeds or fails.
    if (!nextOpen && pending) return
    if (nextOpen) rebase.reset()
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
                ? "Move this sandbox onto another template"
                : "Only a running or stopped sandbox can be rebased"
            }
          />
        }
      >
        <ArrowsClockwiseIcon />
        Rebase
      </DialogTrigger>
      <DialogContent
        className="max-h-[calc(100dvh-2rem)] overflow-y-auto"
        showCloseButton={!pending}
      >
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
  onSubmit: (templateId: string) => void
  onCancel: () => void
}) {
  const [templateId, setTemplateId] = useState<string | null>(null)
  const templates = $api.useQuery("get", "/api/environments/{env}/templates", {
    params: { path: { env } },
  })
  const choices = (templates.data ?? []).filter(
    (template) => template.id !== sandbox.templateId
  )
  const selected = choices.find((template) => template.id === templateId)

  return (
    <form
      className="contents"
      onSubmit={(event) => {
        event.preventDefault()
        if (!selected || pending) return
        onSubmit(selected.id)
      }}
    >
      <DialogHeader>
        <DialogTitle>Rebase {sandbox.name}</DialogTitle>
        <DialogDescription>
          Moves this sandbox onto another ready template in this environment.
          The VM is rebuilt from that template.
        </DialogDescription>
      </DialogHeader>

      <section
        className="grid gap-2 rounded-md border border-amber-500/30 bg-amber-500/5 p-3 text-amber-700 dark:text-amber-400"
        aria-label="What the rebase changes"
      >
        <p className="flex gap-2 font-medium">
          <WarningIcon className="mt-0.5 size-3 shrink-0" />
          /workspace is kept. Everything else in the VM is replaced.
        </p>
        <ul className="ml-5 list-disc space-y-1">
          <li>Docker images, containers and volumes are lost.</li>
          <li>Running processes and tmux or terminal sessions end.</li>
          <li>
            {sandbox.status === "running"
              ? "The sandbox restarts after the copy."
              : "The sandbox stays stopped."}
          </li>
          <li>
            If the rebase fails, the sandbox stays on its current template.
          </li>
        </ul>
      </section>

      <Field>
        <FieldLabel htmlFor="rebase-template">Template</FieldLabel>
        {templates.isPending ? (
          <div
            className="grid min-h-16 place-items-center"
            aria-label="Loading templates"
          >
            <Spinner />
          </div>
        ) : templates.isError && !templates.data ? (
          <div className="space-y-3 rounded-md border p-3" role="alert">
            <div className="space-y-1">
              <h3 className="font-medium">Templates unavailable</h3>
              <p className="break-words text-muted-foreground">
                {errorMessage(templates.error)}
              </p>
            </div>
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
        ) : choices.length === 0 ? (
          <p className="text-muted-foreground">
            No other ready template in this environment. Build one on the Builds
            page first.
          </p>
        ) : (
          <Select
            value={templateId}
            onValueChange={(value) => setTemplateId(value)}
            items={Object.fromEntries(
              choices.map((template) => [template.id, templateLabel(template)])
            )}
            disabled={pending}
          >
            <SelectTrigger id="rebase-template" className="w-full">
              <SelectValue placeholder="Choose a template" />
            </SelectTrigger>
            <SelectContent>
              {choices.map((template) => (
                <SelectItem key={template.id} value={template.id}>
                  {templateLabel(template)}
                </SelectItem>
              ))}
            </SelectContent>
          </Select>
        )}
      </Field>

      {selected && <TemplateResources resources={selected.resources} />}

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
            Could not rebase the sandbox: {failure}
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
        <Button
          type="submit"
          variant="destructive"
          disabled={!selected || pending}
        >
          {pending && <Spinner />}
          Rebase
        </Button>
      </DialogFooter>
    </form>
  )
}

/** Templates have no names, so the short ID and age tell them apart. */
function templateLabel(template: Template) {
  return `Template ${template.id.slice(0, 8)}, created ${formatAge(template.createdAt)}`
}
