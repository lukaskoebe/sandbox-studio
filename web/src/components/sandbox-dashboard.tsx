import { useEffect, useState, type ReactNode } from "react"
import { Link } from "@tanstack/react-router"
import {
  ArrowSquareOutIcon,
  BroadcastIcon,
  ChatCircleDotsIcon,
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
import { StartButton, StopButton } from "@/components/sandbox-lifecycle"
import { DeleteSandboxButton } from "@/components/sandbox-options-menu"
import { useSandboxPage } from "@/components/sandbox-page"
import { SandboxProgress } from "@/components/sandbox-progress"
import { SectionHeader } from "@/components/section-header"
import { phase } from "@/components/status-badge"
import { SandboxOwner } from "@/components/teammate-avatar"
import { Button } from "@/components/ui/button"
import type { Area } from "@/lib/accents"
import { $api, isReady, type Sandbox } from "@/lib/api/client"
import { chatStateLabels, chatTitle } from "@/lib/chats"
import { harnessLabels } from "@/lib/teammates"
import { onStudioMachine, openPreviewTab } from "@/lib/previews"
import { formatAge, formatUptime } from "@/lib/utils"

/**
 * A sandbox at a glance: its facts, then its ports, connections, history, checkpoints, chats
 * and terminals as lists. Each list opens its item in the workspace.
 */
export function SandboxDashboard() {
  const { env, sandbox: sb } = useSandboxPage()
  // A create or fork has no guest until it succeeds; a failed one is cleared by deleting it.
  const building = sb.status === "creating" || sb.status === "failed"
  return (
    <div className="min-h-0 flex-1 overflow-y-auto">
      <div className="grid w-full content-start gap-6 p-3 md:p-4">
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
        <Overview env={env} sandbox={sb} />
        <div className="grid gap-6 xl:grid-cols-2">
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
                All connections
              </Button>
            }
          >
            <RecentConnections env={env} id={sb.id} />
          </Section>
        </div>
        <Section title="History" icon={GitForkIcon}>
          <LineageTimeline environmentId={env} sandboxId={sb.id} />
        </Section>
        {!building && <CheckpointsSection env={env} sandbox={sb} />}
        <div className="grid gap-6 xl:grid-cols-2">
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
        </div>
      </div>
    </div>
  )
}

/** The status, owner, template, resources, age and ID of a sandbox, with its start and stop. */
function Overview({ env, sandbox: sb }: { env: string; sandbox: Sandbox }) {
  const p = phase(sb)
  // Uptime is relative to now, so the clock is read again every minute.
  const [now, setNow] = useState(Date.now)
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
          : undefined
  return (
    <section aria-labelledby="overview-heading" className="grid gap-3">
      <h2 id="overview-heading" className="sr-only">
        Overview
      </h2>
      <dl className="grid grid-cols-2 border-y sm:grid-cols-3 xl:grid-cols-5">
        <Fact label="Owner">
          {sb.teammateId ? (
            <SandboxOwner env={env} teammateId={sb.teammateId} />
          ) : (
            "Unowned"
          )}
        </Fact>
        <Fact label="Template">
          <span className="font-mono">
            {sb.templateName ?? sb.templateId ?? "None"}
          </span>
        </Fact>
        <Fact label="Resources">
          <span
            title={`${sb.cpus} CPUs, ${formatMiB(sb.memoryMiB)} memory, ${formatMiB(sb.workspaceMiB)} workspace`}
          >
            {sb.cpus} CPUs · {formatMiB(sb.memoryMiB)} ·{" "}
            {formatMiB(sb.workspaceMiB)} workspace
          </span>
        </Fact>
        {p === "ready" && sb.startedAt && (
          <Fact label="Status">{formatUptime(sb.startedAt, now)}</Fact>
        )}
        <Fact label="Created">
          <time
            dateTime={sb.createdAt}
            title={new Date(sb.createdAt).toLocaleString()}
          >
            {formatAge(sb.createdAt)}
          </time>
        </Fact>
        <Fact label="ID">
          <Ident copy>{sb.id}</Ident>
        </Fact>
      </dl>
      <div className="flex flex-wrap items-center gap-2">
        {note && (
          <p className="min-w-0 flex-1 text-xs text-muted-foreground">{note}</p>
        )}
        <div className="ml-auto flex flex-wrap items-center gap-2">
          {(p === "stopped" || p === "failed") && (
            <StartButton env={env} sandbox={sb} />
          )}
          {(p === "ready" || p === "booting" || p === "suspended") && (
            <StopButton env={env} sandbox={sb} />
          )}
          {p !== "creating" && (
            <Button
              variant="outline"
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
      </div>
    </section>
  )
}

function Fact({ label, children }: { label: string; children: ReactNode }) {
  return (
    <div className="min-w-0 border-r px-4 py-2.5 last:border-r-0">
      <dt className="font-mono text-[0.625rem] tracking-wider text-muted-foreground uppercase">
        {label}
      </dt>
      <dd className="mt-1 min-w-0 truncate">{children}</dd>
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
  return mib >= 1024 ? `${(mib / 1024).toFixed(1)} GiB` : `${mib} MiB`
}
