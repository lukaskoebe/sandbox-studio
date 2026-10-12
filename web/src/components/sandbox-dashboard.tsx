import type { ReactNode } from "react"
import { Link } from "@tanstack/react-router"
import {
  ArrowSquareOutIcon,
  ChatCircleDotsIcon,
  PlusIcon,
  RobotIcon,
  TerminalWindowIcon,
} from "@phosphor-icons/react"
import { CheckpointsSection } from "@/components/checkpoints-section"
import { RecentConnections } from "@/components/connections-panel"
import { StartButton, StopButton } from "@/components/sandbox-lifecycle"
import { DeleteSandboxButton } from "@/components/sandbox-options-menu"
import { useSandboxPage } from "@/components/sandbox-page"
import { SandboxProgress } from "@/components/sandbox-progress"
import { phase, StatusBadge } from "@/components/status-badge"
import { SandboxOwner } from "@/components/teammate-avatar"
import { Button } from "@/components/ui/button"
import { $api, isReady, type Sandbox } from "@/lib/api/client"
import { chatStateLabels, chatTitle } from "@/lib/chats"
import { harnessLabels } from "@/lib/teammates"
import { onStudioMachine, openPreviewTab } from "@/lib/previews"
import { formatAge } from "@/lib/utils"

/**
 * A sandbox at a glance: its state and what it runs on, then its ports, connections,
 * checkpoints and the chats and terminals it has. Each list opens its item in the workspace.
 */
export function SandboxDashboard() {
  const { env, sandbox: sb } = useSandboxPage()
  // A create or fork has no guest until it succeeds; a failed one is cleared by deleting it.
  const building = sb.status === "creating" || sb.status === "failed"
  return (
    <div className="min-h-0 flex-1 overflow-y-auto">
      <div className="mx-auto grid w-full max-w-5xl gap-4 p-4 sm:p-6">
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
        <div className="grid gap-4 lg:grid-cols-2">
          <Section title="Ports">
            <Ports env={env} sandbox={sb} />
          </Section>
          <Section
            title="Recent connections"
            action={
              <Button
                variant="ghost"
                size="sm"
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
        {/* lineage */}
        {!building && (
          <div className="rounded-lg border p-4">
            <CheckpointsSection env={env} sandbox={sb} />
          </div>
        )}
        <div className="grid gap-4 lg:grid-cols-2">
          {sb.teammateId && (
            <Section
              title="Chats"
              action={
                <Button
                  variant="ghost"
                  size="sm"
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
          <Section title="Terminals">
            <Terminals env={env} sandbox={sb} />
          </Section>
        </div>
      </div>
    </div>
  )
}

/** The status, owner, template, resources and age of a sandbox, with its start and stop. */
function Overview({ env, sandbox: sb }: { env: string; sandbox: Sandbox }) {
  const p = phase(sb)
  const note =
    p === "stopped"
      ? "Your workspace, home directory and Docker images are kept."
      : p === "suspended"
        ? "Suspended: resume it to continue where it left off."
        : p === "failed"
          ? "The sandbox stopped unexpectedly. Your workspace is kept. Start it again to continue."
          : undefined
  return (
    <section
      aria-labelledby="overview-heading"
      className="grid gap-4 rounded-lg border p-4"
    >
      <div className="flex flex-wrap items-center gap-2">
        <h2 id="overview-heading" className="sr-only">
          Overview
        </h2>
        <StatusBadge sandbox={sb} />
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
      <dl className="grid grid-cols-2 gap-x-6 gap-y-3 text-xs sm:grid-cols-4">
        <Fact label="Owner">
          {sb.teammateId ? (
            <SandboxOwner env={env} teammateId={sb.teammateId} />
          ) : (
            "Unowned"
          )}
        </Fact>
        <Fact label="Template">
          <span className="font-mono">{sb.templateId ?? "None"}</span>
        </Fact>
        <Fact label="Resources">
          {sb.cpus} CPUs, {formatMiB(sb.memoryMiB)} memory,{" "}
          {formatMiB(sb.workspaceMiB)} workspace
        </Fact>
        <Fact label="Created">
          <time
            dateTime={sb.createdAt}
            title={new Date(sb.createdAt).toLocaleString()}
          >
            {formatAge(sb.createdAt)}
          </time>
        </Fact>
      </dl>
      {note && <p className="text-xs text-muted-foreground">{note}</p>}
    </section>
  )
}

function Fact({ label, children }: { label: string; children: ReactNode }) {
  return (
    <div className="grid min-w-0 content-start gap-1">
      <dt className="text-muted-foreground">{label}</dt>
      <dd className="min-w-0 truncate font-medium">{children}</dd>
    </div>
  )
}

function Section({
  title,
  action,
  children,
}: {
  title: string
  action?: ReactNode
  children: ReactNode
}) {
  return (
    <section
      aria-label={title}
      className="grid content-start gap-2 rounded-lg border p-4"
    >
      <div className="flex min-h-7 items-center gap-2">
        <h2 className="text-sm font-medium">{title}</h2>
        {action && <div className="ml-auto">{action}</div>}
      </div>
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
      <ul className="divide-y">
        {list.map((p) => (
          <li key={p.port} className="flex items-center gap-2 py-1.5 text-xs">
            <span className="font-mono">:{p.port}</span>
            <Button
              variant="ghost"
              size="sm"
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
    <ul className="divide-y">
      {list.map((c) => (
        <li key={c.id} className="flex items-center gap-2 py-1.5 text-xs">
          <ChatCircleDotsIcon className="shrink-0 text-muted-foreground" />
          <span className="min-w-0 flex-1 truncate">{chatTitle(c)}</span>
          <span
            className="shrink-0 text-muted-foreground"
            title={chatStateLabels[c.state]}
          >
            {c.state === "running" ? "Working" : formatAge(c.updatedAt)}
          </span>
          <Button
            variant="ghost"
            size="sm"
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
    <ul className="divide-y">
      {list.map((s) => (
        <li key={s.name} className="flex items-center gap-2 py-1.5 text-xs">
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
            size="sm"
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
