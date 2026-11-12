import { useState } from "react"
import { Link } from "@tanstack/react-router"
import { useQueryClient } from "@tanstack/react-query"
import { toast } from "sonner"
import {
  ArrowRightIcon,
  CaretRightIcon,
  MoonStarsIcon,
  PlusIcon,
} from "@phosphor-icons/react"
import { invalidateMemory } from "@/components/memory-dialogs"
import { CopyTeammateDialog } from "@/components/teammate-options-menu"
import { Button } from "@/components/ui/button"
import { Checkbox } from "@/components/ui/checkbox"
import {
  Collapsible,
  CollapsibleContent,
  CollapsibleTrigger,
} from "@/components/ui/collapsible"
import { Input } from "@/components/ui/input"
import { Spinner } from "@/components/ui/spinner"
import { $api, errorMessage, type Teammate } from "@/lib/api/client"
import type { components } from "@/lib/api/schema.gen"
import { cn, formatAge } from "@/lib/utils"

type Rule = components["schemas"]["Rule"]

const week = 7 * 24 * 60 * 60_000

/** What a teammate's memory holds, what needs the user, and when it last dreamt. */
export function useMemorySummary(env: string, teammate: string) {
  const scope = `teammate:${teammate}`
  const scopes = $api.useQuery("get", "/api/environments/{env}/memory/scopes", {
    params: { path: { env } },
  })
  const conflicts = $api.useQuery(
    "get",
    "/api/environments/{env}/memory/conflicts",
    { params: { path: { env }, query: { scope } } }
  )
  const facts = $api.useQuery("get", "/api/environments/{env}/memory/facts", {
    params: { path: { env }, query: { scope, status: "active" } },
  })
  const dreams = $api.useQuery(
    "get",
    "/api/environments/{env}/memory/dreams",
    { params: { path: { env }, query: { scope, limit: 1 } } },
    {
      refetchInterval: (q) =>
        q.state.data?.some((r) => r.status === "running") ? 2000 : false,
    }
  )
  const current = scopes.data?.find((s) => s.scope === scope)
  const since = Date.now() - week
  return {
    scope,
    loaded: !!scopes.data,
    pages: current?.pages ?? 0,
    facts: current?.facts ?? 0,
    needYou: (conflicts.data ?? []).filter((c) => c.status === "open").length,
    thisWeek: (facts.data ?? []).filter(
      (f) => new Date(f.createdAt).getTime() >= since
    ).length,
    lastDream: dreams.data?.[0],
  }
}

/** The Memory row's value: its pages and facts, and what needs the user. */
export function MemorySummary({ env, id }: { env: string; id: string }) {
  const m = useMemorySummary(env, id)
  if (!m.loaded) return null
  return (
    <>
      {m.pages} {m.pages === 1 ? "page" : "pages"} · {m.facts}{" "}
      {m.facts === 1 ? "fact" : "facts"}
      {m.needYou > 0 && (
        <span className="text-amber-700 dark:text-amber-400">
          {" "}
          · {m.needYou} need you
        </span>
      )}
    </>
  )
}

/**
 * The Memory row of a teammate's settings: its counts, the way to its memory, a fact to add,
 * Dream now, and what its memory does on its own.
 */
