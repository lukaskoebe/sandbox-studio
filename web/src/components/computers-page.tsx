import { useState } from "react"
import { Link, useNavigate } from "@tanstack/react-router"
import { DownloadSimpleIcon, PlusIcon, StackIcon } from "@phosphor-icons/react"
import { BaseMark } from "@/components/base-dialog"
import { ComputerMenu } from "@/components/computer/computer-menu"
import { CreateSandboxDialog } from "@/components/create-sandbox-dialog"
import { ImportSandboxDialog } from "@/components/import-sandbox-dialog"
import { Notice } from "@/components/notice"
import { DisksNotice } from "@/components/move-disks"
import { PageHeader } from "@/components/page-header"
import { RebaseAllDialog } from "@/components/rebase-sandbox-dialog"
import { formatMiB } from "@/components/sandbox-details"
import { phase, phaseDot } from "@/components/status-badge"
import { StatusDot } from "@/components/status-dot"
import { ThreadDot } from "@/components/thread-status"
import { TeammateAvatar } from "@/components/teammate-avatar"
import { Button } from "@/components/ui/button"
import {
  Empty,
  EmptyContent,
  EmptyDescription,
  EmptyHeader,
  EmptyTitle,
} from "@/components/ui/empty"
import { ScrollArea } from "@/components/ui/scroll-area"
import { Spinner } from "@/components/ui/spinner"
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "@/components/ui/table"
import { usePendingApprovals } from "@/hooks/use-pending-approvals"
import { $api, type Sandbox, type Teammate } from "@/lib/api/client"
import { forkTree } from "@/lib/computer"
import { chatTitle, type Chat } from "@/lib/chats"
import {
  agentTerminals,
  chatApprovals,
  chatStatus,
  statusLabels,
  type AgentTerminal,
} from "@/lib/threads"
import { cn, formatAge } from "@/lib/utils"

type Filter = "all" | "running" | "yours" | "bases"

const filters: { id: Filter; label: string }[] = [
  { id: "all", label: "All" },
  { id: "running", label: "Running" },
  { id: "yours", label: "Yours" },
  { id: "bases", label: "Bases" },
]

/**
 * Every computer of the environment in one table (docs/design/teammate-computer.md): forks under
 * their base, who uses each, the thread on it now, its disk and when it was last used. A row
 * opens the computer: a teammate's on its desk, the user's own on its page. Its ⋯ has what the
 * screen's has.
 */
