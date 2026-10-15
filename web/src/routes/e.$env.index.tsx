import { useState, type ReactNode } from "react"
import { Link, createFileRoute } from "@tanstack/react-router"
import { ScrollArea } from "@/components/ui/scroll-area"
import {
  ArrowRightIcon,
  ChatCircleDotsIcon,
  ClockCounterClockwiseIcon,
  CubeIcon,
  GitForkIcon,
  PlusIcon,
  ShieldCheckIcon,
  UploadSimpleIcon,
  UsersThreeIcon,
  type Icon,
} from "@phosphor-icons/react"
import { Button } from "@/components/ui/button"
import {
  Empty,
  EmptyContent,
  EmptyDescription,
  EmptyHeader,
  EmptyMedia,
  EmptyTitle,
} from "@/components/ui/empty"
import { Skeleton } from "@/components/ui/skeleton"
import {
  ApprovalCardList,
  useApprovalReview,
} from "@/components/approval-stack"
import { CreateSandboxDialog } from "@/components/create-sandbox-dialog"
import { ImportSandboxDialog } from "@/components/import-sandbox-dialog"
import { Ident } from "@/components/ident"
import { SetupCard } from "@/components/setup-card"
import { LineageTree } from "@/components/lineage-tree"
import { PageHeader } from "@/components/page-header"
import { SectionHeader } from "@/components/section-header"
import { phase, phaseDot } from "@/components/status-badge"
import { StatusDot } from "@/components/status-dot"
import { PresenceDot, TeammateAvatar } from "@/components/teammate-avatar"
import { WaitingCount } from "@/components/waiting-count"
import { usePendingApprovals } from "@/hooks/use-pending-approvals"
import {
  presenceLabels,
  usePresence,
  type Presence,
} from "@/hooks/use-teammate-presence"
import {
  $api,
  isReady,
  type Approval,
  type PreviewPort,
  type Sandbox,
  type Teammate,
} from "@/lib/api/client"
import { tileClass, type Area } from "@/lib/accents"
import { approvalCards } from "@/lib/approval-groups"
import {
  approvalDecision,
  approvalKind,
  approvalSource,
} from "@/lib/approval-kinds"
import { shortTemplate } from "@/lib/lineage"
import { cn, formatAge, formatUptime } from "@/lib/utils"

export const Route = createFileRoute("/e/$env/")({ component: Overview })

/** The sandbox table's columns from the large breakpoint: name, owner, template, status, ports, waiting. */
const tableColumns =
  "lg:grid-cols-[minmax(0,1.6fr)_minmax(0,1fr)_minmax(0,1fr)_minmax(0,1.1fr)_minmax(0,1fr)_2.5rem]"

/** Each cell's column and row from the large breakpoint, placed explicitly so a row stays on one line. */
const cellAt = {
  name: "lg:col-start-1 lg:row-start-1",
  owner: "lg:col-start-2 lg:row-start-1",
  template: "lg:col-start-3 lg:row-start-1",
  status: "lg:col-start-4 lg:row-start-1",
  ports: "lg:col-start-5 lg:row-start-1",
  waiting: "lg:col-start-6 lg:row-start-1",
}

/** Approval cards Home shows before linking to the rest. */
const maxApprovals = 4

/** Entries of the activity list. */
const maxActivity = 8

/**
 * The environment at a glance. The numbers on top; the sandboxes and what happened lately on the
 * left; on the right what needs you (approvals, the setup) and the teammates. Home answers its
 * environment's approvals in place, so the floating stack leaves them out here. Below the extra
 * large breakpoint the approvals come first.
 */
