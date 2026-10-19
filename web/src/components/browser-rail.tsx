import { useMemo, useState, type FormEvent, type ReactNode } from "react"
import { useQueryClient } from "@tanstack/react-query"
import { toast } from "sonner"
import {
  CaretDownIcon,
  CaretRightIcon,
  DetectiveIcon,
  EyeSlashIcon,
  HandIcon,
  XIcon,
} from "@phosphor-icons/react"
import {
  PolicyPicker,
  replayShot,
  screenshotURL,
  stepWords,
  type BrowserAction,
  type BrowserReplay,
} from "@/components/browser-window"
import { Button } from "@/components/ui/button"
import { Input } from "@/components/ui/input"
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select"
import { Spinner } from "@/components/ui/spinner"
import { $api, errorMessage } from "@/lib/api/client"
import type { components } from "@/lib/api/schema.gen"
import {
  elementWords,
  hostOf,
  originOf,
  siteRules,
  sitePolicy,
  useBrowserPatterns,
  useSetSitePolicy,
} from "@/lib/browser-sites"
import { cn, formatAge } from "@/lib/utils"

type BrowserPattern = components["schemas"]["BrowserPattern"]

const statusPath = "/api/environments/{env}/teammates/{teammate}/browser"

export type RailTab = "activity" | "sites"

/** One browser session of the action log: its steps, oldest first. */
export type BrowserRun = {
  id: string
  steps: BrowserAction[]
  ephemeral: boolean
  sandboxId?: string
}

/** The teammate's action log, by session, newest session first. */
export function useBrowserRuns(env: string, teammate: string) {
  const actions = $api.useQuery(
    "get",
    `${statusPath}/actions`,
    { params: { path: { env, teammate }, query: { limit: 500 } } },
    { refetchInterval: 10000 }
  )
  const runs = useMemo(() => {
    const out = new Map<string, BrowserRun>()
    for (const a of actions.data ?? []) {
      const run = out.get(a.sessionId) ?? {
        id: a.sessionId,
        steps: [],
        ephemeral: !!a.ephemeral,
      }
      run.steps.unshift(a) // the log is newest first
      run.sandboxId ||= a.sandboxId
      out.set(a.sessionId, run)
    }
    return [...out.values()]
  }, [actions.data])
  return { runs, isPending: actions.isPending, actions: actions.data }
}

/** The sandbox names of an environment, for the log's "from api-refactor". */
function useSandboxNames(env: string) {
  const sandboxes = $api.useQuery("get", "/api/environments/{env}/sandboxes", {
    params: { path: { env } },
  })
  return (id?: string) =>
    (id && sandboxes.data?.find((s) => s.id === id)?.name) || id
}

/** Beside the full browser: what happened, step by step, and what the teammate may do where. */
export function BrowserRail({
  env,
  teammate,
  name,
  tab,
  onTab,
  replay,
  onReplay,
}: {
  env: string
  teammate: string
  name: string
  tab: RailTab
  onTab: (t: RailTab) => void
  replay: BrowserReplay | null
  onReplay: (r: BrowserReplay | null) => void
}) {
  return (
    <aside className="flex min-h-0 w-80 shrink-0 flex-col border-l max-xl:h-96 max-xl:w-auto max-xl:border-t max-xl:border-l-0">
      <div
        role="tablist"
        aria-label="Browser activity and sites"
        className="flex shrink-0 gap-4 border-b px-4 text-xs"
      >
        {(
          [
            ["activity", "Activity"],
            ["sites", "Sites"],
          ] as const
        ).map(([id, label]) => (
          <button
            key={id}
            type="button"
            role="tab"
            aria-selected={tab === id}
            onClick={() => onTab(id)}
            className={cn(
              "-mb-px h-10 border-b-2",
              tab === id
                ? "border-foreground font-medium"
                : "border-transparent text-muted-foreground hover:text-foreground"
            )}
          >
            {label}
          </button>
        ))}
      </div>
      <div className="min-h-0 flex-1 overflow-y-auto">
        {tab === "activity" ? (
          <Activity
            env={env}
            teammate={teammate}
            name={name}
            replay={replay}
            onReplay={onReplay}
          />
        ) : (
          <Sites env={env} teammate={teammate} name={name} />
        )}
      </div>
    </aside>
  )
}