export function ComputersPage({ env }: { env: string }) {
  const navigate = useNavigate()
  const [filter, setFilter] = useState<Filter>("all")
  const [creating, setCreating] = useState(false)
  const [importing, setImporting] = useState(false)
  const [rebasing, setRebasing] = useState(false)
  const sandboxes = $api.useQuery(
    "get",
    "/api/environments/{env}/sandboxes",
    { params: { path: { env } } },
    { refetchInterval: 3000 }
  )
  const teammates = $api.useQuery("get", "/api/environments/{env}/teammates", {
    params: { path: { env } },
  })
  const chats = $api.useQuery(
    "get",
    "/api/environments/{env}/chats",
    { params: { path: { env } } },
    { refetchInterval: 5000 }
  )
  const terminals = $api.useQuery("get", "/api/environments/{env}/terminals", {
    params: { path: { env } },
  })
  const pending = usePendingApprovals()
  const asks = (pending.data ?? []).filter((a) => a.environmentId === env)
  const all = sandboxes.data ?? []
  const allChats = chats.data ?? []
  const ownerOf = (sb: Sandbox) =>
    teammates.data?.find((t) => t.id === sb.teammateId)
  // Teammates' agents in terminals with no chat behind them are threads too.
  const agents = agentTerminals(
    terminals.data,
    new Set(all.filter((sb) => sb.teammateId).map((sb) => sb.id))
  )
  const matches: Record<Filter, (sb: Sandbox) => boolean> = {
    all: () => true,
    running: (sb) => sb.status === "running",
    yours: (sb) => !ownerOf(sb),
    bases: (sb) => !!sb.forkDefaults,
  }
  const shown = forkTree(all).filter(({ sandbox }) => matches[filter](sandbox))
  const outdated = all.filter((sb) => sb.baseOutdated)
  const open = (sb: Sandbox) => {
    const owner = ownerOf(sb)
    if (owner)
      void navigate({
        to: "/e/$env/teammates/$id",
        params: { env, id: owner.id },
        search: { computer: sb.id, tab: "terminal" },
      })
    else
      void navigate({
        to: "/e/$env/sandboxes/$id",
        params: { env, id: sb.id },
      })
  }

  return (
    <>
      <PageHeader>
        <h1 className="text-sm font-medium">Computers</h1>
        <div className="ml-auto flex gap-2">
          <Button
            size="sm"
            variant="outline"
            onClick={() => setImporting(true)}
          >
            <DownloadSimpleIcon />
            Import
          </Button>
          <Button size="sm" onClick={() => setCreating(true)}>
            <PlusIcon />
            New computer
          </Button>
        </div>
      </PageHeader>
      <ScrollArea
        className="min-h-0 flex-1"
        viewportClassName="space-y-4 p-4 pt-0"
      >
        {all.length > 0 && (
          <div
            role="group"
            aria-label="Show"
            className="flex flex-wrap items-center gap-1.5"
          >
            {filters.map((f) => (
              <button
                key={f.id}
                type="button"
                aria-pressed={filter === f.id}
                onClick={() => setFilter(f.id)}
                className={cn(
                  "inline-flex h-6 items-center gap-1.5 rounded-full border px-2.5 text-caption text-muted-foreground hover:text-foreground",
                  filter === f.id &&
                    "border-foreground/20 bg-muted font-medium text-foreground"
                )}
              >
                {f.label}
                <span className="font-mono text-meta">
                  {all.filter(matches[f.id]).length}
                </span>
              </button>
            ))}
          </div>
        )}
        <DisksNotice env={env} />
        {outdated.length > 0 && (
          <Notice
            aria-label="Base image"
            tone="waiting"
            icon={<StackIcon />}
            actions={
              <Button
                size="sm"
                variant="outline"
                onClick={() => setRebasing(true)}
              >
                Rebase all…
              </Button>
            }
          >
            {outdated.length === 1
              ? "1 computer runs"
              : `${outdated.length} computers run`}{" "}
            an older base image than new ones get.
          </Notice>
        )}
        {sandboxes.isPending ? (
          <Spinner className="mx-auto block" />
        ) : all.length === 0 ? (
          <Empty className="border">
            <EmptyHeader>
              <EmptyTitle>No computers yet</EmptyTitle>
              <EmptyDescription>
                A computer is a small VM where you or a teammate work, with its
                own terminal, browser and files.
              </EmptyDescription>
            </EmptyHeader>
            <EmptyContent>
              <Button size="sm" onClick={() => setCreating(true)}>
                <PlusIcon />
                New computer
              </Button>
            </EmptyContent>
          </Empty>
        ) : (
          <div className="rounded-lg ring-1 ring-foreground/10">
            <Table>
              <TableHeader>
                <TableRow>
                  <TableHead>Name</TableHead>
                  <TableHead>State</TableHead>
                  <TableHead>Used by</TableHead>
                  <TableHead className="max-md:hidden">Thread now</TableHead>
                  <TableHead className="max-lg:hidden">Disk</TableHead>
                  <TableHead className="max-lg:hidden">Active</TableHead>
                  <TableHead className="w-10">
                    <span className="sr-only">More</span>
                  </TableHead>
                </TableRow>
              </TableHeader>
              <TableBody>
                {shown.map(({ sandbox: sb, depth }) => (
                  <ComputerRow
                    key={sb.id}
                    env={env}
                    sandbox={sb}
                    depth={filter === "all" ? depth : 0}
                    owner={ownerOf(sb)}
                    chats={allChats.filter((c) => c.sandboxId === sb.id)}
                    terminal={agents.find((t) => t.sandboxId === sb.id)}
                    waiting={(c) => chatApprovals(c, asks).length}
                    onOpen={() => open(sb)}
                    onDetails={() =>
                      void navigate(
                        ownerOf(sb)
                          ? {
                              to: "/e/$env/teammates/$id",
                              params: { env, id: sb.teammateId! },
                              search: { computer: sb.id, tab: "details" },
                            }
                          : {
                              to: "/e/$env/sandboxes/$id",
                              params: { env, id: sb.id },
                              search: { tab: "details" },
                            }
                      )
                    }
                  />
                ))}
              </TableBody>
            </Table>
          </div>
        )}
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
      <RebaseAllDialog
        env={env}
        sandboxes={outdated}
        open={rebasing}
        onOpenChange={setRebasing}
      />
    </>
  )
}