function Overview() {
  const { env } = Route.useParams()
  const [creating, setCreating] = useState(false)
  const [importing, setImporting] = useState(false)
  const sandboxes = $api.useQuery(
    "get",
    "/api/environments/{env}/sandboxes",
    { params: { path: { env } } },
    { refetchInterval: 3000 }
  )
  const teammates = $api.useQuery("get", "/api/environments/{env}/teammates", {
    params: { path: { env } },
  })
  const approvals = usePendingApprovals()
  const presence = usePresence(env)

  const list = sandboxes.data ?? []
  const people = teammates.data ?? []
  const waiting = (approvals.data ?? []).filter((a) => a.environmentId === env)
  const waitingBySandbox = new Map<string, number>()
  for (const a of waiting) {
    if (a.sandboxId)
      waitingBySandbox.set(
        a.sandboxId,
        (waitingBySandbox.get(a.sandboxId) ?? 0) + 1
      )
  }

  return (
    <>
      <PageHeader>
        <h1 className="text-sm font-medium">Home</h1>
        <Button
          size="sm"
          variant="outline"
          className="ml-auto"
          onClick={() => setImporting(true)}
        >
          <UploadSimpleIcon />
          Import sandbox
        </Button>
        <Button size="sm" onClick={() => setCreating(true)}>
          <PlusIcon />
          New sandbox
        </Button>
      </PageHeader>
      <ScrollArea
        className="min-h-0 flex-1"
        viewportClassName="text-[0.8125rem]"
      >
        <Stats
          sandboxes={list.length}
          running={list.filter(isReady).length}
          approvals={waiting.length}
          teammates={people.length}
          working={people.filter((p) => presence(p.id) === "working").length}
        />
        <div className="grid content-start gap-6 p-3 md:p-4 xl:grid-cols-[minmax(0,1fr)_22rem] xl:grid-rows-[auto_1fr]">
          <Approvals
            env={env}
            approvals={waiting}
            className="xl:col-start-2 xl:row-start-1"
          />
          <div className="grid min-w-0 content-start gap-6 xl:col-start-1 xl:row-span-2 xl:row-start-1">
            {sandboxes.isPending ? (
              <Skeleton className="h-40" />
            ) : list.length ? (
              <SandboxTable
                env={env}
                sandboxes={list}
                teammates={people}
                waiting={waitingBySandbox}
              />
            ) : (
              <NoSandboxes onCreate={() => setCreating(true)} />
            )}
            <Activity env={env} />
            <Family env={env} />
          </div>
          <div className="grid min-w-0 content-start gap-6 xl:col-start-2 xl:row-start-2">
            <SetupCard env={env} />
            <TeammateList
              env={env}
              teammates={people}
              sandboxes={list}
              presence={presence}
            />
          </div>
        </div>
      </ScrollArea>
      <CreateSandboxDialog
        env={env}
        open={creating}
        onOpenChange={setCreating}
      />
      <ImportSandboxDialog
        env={env}
        open={importing}
        onOpenChange={setImporting}
      />
    </>
  )
}

/** The numbers you can count without another request: sandboxes, approvals and teammates. */
function Stats({
  sandboxes,
  running,
  approvals,
  teammates,
  working,
}: {
  sandboxes: number
  running: number
  approvals: number
  teammates: number
  working: number
}) {
  const items = [
    { label: "Sandboxes", value: running, rest: `/ ${sandboxes} running` },
    { label: "Approvals", value: approvals, rest: "waiting" },
    { label: "Teammates", value: working, rest: `/ ${teammates} working` },
  ]
  return (
    <div className="grid grid-cols-3 border-b bg-[linear-gradient(var(--wb-grid)_1px,transparent_1px),linear-gradient(90deg,var(--wb-grid)_1px,transparent_1px)] bg-size-[12px_12px]">
      {items.map((s) => (
        <div
          key={s.label}
          className="min-w-0 border-r px-4 py-2.5 last:border-r-0"
        >
          <div className="font-mono text-[0.625rem] tracking-wider text-muted-foreground uppercase">
            {s.label}
          </div>
          <div className="mt-0.5 flex min-w-0 items-baseline gap-1.5">
            <span className="font-mono text-lg leading-6 font-semibold">
              {s.value}
            </span>
            <span className="truncate text-muted-foreground">{s.rest}</span>
          </div>
        </div>
      ))}
    </div>
  )
}

function NoSandboxes({ onCreate }: { onCreate: () => void }) {
  return (
    <Empty className="rounded-(--radius) border">
      <EmptyHeader>
        <EmptyMedia variant="icon">
          <CubeIcon />
        </EmptyMedia>
        <EmptyTitle>No sandboxes yet</EmptyTitle>
        <EmptyDescription>
          A sandbox is a Linux VM with Docker and a persistent workspace, where
          teammates and you work on a project.{" "}
          <a
            href="https://lukaskoebe.github.io/sandbox-studio/sandboxes/"
            target="_blank"
            rel="noreferrer"
            className="underline underline-offset-4"
          >
            Learn more
          </a>
        </EmptyDescription>
      </EmptyHeader>
      <EmptyContent>
        <Button onClick={onCreate}>
          <PlusIcon />
          New sandbox
        </Button>
      </EmptyContent>
    </Empty>
  )
}

/**
 * The sandboxes as a table. Each row is a link to the sandbox. Below the large breakpoint the
 * columns stack: the name and the approvals on the first line, the owner, status and ports on
 * the second. A cell with nothing to say stays empty or shows a dash, never a word like "None".
 */
