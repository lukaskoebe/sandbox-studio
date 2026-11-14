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
import { Checkbox } from "@/components/ui/checkbox"
import { Spinner } from "@/components/ui/spinner"
import { formatMiB } from "@/components/sandbox-details"
import { TemplateResources } from "@/components/template-resources"
import {
  $api,
  errorMessage,
  type Sandbox,
  type Template,
} from "@/lib/api/client"
import { canCopyDisks } from "@/components/status-badge"
import { templateStack } from "@/lib/lineage"
import { cn, formatAge } from "@/lib/utils"

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
          onSubmit={(templateId, keep) =>
            rebase.mutate({
              params: { path: { env, id: sandbox.id } },
              body: { templateId, ...keep },
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
  /**
   * The template to rebase onto, or "" for the current base image, and whether to keep the
   * tools in /opt/mise and Docker's data.
   */
  onSubmit: (
    templateId: string,
    keep: { keepTools: boolean; keepDocker: boolean }
  ) => void
  onCancel: () => void
}) {
  const [picked, setPicked] = useState<string>()
  const [keepTools, setKeepTools] = useState(true)
  const [keepDocker, setKeepDocker] = useState(false)
  const templates = $api.useQuery("get", "/api/environments/{env}/templates", {
    params: { path: { env } },
  })
  const updates = $api.useQuery("get", "/api/system/updates")
  const choices = (templates.data ?? []).filter(
    (template) => template.id !== sandbox.templateId
  )
  const rebuilt = rebuiltTemplate(sandbox, choices)
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
        onSubmit(choice, { keepTools, keepDocker })
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
        <TemplateResources
          resources={{
            ...selected.resources,
            // A rebase doesn't shrink a disk.
            workspaceMiB: Math.max(
              selected.resources.workspaceMiB,
              sandbox.workspaceMiB
            ),
            dockerMiB: Math.max(
              selected.resources.dockerMiB,
              sandbox.dockerMiB
            ),
            homeMiB:
              selected.resources.homeMiB &&
              Math.max(selected.resources.homeMiB, sandbox.homeMiB ?? 0),
          }}
        />
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
            {keepTools && (
              <li>
                tools from <span className="font-mono">mise use</span>
              </li>
            )}
            {keepDocker && <li>Docker images and volumes</li>}
          </ul>
        </div>
        <div className="grid content-start gap-1">
          <h3 className="font-mono text-caption font-semibold text-destructive">
            Lost
          </h3>
          <ul className="grid gap-0.5">
            {!keepDocker && <li>Docker images and volumes</li>}
            <li>
              apt packages
              {!keepTools && (
                <>
                  , tools from <span className="font-mono">mise use</span>
                </>
              )}
            </li>
            <li>running terminals</li>
          </ul>
        </div>
      </section>
      <KeepChoices
        keepTools={keepTools}
        keepDocker={keepDocker}
        onKeepTools={setKeepTools}
        onKeepDocker={setKeepDocker}
        disabled={pending}
      />
      <p className="text-muted-foreground">
        {sandbox.status === "running" || sandbox.status === "starting"
          ? "It restarts after the copy."
          : sandbox.status === "suspended"
            ? "It stops for the copy, which loses what it has in memory, and stays stopped."
            : "It starts for the copy and stays stopped."}{" "}
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

/** The sandbox's template rebuilt on the current base, the newest one, if there is one. */
function rebuiltTemplate(sandbox: Sandbox, templates: Template[]) {
  const stack = templateStack(sandbox.templateName)
  if (!stack) return undefined
  return templates
    .filter(
      (t) =>
        t.id !== sandbox.templateId &&
        !t.baseOutdated &&
        templateStack(t.name) === stack
    )
    .sort((a, b) => b.createdAt.localeCompare(a.createdAt))
    .at(0)
}

/** The two copies a rebase may add to the workspace and home, as checkboxes. */
function KeepChoices({
  keepTools,
  keepDocker,
  onKeepTools,
  onKeepDocker,
  disabled,
}: {
  keepTools: boolean
  keepDocker: boolean
  onKeepTools: (on: boolean) => void
  onKeepDocker: (on: boolean) => void
  disabled: boolean
}) {
  return (
    <>
      <label className="flex items-start gap-2">
        <Checkbox
          checked={keepTools}
          onCheckedChange={onKeepTools}
          disabled={disabled}
          className="mt-0.5"
        />
        <span>
          Keep the tools installed with{" "}
          <span className="font-mono">mise use</span>
          <span className="block text-muted-foreground">
            Copies the versions the new template lacks; for a tool both have,
            the template&apos;s version stays the default.
          </span>
        </span>
      </label>
      <label className="flex items-start gap-2">
        <Checkbox
          checked={keepDocker}
          onCheckedChange={onKeepDocker}
          disabled={disabled}
          className="mt-0.5"
        />
        <span>
          Keep Docker&apos;s images, containers and volumes
          <span className="block text-muted-foreground">
            Copying many images takes a while: up to 15 minutes for 10 GB.
          </span>
        </span>
      </label>
    </>
  )
}

/** Where Rebase all moves a sandbox, or why it leaves it. */
type Plan =
  | { sandbox: Sandbox; templateId: string; onto: string }
  | { sandbox: Sandbox; skip: string }

function planRebase(sandbox: Sandbox, templates: Template[]): Plan {
  if (!canCopyDisks(sandbox)) return { sandbox, skip: "busy" }
  if (!sandbox.templateId)
    return { sandbox, templateId: "", onto: "current base" }
  const rebuilt = rebuiltTemplate(sandbox, templates)
  if (rebuilt)
    return {
      sandbox,
      templateId: rebuilt.id,
      onto: `${rebuilt.name || rebuilt.id.slice(0, 8)} rebuilt`,
    }
  return {
    sandbox,
    skip: `rebuild ${sandbox.templateName || "its template"} first`,
  }
}

/**
 * Rebases every sandbox on the old base image: one without a template onto the current base,
 * one with a template onto that template rebuilt. A template not rebuilt yet leaves its
 * sandboxes, which would lose its tools on the bare base. Studio copies one at a time.
 */
export function RebaseAllDialog({
  env,
  sandboxes,
  open,
  onOpenChange,
}: {
  env: string
  sandboxes: Sandbox[]
  open: boolean
  onOpenChange: (open: boolean) => void
}) {
  const queryClient = useQueryClient()
  const [keepTools, setKeepTools] = useState(true)
  const [keepDocker, setKeepDocker] = useState(false)
  const [pending, setPending] = useState(false)
  const [failures, setFailures] = useState<string[]>([])
  const templates = $api.useQuery("get", "/api/environments/{env}/templates", {
    params: { path: { env } },
    enabled: open,
  })
  const plans = sandboxes.map((sb) => planRebase(sb, templates.data ?? []))
  const go = plans.filter((p) => "templateId" in p)
  const rebase = $api.useMutation(
    "post",
    "/api/environments/{env}/sandboxes/{id}/rebase"
  )

  async function start() {
    setPending(true)
    const failed: string[] = []
    let started = 0
    // Each request only checks and accepts its rebase; Studio runs them one after another.
    for (const p of go) {
      try {
        await rebase.mutateAsync({
          params: { path: { env, id: p.sandbox.id } },
          body: { templateId: p.templateId, keepTools, keepDocker },
        })
        started++
      } catch (err) {
        failed.push(`${p.sandbox.name}: ${errorMessage(err)}`)
      }
    }
    await queryClient.invalidateQueries({
      queryKey: ["get", "/api/environments/{env}/sandboxes"],
    })
    setPending(false)
    setFailures(failed)
    if (started > 0)
      toast.success(
        started === 1 ? "Rebase started" : `${started} rebases started`,
        { description: started > 1 ? "They run one at a time." : undefined }
      )
    if (failed.length === 0) onOpenChange(false)
  }

  return (
    <Dialog
      open={open}
      onOpenChange={(next) => {
        if (!next && pending) return
        if (next) setFailures([])
        onOpenChange(next)
      }}
    >
      <DialogContent showCloseButton={!pending}>
        <form
          className="contents"
          onSubmit={(event) => {
            event.preventDefault()
            if (!pending && go.length > 0) void start()
          }}
        >
          <DialogHeader>
            <DialogTitle>
              Rebase {sandboxes.length}{" "}
              {sandboxes.length === 1 ? "sandbox" : "sandboxes"}
            </DialogTitle>
            <DialogDescription>
              Each gets a new generation from the current base, with its work
              copied over. They go one at a time, the others waiting their turn.
            </DialogDescription>
          </DialogHeader>

          {templates.isPending ? (
            <div
              className="grid min-h-12 place-items-center"
              aria-label="Loading templates"
            >
              <Spinner />
            </div>
          ) : (
            <ul
              aria-label="Sandboxes to rebase"
              className="grid max-h-64 divide-y overflow-y-auto border"
            >
              {plans.map((p) => (
                <li
                  key={p.sandbox.id}
                  className="flex min-w-0 items-baseline gap-3 px-3 py-2"
                >
                  <span className="min-w-0 truncate font-mono">
                    {p.sandbox.name}
                  </span>
                  <span
                    className={cn(
                      "ml-auto max-w-[55%] shrink-0 truncate text-right",
                      "skip" in p ? "text-muted-foreground" : ""
                    )}
                  >
                    {"skip" in p ? `skipped: ${p.skip}` : `onto ${p.onto}`}
                  </span>
                </li>
              ))}
            </ul>
          )}
          {plans.some((p) => "skip" in p && p.skip !== "busy") && (
            <p className="text-muted-foreground">
              A sandbox on a template keeps the template&apos;s tools only on
              that template rebuilt: rebuild it in Settings → Templates, or
              rebase the sandbox from its computer&apos;s menu.
            </p>
          )}

          <KeepChoices
            keepTools={keepTools}
            keepDocker={keepDocker}
            onKeepTools={setKeepTools}
            onKeepDocker={setKeepDocker}
            disabled={pending}
          />
          <p className="text-muted-foreground">
            Each keeps /workspace, /home/agent, its chats and checkpoints, and
            loses apt packages and running terminals. A running sandbox restarts
            after its copy; a stopped one stays stopped. One that fails stays as
            it was.
          </p>

          {failures.length > 0 && !pending && (
            <div role="alert" className="grid gap-1 text-destructive">
              <p>
                {failures.length === 1
                  ? "One rebase didn't start:"
                  : `${failures.length} rebases didn't start:`}
              </p>
              <ul className="grid gap-0.5">
                {failures.map((f) => (
                  <li key={f} className="break-words">
                    {f}
                  </li>
                ))}
              </ul>
            </div>
          )}

          <DialogFooter>
            <Button
              type="button"
              variant="outline"
              disabled={pending}
              onClick={() => onOpenChange(false)}
            >
              Cancel
            </Button>
            <Button
              type="submit"
              disabled={pending || templates.isPending || go.length === 0}
            >
              {pending && <Spinner />}
              {go.length === sandboxes.length
                ? "Rebase all"
                : `Rebase ${go.length}`}
            </Button>
          </DialogFooter>
        </form>
      </DialogContent>
    </Dialog>
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