function Activity({
  env,
  teammate,
  name,
  replay,
  onReplay,
}: {
  env: string
  teammate: string
  name: string
  replay: BrowserReplay | null
  onReplay: (r: BrowserReplay | null) => void
}) {
  const { runs, isPending } = useBrowserRuns(env, teammate)
  const sandboxName = useSandboxNames(env)
  // The newest session is open; the others open on click.
  const [open, setOpen] = useState<Record<string, boolean>>({})
  if (isPending)
    return (
      <div className="flex justify-center p-6">
        <Spinner />
      </div>
    )
  if (runs.length === 0)
    return (
      <p className="p-4 text-xs text-muted-foreground">
        Nothing yet. Every step {name} or you take in the browser shows here,
        and replays from its screenshot.
      </p>
    )
  return (
    <div className="grid gap-4 p-4 text-xs">
      {runs.map((run, i) => {
        const expanded = open[run.id] ?? i === 0
        const first = run.steps[0]
        const from = sandboxName(run.sandboxId)
        return (
          <section key={run.id} className="grid gap-2">
            <button
              type="button"
              aria-expanded={expanded}
              onClick={() => setOpen({ ...open, [run.id]: !expanded })}
              className="flex items-start gap-1.5 text-left"
            >
              {expanded ? (
                <CaretDownIcon className="mt-0.5 size-3 shrink-0" />
              ) : (
                <CaretRightIcon className="mt-0.5 size-3 shrink-0" />
              )}
              <span className="grid min-w-0 gap-0.5">
                <span className="font-mono text-[0.625rem] tracking-wider text-muted-foreground uppercase">
                  {formatAge(first.at)}
                </span>
                <span className="flex items-center gap-1 truncate">
                  {run.ephemeral && (
                    <DetectiveIcon
                      className="size-3 shrink-0"
                      aria-label="Throwaway session"
                    />
                  )}
                  <span className="min-w-0 truncate">
                    {hostOf([...run.steps].reverse().find((a) => a.url)?.url) ||
                      "Browser session"}
                  </span>
                </span>
                <span className="text-muted-foreground">
                  {run.steps.length} {run.steps.length === 1 ? "step" : "steps"}
                  {from && (
                    <>
                      {" "}
                      · from <span className="font-mono">{from}</span>
                    </>
                  )}
                </span>
              </span>
            </button>
            {expanded && (
              <BrowserSteps
                env={env}
                teammate={teammate}
                name={name}
                steps={run.steps}
                replay={replay}
                onReplay={onReplay}
                thumbs
              />
            )}
          </section>
        )
      })}
    </div>
  )
}

/**
 * Steps of the log as a list. Clicking one shows the page as it was after it, in the window; a
 * step that changed the page shows a small picture of it.
 */
export function BrowserSteps({
  env,
  teammate,
  name,
  steps,
  replay,
  onReplay,
  thumbs = false,
  className,
}: {
  env: string
  teammate: string
  name: string
  /** Oldest first; replay steps through these. */
  steps: BrowserAction[]
  replay: BrowserReplay | null
  onReplay: (r: BrowserReplay | null) => void
  thumbs?: boolean
  className?: string
}) {
  const replayed = replay?.steps[replay.at]?.id
  return (
    <ol className={cn("grid", className)}>
      {steps.map((a, i) => {
        const changed =
          !!a.screenshot && (i === 0 || steps[i - 1].url !== a.url)
        const active = replayed === a.id
        return (
          <li key={a.id} className="relative pl-5">
            <span
              aria-hidden
              className={cn(
                "absolute top-2.5 left-[5px] size-2 rounded-full",
                a.outcome === "pending"
                  ? "bg-(--st-waiting)"
                  : a.outcome === "denied" || a.outcome === "failed"
                    ? "bg-(--st-failed)"
                    : a.actor === "user"
                      ? "bg-primary"
                      : "bg-muted-foreground/40"
              )}
            />
            {i < steps.length - 1 && (
              <span
                aria-hidden
                className="absolute top-5 bottom-0 left-[8px] w-px bg-border"
              />
            )}
            <button
              type="button"
              aria-pressed={active}
              onClick={() => onReplay(active ? null : { steps, at: i })}
              className={cn(
                "grid w-full gap-1 rounded-md px-2 py-1.5 text-left hover:bg-muted",
                active && "bg-muted ring-1 ring-border"
              )}
            >
              <span className="flex items-baseline gap-1.5">
                <span className="shrink-0 font-mono text-[0.625rem] text-muted-foreground">
                  {new Date(a.at).toLocaleTimeString([], {
                    hour: "2-digit",
                    minute: "2-digit",
                  })}
                </span>
                <span className="min-w-0 flex-1 break-words">
                  {a.actor === "user" && (
                    <HandIcon className="mr-1 inline size-3 text-primary" />
                  )}
                  {stepWords(a, name)}
                  {a.detail && a.outcome !== "pending" && (
                    <span className="text-muted-foreground"> {a.detail}</span>
                  )}
                </span>
              </span>
              {a.outcome === "pending" ? (
                <span className="flex items-center gap-1 text-[0.6875rem] text-muted-foreground">
                  <span className="size-1.5 rounded-full bg-(--st-waiting)" />
                  Asked you
                </span>
              ) : a.outcome === "denied" ? (
                <span className="text-[0.6875rem] text-muted-foreground">
                  Denied
                </span>
              ) : a.outcome === "failed" ? (
                <span className="text-[0.6875rem] text-destructive">
                  Failed
                </span>
              ) : null}
              {thumbs && changed && (
                <img
                  alt=""
                  loading="lazy"
                  src={screenshotURL(env, teammate, a)}
                  className="pointer-events-none w-44 rounded border"
                />
              )}
            </button>
          </li>
        )
      })}
    </ol>
  )
}