function SandboxTable({
  env,
  sandboxes,
  teammates,
  waiting,
}: {
  env: string
  sandboxes: Sandbox[]
  teammates: Teammate[]
  waiting: Map<string, number>
}) {
  const owners = new Map(teammates.map((t) => [t.id, t]))
  return (
    <section className="grid min-w-0 content-start gap-2">
      <SectionHeader
        title="Sandboxes"
        icon={CubeIcon}
        count={sandboxes.length}
      />
      <div className="overflow-hidden rounded-(--radius) border bg-card">
        <div
          className={cn(
            "hidden h-(--wb-header) items-center gap-3 border-b bg-muted/50 px-3 font-mono text-[0.625rem] tracking-wider text-muted-foreground uppercase lg:grid",
            tableColumns
          )}
        >
          <span>Name</span>
          <span>Owner</span>
          <span>Template</span>
          <span>Status</span>
          <span>Ports</span>
          <span className="text-right">Ask</span>
        </div>
        {sandboxes.map((sb) => {
          const owner = sb.teammateId ? owners.get(sb.teammateId) : undefined
          const p = phase(sb)
          return (
            <Link
              key={sb.id}
              to="/e/$env/sandboxes/$id"
              params={{ env, id: sb.id }}
              className={cn(
                "flex flex-wrap items-center gap-x-3 gap-y-1 border-b px-3 py-2 last:border-b-0 hover:bg-muted/40 lg:grid lg:h-(--wb-row) lg:gap-y-0 lg:py-0",
                tableColumns
              )}
            >
              <span
                className={cn(
                  "flex min-w-0 flex-1 items-baseline gap-2",
                  cellAt.name
                )}
              >
                <span className="truncate font-mono font-medium">
                  {sb.name}
                </span>
                <span className="truncate font-mono text-[0.625rem] text-muted-foreground max-sm:hidden">
                  {sb.id}
                </span>
              </span>
              <span className={cn("shrink-0 justify-self-end", cellAt.waiting)}>
                <WaitingCount count={waiting.get(sb.id) ?? 0} />
              </span>
              <span className="flex min-w-0 basis-full flex-wrap items-center gap-x-3 gap-y-1 text-xs lg:contents">
                <span
                  className={cn(
                    "flex min-w-0 items-center gap-1.5 text-muted-foreground",
                    cellAt.owner
                  )}
                >
                  {owner ? (
                    <>
                      <TeammateAvatar
                        name={owner.name}
                        avatar={owner.avatar}
                        className="size-4 rounded-[3px] text-[0.5rem]"
                      />
                      <span className="truncate">{owner.name}</span>
                    </>
                  ) : (
                    <span aria-label="No owner" className="max-lg:hidden">
                      —
                    </span>
                  )}
                </span>
                <span className={cn("min-w-0 max-lg:hidden", cellAt.template)}>
                  {sb.templateId ? (
                    <Ident>
                      {sb.templateName ?? shortTemplate(sb.templateId)}
                    </Ident>
                  ) : (
                    <span className="text-muted-foreground">Base image</span>
                  )}
                </span>
                <span
                  className={cn(
                    "flex min-w-0 items-center gap-1.5",
                    cellAt.status
                  )}
                >
                  <StatusDot {...phaseDot(p)} className="shrink-0" />
                  {p === "ready" && sb.startedAt && (
                    <span className="truncate text-muted-foreground">
                      {formatUptime(sb.startedAt).toLowerCase()}
                    </span>
                  )}
                </span>
                <RowPorts env={env} sandbox={sb} className={cellAt.ports} />
              </span>
            </Link>
          )
        })}
      </div>
    </section>
  )
}

/** A sandbox's listening ports, in the table. They are only asked for while it runs. */
function RowPorts({
  env,
  sandbox: sb,
  className,
}: {
  env: string
  sandbox: Sandbox
  className?: string
}) {
  const ports = $api.useQuery(
    "get",
    "/api/environments/{env}/sandboxes/{id}/ports",
    { params: { path: { env, id: sb.id } } },
    { refetchInterval: 3000, enabled: isReady(sb) }
  )
  const list: PreviewPort[] = isReady(sb) ? (ports.data ?? []) : []
  return (
    <span className={cn("flex min-w-0 flex-wrap gap-1", className)}>
      {list.map((p) => (
        <Ident key={p.port}>{`:${p.port}`}</Ident>
      ))}
    </span>
  )
}

/**
 * The environment's approvals, as the same cards the stack shows, answered here. Requests from
 * one sandbox that came together share a card, and "Review individually" splits them.
 */
