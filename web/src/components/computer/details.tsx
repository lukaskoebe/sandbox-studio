import { useState, type ReactNode } from "react"
import {
  ClockCounterClockwiseIcon,
  CpuIcon,
  GearIcon,
  GitBranchIcon,
  HardDriveIcon,
  MonitorIcon,
  PlugsConnectedIcon,
  WarningIcon,
  type Icon,
} from "@phosphor-icons/react"
import {
  ComputerDialogs,
  type ComputerDialog,
} from "@/components/computer/computer-menu"
import { BaseMark } from "@/components/base-dialog"
import { canCopyDisks, phase, phaseDot } from "@/components/status-badge"
import { StatusDot } from "@/components/status-dot"
import { formatMiB } from "@/components/sandbox-details"
import { sizeLabel } from "@/components/resize-sandbox-dialog"
import { ScrollArea } from "@/components/ui/scroll-area"
import { Button } from "@/components/ui/button"
import {
  $api,
  type Sandbox,
  type SandboxUsage,
  type Teammate,
} from "@/lib/api/client"
import { shortTemplate } from "@/lib/lineage"
import { cn, formatAge, formatUptime } from "@/lib/utils"

/**
 * The screen's Details tab: what the ⋯ menu used to hold, laid out to read. Its state, disks,
 * ports, how to reach it, its checkpoints and where it came from, with Maintenance last. It
 * works while the computer is off too.
 */
