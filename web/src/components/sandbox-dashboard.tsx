import { useEffect, useState, type ReactNode } from "react"
import { Link } from "@tanstack/react-router"
import {
  ArrowSquareOutIcon,
  BroadcastIcon,
  ChatCircleDotsIcon,
  CpuIcon,
  GitForkIcon,
  PlugsConnectedIcon,
  PlusIcon,
  RobotIcon,
  TerminalWindowIcon,
  type Icon,
} from "@phosphor-icons/react"
import { CheckpointsSection } from "@/components/checkpoints-section"
import { RecentConnections } from "@/components/connections-panel"
import { Ident } from "@/components/ident"
import { LineageTimeline } from "@/components/lineage-timeline"
import { ResizeSandboxDialog } from "@/components/resize-sandbox-dialog"
import { StartButton, StopButton } from "@/components/sandbox-lifecycle"
import { DeleteSandboxButton } from "@/components/sandbox-options-menu"
import { useSandboxPage } from "@/components/sandbox-page"
import { SandboxProgress } from "@/components/sandbox-progress"
import { SectionHeader } from "@/components/section-header"
import { phase, StatusBadge } from "@/components/status-badge"
import { SandboxOwner } from "@/components/teammate-avatar"
import { Button } from "@/components/ui/button"
import type { Area } from "@/lib/accents"
import { $api, isReady, type Sandbox } from "@/lib/api/client"
import { chatStateLabels, chatTitle } from "@/lib/chats"
import { harnessLabels } from "@/lib/teammates"
import { onStudioMachine, openPreviewTab } from "@/lib/previews"
import { formatAge, formatUptime } from "@/lib/utils"

/**
 * A sandbox at a glance, laid out like Home: a band of facts with the lifecycle actions, then
 * its chats, terminals, history and checkpoints, with ports and connections at the side. Each
 * list opens its item in the workspace.
 */
export function SandboxDashboard() {
  const { env, sandbox: sb } = useSandboxPage()
  // A create or fork has no guest until it succeeds; a failed one is cleared by deleting it.
  const building = sb.status === "creating" || sb.status === "failed"
  return (
    <div className="min-h-0 flex-1 overflow-y-auto text-[0.8125rem]">
      <Overview env={env} sandbox={sb} />
      <div className="grid content-start gap-6 p-3 md:p-4 xl:grid-cols-[minmax(0,1fr)_22rem]">
        <div className="grid min-w-0 content-start gap-6">
          {building && (
            <SandboxProgress
              sandbox={sb}
              action={
                sb.status === "failed" ? (
                  <DeleteSandboxButton env={env} sandbox={sb} />
                ) : undefined
              }
            />
          )}
          {sb.teammateId && (
            <Section
              title="Chats"
              icon={ChatCircleDotsIcon}
              area="chat"
              actions={
                <Button
                  variant="ghost"
                  size="xs"
                  render={
                    <Link
                      to="/e/$env/chats/new"
                      params={{ env }}
                      search={{ sandbox: sb.id }}
                    />
                  }
                >
                  <PlusIcon />
                  New chat
                </Button>
              }
            >
              <Chats env={env} sandbox={sb} />
            </Section>
          )}
          <Section title="Terminals" icon={TerminalWindowIcon}>
            <Terminals env={env} sandbox={sb} />
          </Section>
          <Section title="History" icon={GitForkIcon}>
            <LineageTimeline environmentId={env} sandboxId={sb.id} />
          </Section>
          {!building && <CheckpointsSection env={env} sandbox={sb} />}
        </div>
        <div className="grid min-w-0 content-start gap-6">
          <Section title="Ports" icon={BroadcastIcon}>
            <Ports env={env} sandbox={sb} />
          </Section>
          <Section
            title="Recent connections"
            icon={PlugsConnectedIcon}
            area="network"
            actions={
              <Button
                variant="ghost"
                size="xs"
                render={
                  <Link
                    to="/e/$env/sandboxes/$id/work"
                    params={{ env, id: sb.id }}
                    search={{ panel: "connections" }}
                  />
                }
              >
                All
              </Button>
            }
          >
            <RecentConnections env={env} id={sb.id} />
          </Section>
        </div>
      </div>
    </div>
  )
}

/**
 * The band under the header: status, owner, template, resources and age on Home's grid paper,
 * with start, stop, resize and the workspace at its end. The ID is in the header.
 */