/** The newest picture of a session's page, for the conversation's browsing card. */
export function lastShot(steps: BrowserAction[]) {
  return replayShot({ steps, at: steps.length - 1 })
}

const verdicts = { allow: "Allow", deny: "Block", sensitive: "Hide field" }

/** Every site the teammate's browser has a rule for or went to, with what it may do there. */
function Sites({
  env,
  teammate,
  name,
}: {
  env: string
  teammate: string
  name: string
}) {
  const path = { env, teammate }
  const queryClient = useQueryClient()
  const patterns = useBrowserPatterns(env, teammate)
  const setPolicy = useSetSitePolicy(env, teammate)
  const { actions } = useBrowserRuns(env, teammate)
  const all = patterns.data ?? []
  const del = $api.useMutation("delete", `${statusPath}/patterns/{id}`, {
    onSettled: () =>
      queryClient.invalidateQueries({
        queryKey: ["get", `${statusPath}/patterns`],
      }),
    onError: (err) =>
      toast.error("Could not remove the rule", {
        description: errorMessage(err),
      }),
  })

  // Sites with a rule first, then the ones visited, newest first; "*" is every site.
  const sites = useMemo(() => {
    const seen = new Map<string, string | undefined>()
    for (const p of all) if (!seen.has(p.origin)) seen.set(p.origin, undefined)
    for (const a of actions ?? []) {
      const o = originOf(a.url)
      if (o && !seen.get(o)) seen.set(o, a.at)
    }
    return [...seen.entries()].sort(
      ([a, at], [b, bt]) =>
        Number(b === "*") - Number(a === "*") ||
        (bt ?? "").localeCompare(at ?? "")
    )
  }, [all, actions])

  const remove = (p: BrowserPattern) =>
    del.mutate({ params: { path: { ...path, id: p.id } } })

  return (
    <div className="grid gap-3 p-4 text-xs">
      <p className="text-muted-foreground">
        Reading pages and scrolling are always allowed; scripts, cookies and
        storage never. Other actions on a site without a rule ask you first.
        Hidden fields are left out of what {name} sees, like passwords.
      </p>
      {patterns.isPending && <Spinner />}
      {sites.map(([origin, lastUsed]) => {
        const rules = siteRules(all, origin)
        return (
          <section
            key={origin}
            aria-label={origin === "*" ? "All sites" : hostOf(origin)}
            className="grid gap-2 rounded-lg border p-3"
          >
            <div className="flex items-center gap-2">
              <span className="min-w-0 flex-1 truncate font-mono font-medium">
                {origin === "*" ? "All sites" : hostOf(origin)}
              </span>
              {lastUsed && (
                <span className="text-muted-foreground">
                  {formatAge(lastUsed)}
                </span>
              )}
            </div>
            <PolicyPicker
              value={sitePolicy(all, origin)}
              onChange={(p) => void setPolicy(origin, p)}
            />
            {(rules.actions.length > 0 || rules.hidden.length > 0) && (
              <div className="flex flex-wrap gap-1">
                {rules.actions.map((p) => (
                  <RuleChip key={p.id} onRemove={() => remove(p)}>
                    {p.verdict === "allow" ? "Allow" : "Block"}{" "}
                    <span className="font-mono">{p.action}</span>
                    {(p.role || (p.label && p.label !== "*")) && (
                      <> on {elementWords(p)}</>
                    )}
                  </RuleChip>
                ))}
                {rules.hidden.map((p) => (
                  <RuleChip key={p.id} onRemove={() => remove(p)}>
                    <EyeSlashIcon className="size-3" />
                    {elementWords(p)}
                  </RuleChip>
                ))}
              </div>
            )}
          </section>
        )
      })}
      <AddRule env={env} teammate={teammate} name={name} />
    </div>
  )
}