export function ComputerDetails({
  env,
  sandbox: sb,
  owner,
  onPreview,
}: {
  env: string
  sandbox: Sandbox
  owner?: Teammate
  /** Opens the Preview tab on a port. */
  onPreview: (port: number) => void
}) {
  const [dialog, setDialog] = useState<ComputerDialog>()
  const p = phase(sb)
  const dot = phaseDot(p)
  const running = sb.status === "running"
  const building = sb.status === "creating" || sb.status === "failed"
  const available = sb.status === "running" || sb.status === "stopped"
  const copies = canCopyDisks(sb)
  const usage = $api.useQuery(
    "get",
    "/api/environments/{env}/sandboxes/{id}/usage",
    { params: { path: { env, id: sb.id } } },
    { enabled: running, refetchInterval: running ? 5000 : false, retry: false }
  )
  const ports = $api.useQuery(
    "get",
    "/api/environments/{env}/sandboxes/{id}/ports",
    { params: { path: { env, id: sb.id } } },
    { enabled: running, refetchInterval: 5000 }
  )
  const checkpoints = $api.useQuery(
    "get",
    "/api/environments/{env}/sandboxes/{id}/checkpoints",
    { params: { path: { env, id: sb.id } } },
    { enabled: !building }
  )
  const sandboxes = $api.useQuery("get", "/api/environments/{env}/sandboxes", {
    params: { path: { env } },
  })
  const source = sandboxes.data?.find((s) => s.id === sb.forkOf)
  const saved = [...(checkpoints.data ?? [])]
    .filter((c) => c.state === "ready")
    .sort((a, b) => b.createdAt.localeCompare(a.createdAt))
  const template = shortTemplate(sb.templateId, sb.templateName)
  const disks = [
    { name: "Workspace", path: "/workspace", mib: sb.workspaceMiB },
    { name: "Docker", path: "/var/lib/docker", mib: sb.dockerMiB },
    ...(sb.homeMiB
      ? [{ name: "Home", path: "/home/agent", mib: sb.homeMiB }]
      : []),
  ]
  const low = new Map((sb.lowDisks ?? []).map((d) => [d.name, d]))
  const open = (d: ComputerDialog) => () => setDialog(d)

  const maintenance: {
    label: string
    hint: string
    action: ReactNode
  }[] = [
    {
      label: "Rebase",
      hint: "Move it onto the newest base or another template. Its files and home are copied over.",
      action: (
        <Button
          size="sm"
          variant="outline"
          disabled={!copies}
          onClick={open("rebase")}
        >
          Rebase…
        </Button>
      ),
    },
    {
      label: "Fork",
      hint: "A copy that starts from this moment, to try something risky.",
      action: (
        <Button
          size="sm"
          variant="outline"
          disabled={!available}
          onClick={open("fork")}
        >
          Fork…
        </Button>
      ),
    },
    {
      label: sb.forkDefaults ? "Base settings" : "Use as a base",
      hint: "New computers can start from it instead of a template.",
      action: (
        <Button
          size="sm"
          variant="outline"
          onClick={open("base")}
          aria-label={
            sb.forkDefaults ? "Change the base settings…" : "Use as a base…"
          }
        >
          {sb.forkDefaults ? "Change…" : "Use…"}
        </Button>
      ),
    },
    ...(source
      ? [
          {
            label: `Merge into ${source.name}`,
            hint: running
              ? `Bring what changed here back to ${source.name}.`
              : "Start this computer to merge it.",
            action: (
              <Button
                size="sm"
                variant="outline"
                disabled={!running}
                onClick={open("merge")}
                aria-label={`Merge into ${source.name}…`}
              >
                Merge…
              </Button>
            ),
          },
        ]
      : []),
    {
      label: "Export",
      hint: "Template, resources, /workspace and /home/agent in one file. Secrets, checkpoints and Docker state stay here.",
      action: available ? (
        <Button
          size="sm"
          variant="outline"
          render={
            <a
              href={`/api/environments/${encodeURIComponent(env)}/sandboxes/${encodeURIComponent(sb.id)}/export`}
              download={`${sb.name}.studio-sandbox`}
            />
          }
        >
          Export
        </Button>
      ) : (
        <Button size="sm" variant="outline" disabled>
          Export
        </Button>
      ),
    },
    {
      label: "Delete",
      hint: "The computer and its checkpoints, for good.",
      action: (
        <Button
          size="sm"
          variant="destructive"
          onClick={open("delete")}
          aria-label={`Delete ${sb.name}…`}
        >
          Delete…
        </Button>
      ),
    },
  ]

  return (
    <ScrollArea
      role="region"
      aria-label={`${sb.name}'s details`}
      className="min-h-0 flex-1"
      viewportClassName="@container px-4 pt-5 pb-8 @2xl:px-7"
    >
      <div className="grid min-w-0 gap-5 text-xs">
        <header className="grid min-w-0 gap-1">
          <h2 className="flex min-w-0 flex-wrap items-center gap-2.5 font-mono text-title font-semibold">
            <span className="truncate">{sb.name}</span>
            <Pill>
              <span aria-hidden className="contents">
                <StatusDot {...dot} compact />
              </span>
              {dot.label}
            </Pill>
            {sb.forkDefaults && (
              <Pill>
                <BaseMark />
                base
              </Pill>
            )}
          </h2>
          <p className="text-muted-foreground">
            {owner ? `${owner.name}'s computer` : "Your computer"} · template{" "}
            <span className="font-mono">{template}</span>
            {sb.baseVersion && (
              <>
                {" "}
                · base <span className="font-mono">{sb.baseVersion}</span>
              </>
            )}
          </p>
        </header>
        {sb.baseOutdated && !building && (
          <div
            role="status"
            className="flex flex-wrap items-center gap-2.5 rounded-(--radius) border border-(--st-waiting)/45 bg-(--st-waiting)/8 px-3 py-2"
          >
            <WarningIcon className="size-4 shrink-0 text-(--st-waiting-fg) dark:text-(--st-waiting)" />
            <span className="min-w-0 flex-[1_1_15rem]">
              <b className="font-semibold">Built on an older base</b>
              {sb.baseVersion ? `, ${sb.baseVersion}` : ""}. Rebase for the
              newer harness versions and chat adapters, and Debian&apos;s
              security updates.
            </span>
            <Button size="sm" disabled={!copies} onClick={open("rebase")}>
              Rebase…
            </Button>
          </div>
        )}
        <div className="-mb-4 columns-[16.5rem] gap-4">
          <Card
            label="State"
            icon={CpuIcon}
            action={
              <Button
                size="xs"
                variant="outline"
                disabled={p !== "ready" && p !== "stopped" && p !== "suspended"}
                onClick={open("resources")}
              >
                Resources…
              </Button>
            }
          >
            <p className="flex items-center gap-2">
              <span aria-hidden className="contents">
                <StatusDot {...dot} compact />
              </span>
              <b className="font-semibold">{dot.label}</b>
              {running && sb.startedAt && (
                <span className="text-muted-foreground">
                  {formatUptime(sb.startedAt).replace(/^Up /, "up ")}
                </span>
              )}
            </p>
            <p className="font-mono text-muted-foreground tabular-nums">
              {sizeLabel(sb)}
            </p>
            {running && usage.data && <Meters usage={usage.data} />}
          </Card>
          <Card
            label="Disks"
            icon={HardDriveIcon}
            action={
              <Button
                size="xs"
                variant="outline"
                disabled={!copies}
                onClick={open("disks")}
                aria-label="Resize disks…"
              >
                Resize…
              </Button>
            }
          >
            <dl className="grid gap-2">
              {disks.map((d) => {
                const l = low.get(
                  d.name.toLowerCase() as "home" | "workspace" | "docker"
                )
                return (
                  <div key={d.name} className="flex items-baseline gap-2">
                    <dt className="min-w-0 flex-1">
                      {d.name}{" "}
                      <span className="font-mono text-muted-foreground">
                        {d.path}
                      </span>
                    </dt>
                    <dd
                      className={cn(
                        "shrink-0 font-mono tabular-nums",
                        l && "text-(--st-waiting-fg) dark:text-(--st-waiting)"
                      )}
                      title={l ? "Nearly full" : undefined}
                    >
                      {l
                        ? `${formatMiB(Math.round(l.free / 2 ** 20))} free of ${formatMiB(d.mib)}`
                        : formatMiB(d.mib)}
                    </dd>
                  </div>
                )
              })}
            </dl>
          </Card>
          <Card label="Ports and previews" icon={MonitorIcon}>
            {running && ports.data?.length ? (
              <ul className="grid gap-1.5">
                {ports.data.map((port) => (
                  <li key={port.port} className="flex items-center gap-2">
                    <span className="min-w-0 flex-1 truncate font-mono">
                      :{port.port}
                    </span>
                    <Button
                      size="xs"
                      variant="outline"
                      onClick={() => onPreview(port.port)}
                    >
                      Preview
                    </Button>
                  </li>
                ))}
              </ul>
            ) : (
              <p className="text-muted-foreground">
                {running
                  ? "Nothing is listening."
                  : `None while it is ${dot.label.toLowerCase()}.`}
              </p>
            )}
          </Card>
          <Card label="Reach it" icon={PlugsConnectedIcon}>
            <Row
              name="SSH"
              value="From ssh, scp or an editor that works over SSH"
              action={
                <Button
                  size="xs"
                  variant="outline"
                  onClick={open("ssh")}
                  aria-label="Connect over SSH…"
                >
                  Connect…
                </Button>
              }
            />
            <Row
              name="Connections"
              value="What it reached, and what waits"
              action={
                <Button
                  size="xs"
                  variant="outline"
                  onClick={open("connections")}
                  aria-label="Show its connections…"
                >
                  Show…
                </Button>
              }
            />
            <Row
              name="Teammate"
              value={owner?.name ?? "None, it is yours"}
              action={
                <Button
                  size="xs"
                  variant="outline"
                  onClick={open("teammate")}
                  aria-label="Change its teammate…"
                >
                  Change…
                </Button>
              }
            />
          </Card>
          {!building && (
            <Card
              label="Checkpoints"
              icon={ClockCounterClockwiseIcon}
              action={
                <Button
                  size="xs"
                  variant="outline"
                  disabled={p !== "ready" && p !== "stopped"}
                  onClick={open("checkpoint")}
                >
                  Checkpoint now
                </Button>
              }
            >
              {saved.length ? (
                <ul className="grid gap-1.5">
                  {saved.slice(0, 3).map((c) => (
                    <li key={c.id} className="flex items-baseline gap-2">
                      <span className="min-w-0 flex-1 truncate font-mono">
                        {c.name}
                      </span>
                      <span className="shrink-0 text-muted-foreground">
                        {formatAge(c.createdAt)}
                      </span>
                    </li>
                  ))}
                </ul>
              ) : (
                <p className="text-muted-foreground">None saved yet.</p>
              )}
              <Button
                size="xs"
                variant="ghost"
                className="justify-self-start"
                onClick={open("history")}
              >
                All checkpoints and history…
              </Button>
            </Card>
          )}
          <Card
            label="History"
            icon={GitBranchIcon}
            action={
              <span
                title="The computer's ID"
                className="font-mono text-meta text-muted-foreground select-all"
              >
                {sb.id}
              </span>
            }
          >
            <ol className="ml-1 grid gap-2.5 border-l pl-3.5">
              {sb.generation > 1 && (
                <TimelineItem
                  what={`Rebased ${sb.generation - 1} time${sb.generation > 2 ? "s" : ""}`}
                  when={`generation ${sb.generation}`}
                />
              )}
              <TimelineItem
                what={
                  sb.forkOf ? (
                    <>
                      Forked from{" "}
                      <span className="font-mono">
                        {source?.name ?? sb.forkOf}
                      </span>
                    </>
                  ) : (
                    <>
                      Created from <span className="font-mono">{template}</span>
                    </>
                  )
                }
                when={new Date(sb.createdAt).toLocaleDateString(undefined, {
                  month: "short",
                  day: "numeric",
                  year: "numeric",
                })}
              />
            </ol>
          </Card>
        </div>
        <Card label="Maintenance" icon={GearIcon} className="mb-0">
          <ul className="grid">
            {maintenance.map((m) => (
              <li
                key={m.label}
                className="flex items-center gap-3 border-t border-border/60 py-2.5 first:border-t-0 first:pt-0 last:pb-0"
              >
                <span className="grid min-w-0 flex-1 gap-0.5">
                  <span className="font-medium">{m.label}</span>
                  <span className="text-muted-foreground">{m.hint}</span>
                </span>
                <span className="flex shrink-0 justify-end [&>*]:min-w-22">
                  {m.action}
                </span>
              </li>
            ))}
          </ul>
        </Card>
      </div>
      <ComputerDialogs
        env={env}
        sandbox={sb}
        owner={owner}
        dialog={dialog}
        onClose={() => setDialog(undefined)}
        tab="details"
      />
    </ScrollArea>
  )
}