function ComputerRow({
  env,
  sandbox: sb,
  depth,
  owner,
  chats,
  terminal,
  waiting,
  onOpen,
  onDetails,
}: {
  env: string
  sandbox: Sandbox
  depth: number
  owner?: Teammate
  /** The chats on this computer. */
  chats: Chat[]
  /** The owner's agent in a terminal here, with no chat behind it. */
  terminal?: AgentTerminal
  waiting: (c: Chat) => number
  onOpen: () => void
  onDetails: () => void
}) {
  const p = phaseDot(phase(sb))
  const latest = [...chats].sort((a, b) =>
    b.updatedAt.localeCompare(a.updatedAt)
  )
  // A chat that runs or waits comes first, then the agent in a terminal, then the latest chat.
  const live = latest.find((c) => c.state === "running" || waiting(c) > 0)
  const now = live ?? (terminal ? undefined : latest.at(0))
  const status = now ? chatStatus(now, waiting(now)) : undefined
  const working =
    status === "working" || (!now && terminal?.state === "working")
  const disk = sb.homeMiB ?? sb.workspaceMiB
  return (
    <TableRow
      data-testid="computer-row"
      className="cursor-pointer"
      onClick={(e) => {
        // The ⋯ menu and its dialogs live inside the row.
        if ((e.target as HTMLElement).closest("button, a, [role=menu]")) return
        onOpen()
      }}
    >
      <TableCell>
        <span
          className="flex min-w-0 items-center gap-2"
          style={{ paddingLeft: `${depth * 1.25}rem` }}
        >
          {depth > 0 && (
            <span aria-hidden className="text-muted-foreground/60">
              └
            </span>
          )}
          <StatusDot {...p} compact />
          <Link
            to={owner ? "/e/$env/teammates/$id" : "/e/$env/sandboxes/$id"}
            params={{ env, id: owner ? owner.id : sb.id }}
            search={owner ? { computer: sb.id, tab: "terminal" } : {}}
            className="min-w-0 truncate font-mono font-medium underline-offset-4 hover:underline"
          >
            {sb.name}
          </Link>
          {sb.forkDefaults && <BaseMark />}
          {sb.forkOf && depth > 0 && (
            <span className="text-caption text-muted-foreground">fork</span>
          )}
        </span>
      </TableCell>
      <TableCell className="text-muted-foreground">{p.label}</TableCell>
      <TableCell>
        {owner ? (
          <span className="flex items-center gap-1.5">
            <TeammateAvatar
              name={owner.name}
              avatar={owner.avatar}
              className="size-4 text-[0.5rem]"
            />
            {owner.name}
          </span>
        ) : (
          <span className="text-muted-foreground">you</span>
        )}
      </TableCell>
      <TableCell className="max-w-72 max-md:hidden">
        {now && status ? (
          <span className="flex min-w-0 items-center gap-2">
            <ThreadDot status={status} />
            <span className="truncate">{chatTitle(now)}</span>
            <span className="shrink-0 text-muted-foreground">
              · {statusLabels[status]}
            </span>
          </span>
        ) : terminal ? (
          <span className="flex min-w-0 items-center gap-2">
            <ThreadDot status="terminal" />
            <span className="truncate">
              {terminal.title || `${owner?.name ?? "The agent"} in a terminal`}
            </span>
            <span className="shrink-0 text-muted-foreground">
              · {terminal.state === "working" ? "working" : "in the terminal"}
            </span>
          </span>
        ) : (
          <span className="text-muted-foreground">none</span>
        )}
      </TableCell>
      <TableCell className="font-mono text-muted-foreground max-lg:hidden">
        {disk ? formatMiB(disk) : "—"}
        {sb.lowDisks?.length ? (
          <span className="ml-1.5 font-sans text-(--st-waiting-fg)">
            nearly full
          </span>
        ) : null}
      </TableCell>
      <TableCell className="text-muted-foreground max-lg:hidden">
        {working
          ? "now"
          : latest.length
            ? formatAge(latest[0].updatedAt)
            : sb.startedAt && sb.status === "running"
              ? formatAge(sb.startedAt)
              : "—"}
      </TableCell>
      <TableCell className="text-right">
        <ComputerMenu
          env={env}
          sandbox={sb}
          owner={owner}
          onDetails={onDetails}
        />
      </TableCell>
    </TableRow>
  )
}