function Overview({ env, sandbox: sb }: { env: string; sandbox: Sandbox }) {
  const p = phase(sb)
  // Uptime is relative to now, so the clock is read again every minute.
  const [now, setNow] = useState(Date.now)
  const [resizing, setResizing] = useState(false)
  useEffect(() => {
    const timer = setInterval(() => setNow(Date.now()), 60_000)
    return () => clearInterval(timer)
  }, [])
  const note =
    p === "stopped"
      ? "Your workspace, home directory and Docker images are kept."
      : p === "suspended"
        ? "Suspended: resume it to continue where it left off."
        : p === "failed"
          ? "The sandbox stopped unexpectedly. Your workspace is kept. Start it again to continue."
          : p === "ready" && sb.startedAt
            ? formatUptime(sb.startedAt, now)
            : undefined
  return (
    <section
      aria-labelledby="overview-heading"
      className="flex flex-wrap items-stretch border-b bg-[linear-gradient(var(--wb-grid)_1px,transparent_1px),linear-gradient(90deg,var(--wb-grid)_1px,transparent_1px)] bg-size-[12px_12px]"
    >
      <h2 id="overview-heading" className="sr-only">
        Overview
      </h2>
      {/* Every fact draws its right and bottom rule; the list hides the ones at its edges. */}
      <div className="min-w-0 flex-[1_1_36rem] overflow-hidden">
        <dl className="-mr-px -mb-px grid grid-cols-2 sm:grid-cols-3 lg:grid-cols-5">
          <Fact label="Status" note={note}>
            <StatusBadge sandbox={sb} />
          </Fact>
          <Fact label="Owner">
            {sb.teammateId ? (
              <SandboxOwner env={env} teammateId={sb.teammateId} />
            ) : (
              <span className="text-muted-foreground">Unowned</span>
            )}
          </Fact>
          <Fact label="Template" title={sb.templateId || undefined}>
            <span className="font-mono">
              {sb.templateName ?? sb.templateId ?? "None"}
            </span>
          </Fact>
          <Fact
            label="Resources"
            title={`${sb.cpus} CPUs, ${formatMiB(sb.memoryMiB)} memory, ${formatMiB(sb.workspaceMiB)} workspace`}
          >
            <span>
              {sb.cpus} CPU · {formatMiB(sb.memoryMiB)} ·{" "}
              {formatMiB(sb.workspaceMiB)}
            </span>
          </Fact>
          <Fact label="Created" title={new Date(sb.createdAt).toLocaleString()}>
            <time dateTime={sb.createdAt}>{formatAge(sb.createdAt)}</time>
          </Fact>
        </dl>
      </div>
      <div className="flex flex-wrap items-center gap-2 px-4 py-2.5 max-md:w-full max-md:border-t md:border-l">
        {(p === "stopped" || p === "failed") && (
          <StartButton env={env} sandbox={sb} />
        )}
        {(p === "ready" || p === "booting" || p === "suspended") && (
          <StopButton env={env} sandbox={sb} />
        )}
        {(p === "ready" || p === "stopped" || p === "suspended") && (
          <Button variant="outline" onClick={() => setResizing(true)}>
            <CpuIcon />
            Resources
          </Button>
        )}
        <ResizeSandboxDialog
          env={env}
          sandbox={sb}
          open={resizing}
          onOpenChange={setResizing}
        />
        {p !== "creating" && (
          <Button
            render={
              <Link
                to="/e/$env/sandboxes/$id/work"
                params={{ env, id: sb.id }}
              />
            }
          >
            <TerminalWindowIcon />
            Open workspace
          </Button>
        )}
      </div>
    </section>
  )
}

/** One fact: a mono label, its value, and an optional muted note under it. */
function Fact({
  label,
  title,
  note,
  children,
}: {
  label: string
  title?: string
  note?: string
  children: ReactNode
}) {
  return (
    <div className="min-w-0 border-r border-b px-4 py-2.5" title={title}>
      <dt className="font-mono text-[0.625rem] tracking-wider text-muted-foreground uppercase">
        {label}
      </dt>
      <dd className="mt-1 flex h-5 min-w-0 items-center truncate font-medium">
        {children}
      </dd>
      {note && (
        <dd className="truncate text-xs text-muted-foreground" title={note}>
          {note}
        </dd>
      )}
    </div>
  )
}

/** A dashboard section: its header, then its content. Its title names the region. */
function Section({
  title,
  icon,
  area,
  actions,
  children,
}: {
  title: string
  icon: Icon
  area?: Area
  actions?: ReactNode
  children: ReactNode
}) {
  return (
    <section aria-label={title} className="grid min-w-0 content-start gap-2">
      <SectionHeader title={title} icon={icon} area={area} actions={actions} />
      {children}
    </section>
  )
}