function Approvals({
  env,
  approvals,
  className,
}: {
  env: string
  approvals: Approval[]
  className?: string
}) {
  const [split, setSplit] = useState<ReadonlySet<string>>(() => new Set())
  const envs = $api.useQuery("get", "/api/environments")
  const { sheet, review } = useApprovalReview(approvals)
  const cards = approvalCards(approvals, split)
  const envName = () => envs.data?.find((e) => e.id === env)?.name
  return (
    <section
      aria-label="Approvals"
      className={cn(
        "grid min-w-0 content-start gap-2",
        cards.length === 0 && "max-xl:hidden",
        className
      )}
    >
      <SectionHeader
        title="Approvals"
        icon={ShieldCheckIcon}
        count={approvals.length}
      />
      {cards.length === 0 ? (
        <p className="text-xs text-muted-foreground">
          Nothing is waiting for you.
        </p>
      ) : (
        <div className="grid gap-2">
          <ApprovalCardList
            cards={cards}
            max={maxApprovals}
            envName={envName}
            onReview={review}
            onSplit={(ids) => setSplit((prev) => new Set([...prev, ...ids]))}
          />
        </div>
      )}
      {sheet}
    </section>
  )
}

type Entry = {
  key: string
  at: string
  icon: Icon
  area?: Area
  text: ReactNode
  who?: string
}

/**
 * A decided git review or memory conflict links to its own page; other approvals are plain
 * text.
 */
function ApprovalPageLink({
  approval: a,
  children,
}: {
  approval: Approval
  children: ReactNode
}) {
  const className = "underline-offset-4 hover:underline"
  if (a.kind === "git.push" || a.kind === "git.pr")
    return (
      <Link
        to="/e/$env/git/reviews/$id"
        params={{ env: a.environmentId, id: a.id }}
        className={className}
      >
        {children}
      </Link>
    )
  const conflict = a.memoryConflict?.conflictId
  if (conflict)
    return (
      <Link
        to="/e/$env/memory/conflicts/$id"
        params={{ env: a.environmentId, id: conflict }}
        className={className}
      >
        {children}
      </Link>
    )
  return <>{children}</>
}

/**
 * What happened lately: decided approvals (allowed, denied, by whom or which rule) and chats
 * started, newest first. Network decisions link on to the Network page's Activity tab; git reviews
 * and memory conflicts to their own pages.
 */
function Activity({ env }: { env: string }) {
  const approvals = $api.useQuery("get", "/api/environments/{env}/approvals", {
    params: { path: { env }, query: { status: "all" } },
  })
  const chats = $api.useQuery("get", "/api/environments/{env}/chats", {
    params: { path: { env } },
  })
  const teammates = $api.useQuery("get", "/api/environments/{env}/teammates", {
    params: { path: { env } },
  })
  const names = new Map((teammates.data ?? []).map((t) => [t.id, t.name]))
  const entries: Entry[] = [
    ...(approvals.data ?? [])
      .filter((a) => a.status !== "pending")
      .map((a): Entry => {
        const kind = approvalKind(a)
        const summary = kind.summary(a)
        return {
          key: a.id,
          at: a.updatedAt,
          icon: kind.icon,
          area: kind.area,
          text: (
            <ApprovalPageLink approval={a}>
              <span className="text-muted-foreground">
                {approvalDecision(a)}
              </span>{" "}
              {kind.mono ? <Ident>{summary}</Ident> : summary}
            </ApprovalPageLink>
          ),
          who: approvalSource(a),
        }
      }),
    ...(chats.data ?? []).map((c): Entry => ({
      key: c.id,
      at: c.createdAt,
      icon: ChatCircleDotsIcon,
      area: "chat",
      text: (
        <>
          <span className="text-muted-foreground">
            {c.heartbeat ? "Heartbeat" : "Started chat"}
          </span>{" "}
          {c.title ? `“${c.title}”` : "New chat"}
        </>
      ),
      who: c.teammateId ? names.get(c.teammateId) : undefined,
    })),
  ]
    .sort((a, b) => Date.parse(b.at) - Date.parse(a.at))
    .slice(0, maxActivity)

  return (
    <section className="grid min-w-0 content-start gap-2">
      <SectionHeader
        title="Activity"
        icon={ClockCounterClockwiseIcon}
        actions={
          <Button
            variant="ghost"
            size="xs"
            render={<Link to="/e/$env/network/activity" params={{ env }} />}
          >
            All network activity
            <ArrowRightIcon />
          </Button>
        }
      />
      {entries.length === 0 ? (
        <p className="text-xs text-muted-foreground">
          {approvals.isPending ? "Loading…" : "Nothing has happened yet."}
        </p>
      ) : (
        <ul className="overflow-hidden rounded-(--radius) border bg-card">
          {entries.map((e) => (
            <li
              key={e.key}
              className="flex h-(--wb-row) items-center gap-2.5 border-b px-3 last:border-b-0"
            >
              <span
                className={cn(
                  "grid size-5 shrink-0 place-items-center rounded-[4px]",
                  tileClass(e.area)
                )}
              >
                <e.icon className="size-3" aria-hidden />
              </span>
              <span className="min-w-0 flex-1 truncate">{e.text}</span>
              {e.who && (
                <span className="max-w-32 shrink-0 truncate text-xs text-muted-foreground max-sm:hidden">
                  {e.who}
                </span>
              )}
              <span className="w-14 shrink-0 text-right font-mono text-[0.6875rem] text-muted-foreground">
                {formatAge(e.at)}
              </span>
            </li>
          ))}
        </ul>
      )}
    </section>
  )
}

