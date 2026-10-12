import { useState } from "react"
import { Link, createFileRoute } from "@tanstack/react-router"
import {
  CubeIcon,
  GitForkIcon,
  PlusIcon,
  ShieldCheckIcon,
  UploadSimpleIcon,
  UsersThreeIcon,
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
import { CreateSandboxDialog } from "@/components/create-sandbox-dialog"
import { ImportSandboxDialog } from "@/components/import-sandbox-dialog"
import { Ident } from "@/components/ident"
import { KindChip } from "@/components/kind-chip"
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
import { approvalKind, type ApprovalKind } from "@/lib/approval-kinds"
import { shortTemplate } from "@/lib/lineage"
import { cn, formatAge } from "@/lib/utils"

export const Route = createFileRoute("/e/$env/")({ component: Overview })

/** The sandbox table's columns from the large breakpoint: name, owner, template, status, ports, waiting. */
const tableColumns =
  "lg:grid-cols-[minmax(0,1.5fr)_minmax(0,1fr)_minmax(0,1fr)_8.5rem_minmax(0,1fr)_3.5rem]"

/** Each cell's column and row from the large breakpoint, placed explicitly so a row stays on one line. */
const cellAt = {
  name: "lg:col-start-1 lg:row-start-1",
  owner: "lg:col-start-2 lg:row-start-1",
  template: "lg:col-start-3 lg:row-start-1",
  status: "lg:col-start-4 lg:row-start-1",
  ports: "lg:col-start-5 lg:row-start-1",
  waiting: "lg:col-start-6 lg:row-start-1",
}

/**
 * The environment at a glance: the numbers, the sandboxes as a table with what waits on each,
 * their family tree, and on the right the approvals and the teammates.
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
  const owners = new Map(people.map((p) => [p.id, p.name]))
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
      <div className="min-h-0 flex-1 overflow-y-auto">
        <Stats
          running={list.filter(isReady).length}
          approvals={waiting.length}
          working={people.filter((p) => presence(p.id) === "working").length}
        />
        <div className="grid gap-6 p-3 md:p-4 xl:grid-cols-[minmax(0,1fr)_22rem]">
          <div className="grid min-w-0 content-start gap-6">
            <SetupCard env={env} />
            {sandboxes.isPending ? (
              <Skeleton className="h-40" />
            ) : list.length ? (
              <SandboxTable
                env={env}
                sandboxes={list}
                owners={owners}
                waiting={waitingBySandbox}
              />
            ) : (
              <Empty className="rounded-(--radius) border">
                <EmptyHeader>
                  <EmptyMedia variant="icon">
                    <CubeIcon />
                  </EmptyMedia>
                  <EmptyTitle>No sandboxes yet</EmptyTitle>
                  <EmptyDescription>
                    A sandbox is a Linux VM with Docker and a persistent
                    workspace, where teammates and you work on a project.{" "}
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
                  <Button onClick={() => setCreating(true)}>
                    <PlusIcon />
                    New sandbox
                  </Button>
                </EmptyContent>
              </Empty>
            )}
            {list.length > 0 && (
              <section className="grid min-w-0 content-start gap-2">
                <SectionHeader title="Family" icon={GitForkIcon} />
                <div className="rounded-(--radius) border bg-card p-2">
                  <LineageTree environmentId={env} compact />
                </div>
              </section>
            )}
          </div>
          <div className="grid min-w-0 content-start gap-6">
            <ApprovalsSummary approvals={waiting} />
            <TeammateList teammates={people} presence={presence} />
          </div>
        </div>
      </div>
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
  running,
  approvals,
  working,
}: {
  running: number
  approvals: number
  working: number
}) {
  const items = [
    { label: "Sandboxes", value: running, rest: "running" },
    { label: "Approvals", value: approvals, rest: "waiting" },
    { label: "Teammates", value: working, rest: "working" },
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

/**
 * The sandboxes as a table. Each row is a link to the sandbox. Below the large breakpoint the
 * columns stack: the name and the approvals on the first line, the owner, status and ports on
 * the second.
 */
function SandboxTable({
  env,
  sandboxes,
  owners,
  waiting,
}: {
  env: string
  sandboxes: Sandbox[]
  owners: Map<string, string>
  waiting: Map<string, number>
}) {
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
          <span className="text-right">Waiting</span>
        </div>
        {sandboxes.map((sb) => {
          const owner = sb.teammateId ? owners.get(sb.teammateId) : undefined
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
                {owner ? (
                  <span
                    className={cn(
                      "flex min-w-0 items-center gap-1.5 text-muted-foreground",
                      cellAt.owner
                    )}
                  >
                    <TeammateAvatar
                      name={owner}
                      className="size-4 rounded-[3px] text-[0.5rem]"
                    />
                    <span className="truncate">{owner}</span>
                  </span>
                ) : (
                  <span
                    className={cn("text-muted-foreground/60", cellAt.owner)}
                  >
                    No owner
                  </span>
                )}
                <span className={cn("max-lg:hidden", cellAt.template)}>
                  <Ident>
                    {sb.templateName ?? shortTemplate(sb.templateId)}
                  </Ident>
                </span>
                <StatusDot
                  {...phaseDot(phase(sb))}
                  className={cn("shrink-0", cellAt.status)}
                />
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
  const list: PreviewPort[] = ports.data ?? []
  if (list.length === 0)
    return (
      <span className={cn("text-muted-foreground/60", className)}>None</span>
    )
  return (
    <span className={cn("flex min-w-0 flex-wrap gap-1", className)}>
      {list.map((p) => (
        <Ident key={p.port}>{`:${p.port}`}</Ident>
      ))}
    </span>
  )
}

/**
 * The approvals waiting for you, counted by kind. The stack at the bottom right holds the
 * requests themselves, with their Allow and Deny buttons.
 */
function ApprovalsSummary({ approvals }: { approvals: Approval[] }) {
  const groups = new Map<
    string,
    { kind: ApprovalKind; count: number; oldest: string }
  >()
  for (const a of approvals) {
    const kind = approvalKind(a)
    const group = groups.get(kind.title)
    if (group) {
      group.count++
      if (a.createdAt < group.oldest) group.oldest = a.createdAt
    } else {
      groups.set(kind.title, { kind, count: 1, oldest: a.createdAt })
    }
  }
  return (
    <section className="grid min-w-0 content-start gap-2">
      <SectionHeader
        title="Approvals"
        icon={ShieldCheckIcon}
        count={approvals.length}
      />
      {groups.size === 0 ? (
        <p className="text-xs text-muted-foreground">
          Nothing is waiting for you.
        </p>
      ) : (
        <ul className="overflow-hidden rounded-(--radius) border bg-card">
          {[...groups.values()].map((g) => (
            <li
              key={g.kind.title}
              className="flex h-(--wb-row) items-center gap-2 border-b px-3 last:border-b-0"
            >
              <KindChip area={g.kind.area} icon={g.kind.icon}>
                {g.kind.title}
              </KindChip>
              <span
                className="ml-auto font-mono text-[0.6875rem] text-muted-foreground"
                title="Age of the oldest request"
              >
                {formatAge(g.oldest)}
              </span>
              <WaitingCount count={g.count} />
            </li>
          ))}
        </ul>
      )}
    </section>
  )
}

/** The environment's teammates, each with what they are doing. */
function TeammateList({
  teammates,
  presence,
}: {
  teammates: Teammate[]
  presence: (teammateId: string) => Presence
}) {
  return (
    <section className="grid min-w-0 content-start gap-2">
      <SectionHeader
        title="Teammates"
        icon={UsersThreeIcon}
        count={teammates.length}
      />
      <ul className="overflow-hidden rounded-(--radius) border bg-card">
        {teammates.map((t) => {
          const state = presence(t.id)
          return (
            <li
              key={t.id}
              className="flex h-(--wb-row) items-center gap-2 border-b px-3 last:border-b-0"
            >
              <TeammateAvatar
                name={t.name}
                className="size-4 rounded-[3px] text-[0.5rem]"
              />
              <span className="truncate font-medium">{t.name}</span>
              <span className="min-w-0 flex-1 truncate text-muted-foreground">
                {t.role}
              </span>
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
            </li>
          )
        })}
      </ul>
    </section>
  )
}