function Pill({ children }: { children: ReactNode }) {
  return (
    <span className="inline-flex items-center gap-1.5 rounded-full border px-2 py-px font-sans text-meta font-medium whitespace-nowrap text-muted-foreground">
      {children}
    </span>
  )
}

function Card({
  label,
  icon: I,
  action,
  className,
  children,
}: {
  label: string
  icon: Icon
  action?: ReactNode
  className?: string
  children: ReactNode
}) {
  return (
    <section
      aria-label={label}
      className={cn(
        "mb-4 grid min-w-0 break-inside-avoid gap-3 rounded-(--radius) border bg-card px-4 pt-3.5 pb-4",
        className
      )}
    >
      <header className="flex min-h-6 items-center justify-between gap-2">
        <h3 className="inline-flex items-center gap-1.5 font-medium">
          <I className="size-3.5 text-muted-foreground" />
          {label}
        </h3>
        {action}
      </header>
      {children}
    </section>
  )
}

function Row({
  name,
  value,
  action,
}: {
  name: string
  value: ReactNode
  action: ReactNode
}) {
  return (
    <div className="flex min-w-0 items-center gap-2.5">
      <span className="grid min-w-0 flex-1 leading-snug">
        <span className="text-meta text-muted-foreground">{name}</span>
        <span className="truncate">{value}</span>
      </span>
      {action}
    </div>
  )
}