/**
 * The family tree, once there is one: shown only when a sandbox was forked. Sandboxes that
 * were never forked are all in the table already.
 */
function Family({ env }: { env: string }) {
  const tree = $api.useQuery("get", "/api/environments/{env}/lineage", {
    params: { path: { env } },
  })
  const forked = (tree.data?.templates ?? []).some((t) =>
    (t.sandboxes ?? []).some((n) => (n.forks ?? []).length > 0)
  )
  if (!forked) return null
  return (
    <section className="grid min-w-0 content-start gap-2">
      <SectionHeader title="Family" icon={GitForkIcon} />
      <div className="rounded-(--radius) border bg-card p-2">
        <LineageTree environmentId={env} compact />
      </div>
    </section>
  )
}

/** The environment's teammates: what each is doing, and the sandboxes they own. */
function TeammateList({
  env,
  teammates,
  sandboxes,
  presence,
}: {
  env: string
  teammates: Teammate[]
  sandboxes: Sandbox[]
  presence: (teammateId: string) => Presence
}) {
  return (
    <section className="grid min-w-0 content-start gap-2">
      <SectionHeader
        title="Teammates"
        icon={UsersThreeIcon}
        count={teammates.length}
        actions={
          <Button
            variant="ghost"
            size="xs"
            render={<Link to="/e/$env/teammates" params={{ env }} />}
          >
            All teammates
            <ArrowRightIcon />
          </Button>
        }
      />
      {teammates.length === 0 ? (
        <p className="text-xs text-muted-foreground">No teammates yet.</p>
      ) : (
        <ul className="overflow-hidden rounded-(--radius) border bg-card">
          {teammates.map((t) => {
            const state = presence(t.id)
            const own = sandboxes.filter((sb) => sb.teammateId === t.id)
            return (
              <li key={t.id} className="border-b last:border-b-0">
                <Link
                  to="/e/$env/teammates/$id"
                  params={{ env, id: t.id }}
                  className="flex items-start gap-2.5 px-3 py-2 hover:bg-muted/40"
                >
                  <TeammateAvatar
                    name={t.name}
                    avatar={t.avatar}
                    className="size-7 rounded-[4px] text-[0.625rem]"
                  />
                  <span className="grid min-w-0 flex-1 gap-0.5">
                    <span className="flex min-w-0 items-center gap-2">
                      <span className="truncate font-medium">{t.name}</span>
                      <span
                        data-presence={state}
                        className={cn(
                          "flex shrink-0 items-center gap-1.5 text-[0.6875rem]",
                          state === "waiting"
                            ? "text-foreground"
                            : "text-muted-foreground"
                        )}
                      >
                        <PresenceDot presence={state} />
                        {presenceLabels[state]}
                      </span>
                    </span>
                    {t.role && (
                      <span className="truncate text-xs text-muted-foreground">
                        {t.role}
                      </span>
                    )}
                    {own.length > 0 && (
                      <span className="flex min-w-0 flex-wrap items-center gap-1 pt-0.5">
                        {own.slice(0, 3).map((sb) => (
                          <Ident key={sb.id}>{sb.name}</Ident>
                        ))}
                        {own.length > 3 && (
                          <span className="text-xs text-muted-foreground">
                            +{own.length - 3}
                          </span>
                        )}
                      </span>
                    )}
                  </span>
                </Link>
              </li>
            )
          })}
        </ul>
      )}
    </section>
  )
}