export function TeammateKnowledge({
  env,
  teammate,
  onMemory,
}: {
  env: string
  teammate: Teammate
  /** Turns the sheet into the teammate's Memory sheet. */
  onMemory: () => void
}) {
  const queryClient = useQueryClient()
  const m = useMemorySummary(env, teammate.id)
  const [adding, setAdding] = useState(false)
  const [text, setText] = useState("")
  const add = $api.useMutation("post", "/api/environments/{env}/memory/facts", {
    onSuccess: () => {
      setText("")
      setAdding(false)
      toast.success(`${teammate.name} remembers it`)
      void invalidateMemory(queryClient)
    },
    onError: (err) =>
      toast.error("Could not add the fact", {
        description: errorMessage(err),
      }),
  })
  const dream = $api.useMutation(
    "post",
    "/api/environments/{env}/memory/dream",
    {
      onSettled: () => invalidateMemory(queryClient),
      onError: (err) =>
        toast.error("Could not start the dream", {
          description: errorMessage(err),
        }),
    }
  )
  const set = $api.useMutation(
    "put",
    "/api/environments/{env}/teammates/{id}/memory",
    {
      onSuccess: () =>
        queryClient.invalidateQueries({
          queryKey: ["get", "/api/environments/{env}/teammates"],
        }),
      onError: (err) =>
        toast.error("Could not change it", { description: errorMessage(err) }),
    }
  )
  const dreaming = dream.isPending || m.lastDream?.status === "running"
  const lastDream = m.lastDream?.finishedAt ?? m.lastDream?.startedAt
  const path = { env, id: teammate.id }

  return (
    <>
      <dl className="flex flex-wrap gap-x-4 gap-y-1 text-xs">
        {(
          [
            [m.pages, m.pages === 1 ? "page" : "pages"],
            [m.facts, m.facts === 1 ? "fact" : "facts"],
            [m.thisWeek, "this week"],
          ] as const
        ).map(([n, label]) => (
          <div key={label} className="flex items-baseline gap-1">
            <dt className="sr-only">{label}</dt>
            <dd className="font-medium tabular-nums">{n}</dd>
            <span className="text-muted-foreground">{label}</span>
          </div>
        ))}
        <div className="flex items-baseline gap-1 text-muted-foreground">
          {dreaming ? (
            "dreaming now"
          ) : lastDream ? (
            <>
              dreamt{" "}
              <span className="font-medium text-foreground">
                {formatAge(lastDream)}
              </span>
            </>
          ) : (
            "never dreamt"
          )}
        </div>
      </dl>
      <div className="flex flex-wrap gap-2">
        <Button size="sm" onClick={onMemory}>
          Open {teammate.name}'s memory
          <ArrowRightIcon />
        </Button>
        <Button
          size="sm"
          variant="outline"
          aria-expanded={adding}
          onClick={() => setAdding((a) => !a)}
        >
          <PlusIcon />
          Add a fact
        </Button>
        <Button
          size="sm"
          variant="outline"
          disabled={dreaming}
          title="Merge duplicates, retire outdated facts, find contradictions and compile pages"
          onClick={() =>
            dream.mutate({
              params: { path: { env } },
              body: { scope: m.scope },
            })
          }
        >
          {dreaming ? <Spinner /> : <MoonStarsIcon />}
          {dreaming ? "Dreaming…" : "Dream now"}
        </Button>
      </div>
      {adding && (
        <form
          className="flex gap-2"
          onSubmit={(e) => {
            e.preventDefault()
            if (!text.trim()) return
            add.mutate({
              params: { path: { env } },
              body: { scope: m.scope, kind: "fact", text: text.trim() },
            })
          }}
        >
          <Input
            autoFocus
            aria-label="A fact to remember"
            placeholder={`Something ${teammate.name} should know`}
            value={text}
            onChange={(e) => setText(e.target.value)}
            className="h-8 text-xs"
          />
          <Button
            size="sm"
            type="submit"
            disabled={!text.trim() || add.isPending}
          >
            {add.isPending && <Spinner />}
            Remember
          </Button>
        </form>
      )}
      <div className="grid gap-2.5">
        <label className="flex items-start gap-2 text-xs">
          <Checkbox
            className="mt-0.5"
            checked={teammate.memoryLearn}
            disabled={set.isPending}
            onCheckedChange={(on) =>
              set.mutate({ params: { path }, body: { learn: on } })
            }
          />
          <span>
            Learn from its chats
            <span className="block text-caption text-muted-foreground">
              Facts it picks up in chats and sessions. Off, it still keeps a
              fact it's asked to remember, and the ones you add.
            </span>
          </span>
        </label>
        <label className="flex items-start gap-2 text-xs">
          <Checkbox
            className="mt-0.5"
            checked={teammate.memoryDream}
            disabled={set.isPending}
            onCheckedChange={(on) =>
              set.mutate({ params: { path }, body: { dream: on } })
            }
          />
          <span>
            Dream every night
            <span className="block text-caption text-muted-foreground">
              Tidies its memory after 3:00, and after 25 new facts. Dream now
              works either way.
            </span>
          </span>
        </label>
      </div>
    </>
  )
}

/** The Tasks row's value: its open tasks and its schedules. */
export function useTaskSummary(env: string, id: string) {
  const tasks = $api.useQuery(
    "get",
    "/api/environments/{env}/teammates/{id}/tasks",
    { params: { path: { env, id } } }
  )
  const list = tasks.data ?? []
  const open = list.filter((t) => t.status !== "done")
  const scheduled = list.filter(
    (t) => t.schedule && !t.schedule.paused && t.status !== "done"
  )
  return { loaded: !!tasks.data, open, scheduled }
}

export function TaskSummary({ env, id }: { env: string; id: string }) {
  const t = useTaskSummary(env, id)
  if (!t.loaded) return null
  return (
    <>
      {t.open.length} open
      {t.scheduled.length > 0 && ` · ${t.scheduled.length} scheduled`}
    </>
  )
}

/** The Tasks row: the first open tasks and the way to all of them. */
export function TeammateTaskList({
  env,
  teammate,
  onTasks,
}: {
  env: string
  teammate: Teammate
  onTasks: () => void
}) {
  const t = useTaskSummary(env, teammate.id)
  return (
    <>
      {t.open.length === 0 ? (
        <p className="text-xs text-muted-foreground">Nothing open.</p>
      ) : (
        <ul className="grid gap-1 text-xs">
          {t.open.slice(0, 4).map((task) => (
            <li key={task.id} className="flex min-w-0 items-center gap-2">
              <span className="size-3 shrink-0 rounded-sm border border-foreground/30" />
              <span className="min-w-0 flex-1 truncate">{task.title}</span>
              {task.schedule && (
                <span className="shrink-0 text-caption text-muted-foreground">
                  {task.schedule.paused ? "paused" : "scheduled"}
                </span>
              )}
            </li>
          ))}
          {t.open.length > 4 && (
            <li className="text-caption text-muted-foreground">
              {t.open.length - 4} more
            </li>
          )}
        </ul>
      )}
      <div>
        <Button size="sm" variant="outline" onClick={onTasks}>
          Open {teammate.name}'s tasks
          <ArrowRightIcon />
        </Button>
      </div>
    </>
  )
}