function RuleChip({
  children,
  onRemove,
}: {
  children: ReactNode
  onRemove: () => void
}) {
  return (
    <span className="inline-flex items-center gap-1 rounded bg-muted py-0.5 pr-0.5 pl-1.5">
      {children}
      <button
        type="button"
        aria-label="Remove the rule"
        onClick={onRemove}
        className="inline-flex size-4 items-center justify-center rounded hover:bg-background"
      >
        <XIcon className="size-2.5" />
      </button>
    </span>
  )
}

/** A rule for one action or element, or a field to hide: what the policy picker can't say. */
function AddRule({
  env,
  teammate,
  name,
}: {
  env: string
  teammate: string
  name: string
}) {
  const [open, setOpen] = useState(false)
  const queryClient = useQueryClient()
  const add = $api.useMutation("post", `${statusPath}/patterns`, {
    onSettled: () =>
      queryClient.invalidateQueries({
        queryKey: ["get", `${statusPath}/patterns`],
      }),
    onError: (err) =>
      toast.error("Could not add the rule", {
        description: errorMessage(err),
      }),
  })
  const [form, setForm] = useState({
    verdict: "allow" as BrowserPattern["verdict"],
    action: "click",
    origin: "",
    role: "",
    label: "",
  })
  const submit = (e: FormEvent) => {
    e.preventDefault()
    add.mutate(
      { params: { path: { env, teammate } }, body: form },
      { onSuccess: () => setForm({ ...form, label: "" }) }
    )
  }
  if (!open)
    return (
      <button
        type="button"
        className="justify-self-start text-muted-foreground underline-offset-4 hover:text-foreground hover:underline"
        onClick={() => setOpen(true)}
      >
        Add a rule or hide a field from {name}…
      </button>
    )
  return (
    <form
      onSubmit={submit}
      aria-label="Add a browser rule"
      className="grid gap-2 rounded-lg border border-dashed p-3"
    >
      <div className="flex gap-2">
        <Select
          value={form.verdict}
          onValueChange={(v) => v && setForm({ ...form, verdict: v })}
          items={verdicts}
        >
          <SelectTrigger size="sm" className="w-32" aria-label="Rule">
            <SelectValue />
          </SelectTrigger>
          <SelectContent>
            {Object.entries(verdicts).map(([v, label]) => (
              <SelectItem key={v} value={v}>
                {label}
              </SelectItem>
            ))}
          </SelectContent>
        </Select>
        {form.verdict !== "sensitive" && (
          <Input
            aria-label="Action"
            className="h-8 min-w-0 flex-1 font-mono"
            placeholder="click, fill or *"
            required
            value={form.action}
            onChange={(e) => setForm({ ...form, action: e.target.value })}
          />
        )}
      </div>
      <Input
        aria-label="Site"
        className="h-8 font-mono"
        placeholder="https://example.com or *.example.com"
        required
        value={form.origin}
        onChange={(e) => setForm({ ...form, origin: e.target.value })}
      />
      <div className="flex gap-2">
        <Input
          aria-label="Role"
          className="h-8 w-24"
          placeholder="role"
          value={form.role}
          onChange={(e) => setForm({ ...form, role: e.target.value })}
        />
        <Input
          aria-label="Label"
          className="h-8 min-w-0 flex-1"
          placeholder="label, * as wildcard"
          value={form.label}
          onChange={(e) => setForm({ ...form, label: e.target.value })}
        />
      </div>
      <div className="flex justify-end gap-1.5">
        <Button
          size="sm"
          variant="ghost"
          type="button"
          onClick={() => setOpen(false)}
        >
          Done
        </Button>
        <Button size="sm" type="submit" disabled={add.isPending}>
          Add rule
        </Button>
      </div>
    </form>
  )
}