function Ports({ env, sandbox: sb }: { env: string; sandbox: Sandbox }) {
  const ports = $api.useQuery(
    "get",
    "/api/environments/{env}/sandboxes/{id}/ports",
    { params: { path: { env, id: sb.id } } },
    { refetchInterval: 3000, enabled: isReady(sb) }
  )
  if (!isReady(sb))
    return (
      <p className="text-xs text-muted-foreground">
        Listening ports show up here while the sandbox runs.
      </p>
    )
  const list = ports.data ?? []
  if (list.length === 0)
    return (
      <p className="text-xs text-muted-foreground">
        Start a server in a terminal, e.g. on port 3000, and it shows up here.
      </p>
    )
  return (
    <>
      <ul className="overflow-hidden rounded-(--radius) border bg-card">
        {list.map((p) => (
          <li
            key={p.port}
            className="flex h-(--wb-row) items-center gap-2 border-b px-3 text-xs last:border-b-0"
          >
            <Ident>{`:${p.port}`}</Ident>
            <Button
              variant="ghost"
              size="xs"
              className="ml-auto"
              onClick={() => void openPreviewTab(env, sb.id, p.port)}
            >
              Open
              <ArrowSquareOutIcon />
            </Button>
          </li>
        ))}
      </ul>
      {!onStudioMachine && (
        <p className="text-[0.625rem] text-muted-foreground">
          Previews open on the machine running Studio.
        </p>
      )}
    </>
  )
}

function Chats({ env, sandbox: sb }: { env: string; sandbox: Sandbox }) {
  const chats = $api.useQuery(
    "get",
    "/api/environments/{env}/sandboxes/{id}/chats",
    { params: { path: { env, id: sb.id } } }
  )
  const list = [...(chats.data ?? [])]
    .sort((a, b) => Date.parse(b.updatedAt) - Date.parse(a.updatedAt))
    .slice(0, 5)
  if (list.length === 0)
    return (
      <p className="text-xs text-muted-foreground">
        No chats yet. A chat with the teammate who owns this sandbox shows up
        here.
      </p>
    )
  return (
    <ul className="overflow-hidden rounded-(--radius) border bg-card">
      {list.map((c) => (
        <li
          key={c.id}
          className="flex h-(--wb-row) items-center gap-2 border-b px-3 text-xs last:border-b-0"
        >
          <ChatCircleDotsIcon className="shrink-0 text-(--acc-chat)" />
          <span className="min-w-0 flex-1 truncate">{chatTitle(c)}</span>
          <span
            className="shrink-0 font-mono text-[0.6875rem] text-muted-foreground"
            title={chatStateLabels[c.state]}
          >
            {c.state === "running" ? "Working" : formatAge(c.updatedAt)}
          </span>
          <Button
            variant="ghost"
            size="xs"
            render={
              <Link
                to="/e/$env/sandboxes/$id/work"
                params={{ env, id: sb.id }}
                search={{ chat: c.id }}
              />
            }
          >
            Open
          </Button>
        </li>
      ))}
    </ul>
  )
}

function Terminals({ env, sandbox: sb }: { env: string; sandbox: Sandbox }) {
  const sessions = $api.useQuery(
    "get",
    "/api/environments/{env}/sandboxes/{id}/terminals",
    { params: { path: { env, id: sb.id } } },
    { refetchInterval: 5000, enabled: isReady(sb) }
  )
  if (!isReady(sb))
    return (
      <p className="text-xs text-muted-foreground">
        Terminals open once the sandbox is up.
      </p>
    )
  const list = [...(sessions.data ?? [])].sort((a, b) => a.created - b.created)
  if (list.length === 0)
    return (
      <p className="text-xs text-muted-foreground">
        No terminals open. The workspace opens one when you visit it.
      </p>
    )
  return (
    <ul className="overflow-hidden rounded-(--radius) border bg-card">
      {list.map((s) => (
        <li
          key={s.name}
          className="flex h-(--wb-row) items-center gap-2 border-b px-3 text-xs last:border-b-0"
        >
          {s.harness ? (
            <RobotIcon className="shrink-0 text-muted-foreground" />
          ) : (
            <TerminalWindowIcon className="shrink-0 text-muted-foreground" />
          )}
          <span className="min-w-0 flex-1 truncate font-mono">{s.name}</span>
          {s.harness && (
            <span className="shrink-0 text-muted-foreground">
              {s.harness in harnessLabels
                ? harnessLabels[s.harness as keyof typeof harnessLabels]
                : s.harness}
            </span>
          )}
          <Button
            variant="ghost"
            size="xs"
            render={
              <Link
                to="/e/$env/sandboxes/$id/work"
                params={{ env, id: sb.id }}
                search={{ terminal: s.name }}
              />
            }
          >
            Open
          </Button>
        </li>
      ))}
    </ul>
  )
}

function formatMiB(mib: number) {
  return mib >= 1024
    ? `${(mib / 1024).toFixed(1).replace(/\.0$/, "")} GiB`
    : `${mib} MiB`
}