function TimelineItem({ what, when }: { what: ReactNode; when: string }) {
  return (
    <li className="relative grid gap-px before:absolute before:top-1.5 before:-left-[1.15rem] before:size-1.5 before:rounded-full before:border-[1.5px] before:border-muted-foreground before:bg-card">
      <span>{what}</span>
      <span className="text-muted-foreground">{when}</span>
    </li>
  )
}

/** CPU and memory as two bars, as msb sampled them over the last few seconds. */
function Meters({ usage }: { usage: SandboxUsage }) {
  const GiB = 1 << 30
  const mem = (b: number) => (b / GiB).toFixed(1).replace(/\.0$/, "")
  const rows = [
    {
      label: "CPU",
      share: usage.cpuPercent / 100,
      value: `${Math.round(usage.cpuPercent)} % of ${usage.cpus} cores`,
    },
    {
      label: "Memory",
      share: usage.memoryBytes / Math.max(usage.memoryTotalBytes, 1),
      value: `${mem(usage.memoryBytes)} / ${mem(usage.memoryTotalBytes)} GiB`,
    },
  ]
  return (
    <div className="grid gap-2.5">
      {rows.map((r) => (
        <div key={r.label} className="grid gap-1">
          <span className="flex justify-between gap-2">
            <span>{r.label}</span>
            <span className="font-mono text-muted-foreground tabular-nums">
              {r.value}
            </span>
          </span>
          <span className="h-1.5 overflow-hidden rounded-full bg-muted">
            <span
              className={cn(
                "block h-full rounded-full bg-primary/75",
                r.share > 0.85 && "bg-(--st-waiting)"
              )}
              style={{ width: `${Math.min(100, Math.max(1, r.share * 100))}%` }}
            />
          </span>
        </div>
      ))}
    </div>
  )
}