/** The rules that apply to a teammate's computers: its own and its computers', and the environment's. */
function useTeammateRules(env: string, id: string) {
  const rules = $api.useQuery("get", "/api/environments/{env}/rules", {
    params: { path: { env } },
  })
  const sandboxes = $api.useQuery("get", "/api/environments/{env}/sandboxes", {
    params: { path: { env } },
  })
  const mine = new Set(
    (sandboxes.data ?? []).filter((s) => s.teammateId === id).map((s) => s.id)
  )
  const list = rules.data ?? []
  return {
    loaded: !!rules.data,
    own: list.filter(
      (r) => r.teammateId === id || (!!r.sandboxId && mine.has(r.sandboxId))
    ),
    environment: list.filter((r) => !r.teammateId && !r.sandboxId),
  }
}

export function RulesSummary({ env, id }: { env: string; id: string }) {
  const r = useTeammateRules(env, id)
  if (!r.loaded) return null
  return (
    <>
      {r.own.length} its own · {r.environment.length} from the environment
    </>
  )
}

const actionWords: Record<Rule["action"], string> = {
  allow: "Allow",
  proxy: "Proxy",
  caddy: "Caddy",
  deny: "Deny",
}

/** One rule in a line: what it does to which host, and a link to it in the network settings. */
function RuleLine({ env, rule }: { env: string; rule: Rule }) {
  const where = [
    rule.method && rule.method !== "*" ? rule.method : "",
    rule.pathPrefix && rule.pathPrefix !== "/" ? rule.pathPrefix : "",
  ]
    .filter(Boolean)
    .join(" ")
  return (
    <li>
      <Link
        to="/e/$env/settings/network"
        params={{ env }}
        search={{ rule: rule.id }}
        className="flex min-w-0 items-baseline gap-2 rounded-sm px-1.5 py-1 text-xs hover:bg-muted"
      >
        <span
          className={cn(
            "w-10 shrink-0",
            rule.action === "deny" && "text-destructive"
          )}
        >
          {actionWords[rule.action]}
        </span>
        <span className="min-w-0 flex-1 truncate font-mono">
          {rule.host}
          {rule.ports?.length ? `:${rule.ports.join(",")}` : ""}
          {where && <span className="text-muted-foreground"> {where}</span>}
        </span>
        {rule.sandboxId && (
          <span className="shrink-0 text-caption text-muted-foreground">
            one computer
          </span>
        )}
      </Link>
    </li>
  )
}

/** The Network rules row: the rules scoped to the teammate, and the environment's in a fold. */
export function TeammateRules({ env, id }: { env: string; id: string }) {
  const r = useTeammateRules(env, id)
  return (
    <>
      <p className="text-xs text-muted-foreground">
        What its computers may reach besides the level for new hosts. Its own
        rules come first; the environment's apply to every teammate.
      </p>
      {r.own.length === 0 ? (
        <p className="text-xs text-muted-foreground">None of its own yet.</p>
      ) : (
        <ul aria-label="Its own rules" className="grid">
          {r.own.map((rule) => (
            <RuleLine key={rule.id} env={env} rule={rule} />
          ))}
        </ul>
      )}
      {r.environment.length > 0 && (
        <Collapsible>
          <CollapsibleTrigger className="group flex items-center gap-1 text-xs text-muted-foreground hover:text-foreground">
            <CaretRightIcon className="size-3 transition-transform group-data-[panel-open]:rotate-90" />
            {r.environment.length} from the environment
          </CollapsibleTrigger>
          <CollapsibleContent>
            <ul aria-label="The environment's rules" className="mt-1 grid">
              {r.environment.map((rule) => (
                <RuleLine key={rule.id} env={env} rule={rule} />
              ))}
            </ul>
          </CollapsibleContent>
        </Collapsible>
      )}
      <div>
        <Button
          size="sm"
          variant="outline"
          render={
            <Link
              to="/e/$env/settings/network"
              params={{ env }}
              search={{ scope: "teammate" }}
            />
          }
        >
          Edit in Network settings
          <ArrowRightIcon />
        </Button>
      </div>
    </>
  )
}

/** The Copy row: a copy of the teammate in another environment. */
export function CopyTeammate({
  env,
  teammate,
}: {
  env: string
  teammate: Teammate
}) {
  const [open, setOpen] = useState(false)
  return (
    <>
      <p className="text-xs text-muted-foreground">
        The copy keeps its name, role, avatar, soul, agent, model, git identity
        and approval setting. Its sandboxes, chats, memory, secrets and keys
        stay here.
      </p>
      <div>
        <Button size="sm" variant="outline" onClick={() => setOpen(true)}>
          Copy to another environment…
        </Button>
      </div>
      <CopyTeammateDialog
        env={env}
        teammate={teammate}
        open={open}
        onOpenChange={setOpen}
      />
    </>
  )
}
