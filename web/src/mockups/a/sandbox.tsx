import { useState, type ReactNode } from "react"
import { Link, useSearch } from "@tanstack/react-router"
import {
  ArrowSquareOutIcon,
  CaretDownIcon,
  ChatsCircleIcon,
  ClockCounterClockwiseIcon,
  CopyIcon,
  DotsThreeIcon,
  DownloadSimpleIcon,
  GitForkIcon,
  PauseIcon,
  PlayIcon,
  PlusIcon,
  StackIcon,
  StopIcon,
  TerminalWindowIcon,
  TrashIcon,
  WarningIcon,
  XIcon,
} from "@phosphor-icons/react"
import { Button } from "@/components/ui/button"
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuGroup,
  DropdownMenuItem,
  DropdownMenuLabel,
  DropdownMenuSeparator,
  DropdownMenuTrigger,
} from "@/components/ui/dropdown-menu"
import { TeammateAvatar } from "@/components/teammate-avatar"
import { cn } from "@/lib/utils"
import {
  connections,
  environment,
  gitPush,
  previews,
  sandboxes,
  sessions,
  terminal,
  type Connection,
} from "../data"
import {
  Cursor,
  StatusDot,
  TermLine,
  toneIcons,
  type MockupSearch,
} from "../shared"
import {
  Bar,
  Kbd,
  Meter,
  Mono,
  Section,
  Tile,
  WaitingCount,
  WorkbenchShell,
} from "./shell"

const sb = sandboxes[0]
const keep = (prev: MockupSearch) => ({ theme: prev.theme, bare: prev.bare })

/** Header shared by the dashboard and the workspace: tabs, status, options menu. */
function SandboxBar({ page }: { page: "dashboard" | "workspace" }) {
  const search: MockupSearch = useSearch({ strict: false })
  return (
    <Bar crumbs={[environment.slug, "sandboxes", sb.name]}>
      <span
        data-status={sb.status}
        className="inline-flex h-5 items-center gap-1.5 rounded-[3px] bg-(--status)/12 px-1.5 font-mono text-[0.625rem] font-medium text-[color-mix(in_oklch,var(--status),var(--foreground)_35%)] max-sm:hidden"
      >
        <StatusDot status={sb.status} className="size-1.5" />
        running
      </span>
      <nav className="flex h-6 items-center rounded-[4px] bg-muted p-0.5 text-xs">
        {(["dashboard", "workspace"] as const).map((p) => (
          <Link
            key={p}
            to="/mockups/$direction/$screen"
            params={{ direction: "a", screen: p }}
            search={keep}
            className={cn(
              "flex h-5 items-center gap-1 rounded-[3px] px-2 capitalize",
              p === page
                ? "bg-background font-medium shadow-xs"
                : "text-muted-foreground hover:text-foreground"
            )}
          >
            {p === "dashboard" ? (
              <StackIcon className="size-3" />
            ) : (
              <TerminalWindowIcon className="size-3" />
            )}
            <span className="max-sm:hidden">{p}</span>
          </Link>
        ))}
      </nav>
      <OptionsMenu defaultOpen={search.menu} />
    </Bar>
  )
}

function OptionsMenu({ defaultOpen }: { defaultOpen?: boolean }) {
  const item = (icon: ReactNode, label: string, keys?: string) => (
    <DropdownMenuItem className="gap-2 rounded-[3px] text-xs">
      {icon}
      {label}
      {keys && (
        <span className="ml-auto font-mono text-[0.625rem] text-muted-foreground">
          {keys}
        </span>
      )}
    </DropdownMenuItem>
  )
  return (
    <DropdownMenu defaultOpen={defaultOpen} modal={false}>
      <DropdownMenuTrigger
        render={
          <Button
            variant="outline"
            size="icon-sm"
            className="rounded-[4px]"
            aria-label="Sandbox options"
          />
        }
      >
        <DotsThreeIcon weight="bold" />
      </DropdownMenuTrigger>
      <DropdownMenuContent align="end" className="w-52 rounded-(--radius) p-1">
        <DropdownMenuGroup>
          <DropdownMenuLabel className="font-mono text-[0.625rem] tracking-wider uppercase">
            {sb.name} · {sb.id}
          </DropdownMenuLabel>
          {item(<GitForkIcon />, "Fork…", "F")}
          {item(<ClockCounterClockwiseIcon />, "Rebase on template…", "R")}
          {item(<DownloadSimpleIcon />, "Export…", "E")}
        </DropdownMenuGroup>
        <DropdownMenuSeparator />
        <DropdownMenuGroup>
          {item(<PauseIcon />, "Suspend", "S")}
          {item(<StopIcon />, "Stop", "⇧S")}
        </DropdownMenuGroup>
        <DropdownMenuSeparator />
        <DropdownMenuItem
          variant="destructive"
          className="gap-2 rounded-[3px] text-xs"
        >
          <TrashIcon />
          Delete…
        </DropdownMenuItem>
      </DropdownMenuContent>
    </DropdownMenu>
  )
}

type Panel = "browser" | "connections" | "changes"

const panelTones = {
  browser: "browser",
  connections: "network",
  changes: "git",
} as const

const panelLabels: Record<Panel, string> = {
  browser: "Browser",
  connections: "Connections",
  changes: "Changes",
}

export function WorkbenchWorkspace() {
  const [panel, setPanel] = useState<Panel | null>(() =>
    window.innerWidth >= 768 ? "connections" : null
  )
  return (
    <WorkbenchShell active={sb.name}>
      <SandboxBar page="workspace" />
      <div className="flex h-9 shrink-0 items-stretch gap-1 border-b bg-sidebar/60 pr-2">
        <div className="flex min-w-0 items-stretch overflow-x-auto">
          {sessions.map((s) => {
            const I = s.kind === "chat" ? ChatsCircleIcon : TerminalWindowIcon
            return (
              <button
                key={s.name}
                type="button"
                data-tone={s.kind === "chat" ? "agent" : undefined}
                className={cn(
                  "relative flex shrink-0 items-center gap-1.5 border-r px-3 text-xs",
                  s.active
                    ? "bg-background font-medium after:absolute after:inset-x-0 after:top-0 after:h-0.5 after:bg-primary"
                    : "text-muted-foreground hover:bg-background/60 hover:text-foreground max-sm:hidden"
                )}
              >
                <I
                  className={cn(
                    "size-3.5",
                    s.kind === "chat" && "text-(--tone)"
                  )}
                />
                <span className={cn(s.kind === "terminal" && "font-mono")}>
                  {s.name}
                </span>
                {s.active && (
                  <CaretDownIcon className="size-3 text-muted-foreground sm:hidden" />
                )}
              </button>
            )
          })}
          <button
            type="button"
            className="flex w-8 shrink-0 items-center justify-center text-muted-foreground hover:text-foreground"
            title="New session"
          >
            <PlusIcon className="size-3.5" />
          </button>
        </div>
        <div className="ml-auto flex items-center gap-1">
          <PreviewMenu />
          <span className="mx-1 h-4 w-px bg-border" />
          {(Object.keys(panelLabels) as Panel[]).map((p) => {
            const tone = panelTones[p]
            const I = toneIcons[tone]
            const on = panel === p
            return (
              <button
                key={p}
                type="button"
                data-tone={tone}
                aria-pressed={on}
                onClick={() => setPanel(on ? null : p)}
                title={panelLabels[p]}
                className={cn(
                  "flex h-6 items-center gap-1.5 rounded-[4px] px-1.5 text-xs",
                  on
                    ? "bg-(--tone)/12 font-medium text-foreground ring-1 ring-(--tone)/35 ring-inset"
                    : "text-muted-foreground hover:bg-muted hover:text-foreground"
                )}
              >
                <I className={cn("size-3.5", on && "text-(--tone)")} />
                <span className="max-lg:hidden">{panelLabels[p]}</span>
                {p === "connections" && <WaitingCount n={2} />}
              </button>
            )
          })}
        </div>
      </div>
      <div className="relative flex min-h-0 flex-1">
        <FakeTerminal />
        {panel && (
          <>
            <div className="group hidden w-1.5 shrink-0 cursor-col-resize items-center justify-center border-l bg-sidebar hover:bg-primary/20 md:flex">
              <span className="h-8 w-0.5 rounded-full bg-foreground/25 group-hover:bg-primary" />
            </div>
            <aside className="flex min-h-0 flex-col bg-card max-md:fixed max-md:inset-0 max-md:z-40 md:w-[24rem]">
              <PanelHeader panel={panel} onClose={() => setPanel(null)} />
              {panel === "connections" ? (
                <ConnectionsPanel />
              ) : panel === "changes" ? (
                <ChangesPanel />
              ) : (
                <BrowserPanel />
              )}
            </aside>
          </>
        )}
      </div>
      <StatusBar />
    </WorkbenchShell>
  )
}

function PreviewMenu() {
  return (
    <DropdownMenu modal={false}>
      <DropdownMenuTrigger
        render={
          <Button
            variant="ghost"
            size="sm"
            className="h-6 rounded-[4px] font-mono text-[0.6875rem]"
          />
        }
      >
        <ArrowSquareOutIcon />
        <span className="max-sm:hidden">:5173</span>
        <CaretDownIcon className="size-3" />
      </DropdownMenuTrigger>
      <DropdownMenuContent align="end" className="w-56 rounded-(--radius)">
        <DropdownMenuGroup>
          <DropdownMenuLabel className="font-mono text-[0.625rem] tracking-wider uppercase">
            Previews
          </DropdownMenuLabel>
          {previews.map((p) => (
            <DropdownMenuItem key={p.port} className="rounded-[3px] text-xs">
              <span className="font-mono">:{p.port}</span>
              <span className="text-muted-foreground">{p.label}</span>
              <ArrowSquareOutIcon className="ml-auto" />
            </DropdownMenuItem>
          ))}
        </DropdownMenuGroup>
      </DropdownMenuContent>
    </DropdownMenu>
  )
}

function FakeTerminal() {
  return (
    <div className="min-w-0 flex-1 overflow-auto bg-(--wb-term-bg) p-3 font-mono text-[0.75rem] leading-[1.5] text-(--wb-term-fg)">
      {terminal.map((line, i) => (
        <TermLine key={i} line={line} />
      ))}
      <Cursor />
    </div>
  )
}

function PanelHeader({
  panel,
  onClose,
}: {
  panel: Panel
  onClose: () => void
}) {
  return (
    <header className="flex h-9 shrink-0 items-center gap-2 border-b px-3">
      <Tile tone={panelTones[panel]} />
      <span className="text-[0.8125rem] font-semibold">
        {panelLabels[panel]}
      </span>
      {panel === "connections" && (
        <span className="font-mono text-[0.6875rem] text-muted-foreground">
          {connections.length}
        </span>
      )}
      <Button
        variant="ghost"
        size="icon-xs"
        className="ml-auto rounded-[3px]"
        onClick={onClose}
        aria-label="Close panel"
      >
        <XIcon />
      </Button>
    </header>
  )
}

const verdictStyle: Record<Connection["verdict"], string> = {
  held: "waiting",
  allowed: "running",
  denied: "failed",
  open: "stopped",
}

function ConnectionsPanel() {
  const filters = ["all", "held", "allowed", "denied"]
  return (
    <>
      <div className="flex shrink-0 items-center gap-1 border-b px-3 py-1.5">
        {filters.map((f, i) => (
          <button
            key={f}
            type="button"
            className={cn(
              "h-5 rounded-[3px] px-1.5 font-mono text-[0.625rem]",
              i === 0
                ? "bg-foreground text-background"
                : "text-muted-foreground hover:bg-muted"
            )}
          >
            {f}
            {f === "held" && " 2"}
          </button>
        ))}
        <span className="ml-auto font-mono text-[0.625rem] text-muted-foreground">
          live
          <span className="ml-1 inline-block size-1.5 animate-pulse rounded-full bg-(--st-running)" />
        </span>
      </div>
      <ul className="min-h-0 flex-1 overflow-y-auto">
        {connections.map((c) => (
          <li
            key={c.host + c.port}
            data-status={verdictStyle[c.verdict]}
            className={cn(
              "border-b px-3 py-1.5",
              c.verdict === "held" && "bg-(--status)/6"
            )}
          >
            <div className="flex items-center gap-2">
              <span className="size-1.5 shrink-0 rounded-full bg-(--status)" />
              <span
                className="min-w-0 flex-1 truncate font-mono"
                title={c.host}
              >
                {c.host}
                <span className="text-muted-foreground">:{c.port}</span>
              </span>
              <span className="shrink-0 font-mono text-[0.625rem] text-muted-foreground">
                {c.requests}× · {c.last}
              </span>
            </div>
            <div className="mt-0.5 flex items-center gap-2 pl-3.5 text-[0.6875rem] text-muted-foreground">
              <span className="text-[color-mix(in_oklch,var(--status),var(--foreground)_30%)]">
                {c.verdict}
              </span>
              {c.rule && (
                <span className="truncate font-mono">rule: {c.rule}</span>
              )}
              {c.verdict === "held" && (
                <span className="ml-auto flex gap-1">
                  <Button variant="ghost" size="xs" className="rounded-[3px]">
                    Deny
                  </Button>
                  <Button size="xs" className="rounded-[3px]">
                    Allow
                  </Button>
                </span>
              )}
            </div>
          </li>
        ))}
      </ul>
    </>
  )
}

function ChangesPanel() {
  const files = [
    { path: "src/routes/checkout.tsx", add: 64, del: 9, s: "M" },
    { path: "src/lib/cart.ts", add: 21, del: 5, s: "M" },
    { path: "src/components/order-summary.tsx", add: 43, del: 0, s: "A" },
  ]
  return (
    <div className="min-h-0 flex-1 overflow-y-auto">
      <div className="border-b px-3 py-2 font-mono text-[0.6875rem] text-muted-foreground">
        {gitPush.branch} · ahead 3 · {files.length} files
      </div>
      {files.map((f) => (
        <div
          key={f.path}
          className="flex items-center gap-2 border-b px-3 py-1.5 font-mono text-[0.6875rem]"
        >
          <span className="w-3 text-(--acc-git)">{f.s}</span>
          <span className="min-w-0 flex-1 truncate">{f.path}</span>
          <span className="text-(--term-add)">+{f.add}</span>
          <span className="text-(--st-failed)">−{f.del}</span>
        </div>
      ))}
    </div>
  )
}

function BrowserPanel() {
  return (
    <div className="flex min-h-0 flex-1 flex-col gap-2 p-3">
      <div className="flex h-6 items-center gap-2 rounded-[4px] border bg-muted px-2 font-mono text-[0.6875rem] text-muted-foreground">
        <span className="size-1.5 rounded-full bg-(--st-running)" />
        localhost:5173/checkout
      </div>
      <div className="grid flex-1 place-items-center rounded-[4px] border border-dashed text-muted-foreground">
        live view
      </div>
    </div>
  )
}

function StatusBar() {
  const seg = "flex h-full items-center gap-1.5 border-r px-2.5 last:border-r-0"
  return (
    <footer className="flex h-(--wb-statusbar) shrink-0 items-stretch overflow-x-auto border-t bg-sidebar font-mono text-[0.6875rem] whitespace-nowrap text-muted-foreground">
      <span className={seg} data-status="running">
        <StatusDot status="running" className="size-1.5" />
        <span className="text-foreground">running</span>
      </span>
      <span className={cn(seg, "max-sm:hidden")}>{sb.id}</span>
      <span className={cn(seg, "max-md:hidden")}>{sb.template}</span>
      <span className={seg}>
        cpu <Meter pct={sb.cpuPct} className="w-8" /> {sb.cpuPct}%
      </span>
      <span className={seg}>
        mem <Meter pct={(sb.memUsedGiB / sb.memGiB) * 100} className="w-8" />{" "}
        {sb.memUsedGiB}/{sb.memGiB} GiB
      </span>
      <span className={cn(seg, "max-md:hidden")}>
        ports {sb.ports.map((p) => `:${p}`).join(" ")}
      </span>
      <span
        className={cn(
          seg,
          "text-[color-mix(in_oklch,var(--status),var(--foreground)_30%)]"
        )}
        data-status="waiting"
      >
        <WarningIcon weight="fill" className="size-3 text-(--status)" />2
        approvals
      </span>
      <span className="ml-auto flex items-center gap-3 px-2.5 max-md:hidden">
        <span>up {sb.uptime}</span>
        <span>gen 3</span>
      </span>
    </footer>
  )
}

/* ---------- Dashboard ---------- */

export function WorkbenchDashboard() {
  const props: [string, ReactNode][] = [
    [
      "Owner",
      <span className="flex items-center gap-1.5">
        <TeammateAvatar
          name="Robin"
          className="size-4 rounded-[3px] text-[0.5rem]"
        />
        Robin
      </span>,
    ],
    ["Template", <Mono>{sb.template}</Mono>],
    [
      "Resources",
      <span className="font-mono">4 cpu · 8 GiB · 20 GiB disk</span>,
    ],
    ["Uptime", <span className="font-mono">{sb.uptime}</span>],
    [
      "ID",
      <span className="flex items-center gap-1 font-mono">
        {sb.id}
        <CopyIcon className="size-3 text-muted-foreground" />
      </span>,
    ],
  ]
  return (
    <WorkbenchShell active={sb.name}>
      <SandboxBar page="dashboard" />
      <div className="min-h-0 flex-1 overflow-y-auto">
        <dl className="grid grid-cols-2 border-b sm:grid-cols-3 xl:grid-cols-5">
          {props.map(([k, v]) => (
            <div
              key={k}
              className="min-w-0 border-r border-b px-4 py-2.5 xl:border-b-0"
            >
              <dt className="font-mono text-[0.625rem] tracking-wider text-muted-foreground uppercase">
                {k}
              </dt>
              <dd className="mt-1 truncate">{v}</dd>
            </div>
          ))}
        </dl>
        <div className="grid gap-6 p-3 md:p-4 xl:grid-cols-2">
          <Section
            title="Lineage"
            icon={GitForkIcon}
            actions={
              <Button variant="outline" size="xs" className="rounded-[3px]">
                <PlusIcon /> Checkpoint
              </Button>
            }
            className="xl:col-span-2"
          >
            <Lineage />
          </Section>
          <Section
            title="Ports"
            icon={ArrowSquareOutIcon}
            count={previews.length}
          >
            <ul className="overflow-hidden rounded-(--radius) border bg-card">
              {previews.map((p) => (
                <li
                  key={p.port}
                  className="flex h-(--wb-row) items-center gap-3 border-b px-3 last:border-b-0"
                >
                  <Mono>:{p.port}</Mono>
                  <span>{p.label}</span>
                  <span className="truncate font-mono text-[0.6875rem] text-muted-foreground">
                    {p.port}-{sb.name}.studio.localhost
                  </span>
                  <Button
                    variant="ghost"
                    size="xs"
                    className="ml-auto rounded-[3px]"
                  >
                    Open <ArrowSquareOutIcon />
                  </Button>
                </li>
              ))}
            </ul>
          </Section>
          <Section
            title="Sessions & chats"
            tone="agent"
            count={sessions.length}
            actions={
              <Button
                size="xs"
                className="rounded-[3px]"
                nativeButton={false}
                render={
                  <Link
                    to="/mockups/$direction/$screen"
                    params={{ direction: "a", screen: "workspace" }}
                    search={keep}
                  />
                }
              >
                Open workspace
              </Button>
            }
          >
            <ul className="overflow-hidden rounded-(--radius) border bg-card">
              {sessions.map((s) => (
                <li
                  key={s.name}
                  className="flex h-(--wb-row) items-center gap-2 border-b px-3 last:border-b-0"
                >
                  {s.kind === "chat" ? (
                    <ChatsCircleIcon className="size-3.5 text-(--acc-agent)" />
                  ) : (
                    <TerminalWindowIcon className="size-3.5 text-muted-foreground" />
                  )}
                  <span className={cn(s.kind === "terminal" && "font-mono")}>
                    {s.name}
                  </span>
                  <span className="ml-auto font-mono text-[0.6875rem] text-muted-foreground">
                    {s.kind === "chat" ? "12 messages · 4 min" : "attached"}
                  </span>
                </li>
              ))}
            </ul>
          </Section>
          <Section
            title="Recent connections"
            tone="network"
            count={connections.length}
            className="xl:col-span-2"
          >
            <div className="overflow-hidden rounded-(--radius) border bg-card">
              {connections.slice(0, 5).map((c) => (
                <div
                  key={c.host}
                  data-status={verdictStyle[c.verdict]}
                  className="flex h-(--wb-row) items-center gap-3 border-b px-3 last:border-b-0"
                >
                  <span className="size-1.5 shrink-0 rounded-full bg-(--status)" />
                  <span className="min-w-0 flex-1 truncate font-mono">
                    {c.host}
                    <span className="text-muted-foreground">:{c.port}</span>
                  </span>
                  <span className="w-14 font-mono text-[0.6875rem] text-[color-mix(in_oklch,var(--status),var(--foreground)_30%)]">
                    {c.verdict}
                  </span>
                  <span className="w-28 truncate font-mono text-[0.6875rem] text-muted-foreground max-sm:hidden">
                    {c.rule ?? "—"}
                  </span>
                  <span className="text-right font-mono text-[0.6875rem] whitespace-nowrap text-muted-foreground">
                    {c.requests}× · {c.last}
                  </span>
                </div>
              ))}
            </div>
          </Section>
          <Section title="Lifecycle" icon={PlayIcon} className="xl:col-span-2">
            <div className="flex flex-wrap gap-1.5">
              {[
                [<PauseIcon key="i" />, "Suspend"],
                [<StopIcon key="i" />, "Stop"],
                [<GitForkIcon key="i" />, "Fork"],
                [<ClockCounterClockwiseIcon key="i" />, "Rebase"],
                [<DownloadSimpleIcon key="i" />, "Export"],
              ].map(([icon, label]) => (
                <Button
                  key={String(label)}
                  variant="outline"
                  size="sm"
                  className="rounded-[4px]"
                >
                  {icon}
                  {label}
                </Button>
              ))}
              <Button
                variant="destructive"
                size="sm"
                className="ml-auto rounded-[4px]"
              >
                <TrashIcon /> Delete
              </Button>
            </div>
            <p className="mt-2 flex items-center gap-1.5 text-muted-foreground">
              Same actions live in the <Kbd>⋯</Kbd> menu on both pages.
            </p>
          </Section>
        </div>
      </div>
    </WorkbenchShell>
  )
}

/** A git-graph-like timeline: generations as segments, checkpoints as nodes, a fork branching off. */
function Lineage() {
  return (
    <div className="overflow-x-auto rounded-(--radius) border bg-card p-3">
      <svg
        viewBox="0 0 760 92"
        className="h-[92px] w-full min-w-[640px] font-mono text-[10px]"
      >
        <line
          x1="20"
          y1="34"
          x2="720"
          y2="34"
          className="stroke-foreground/25"
          strokeWidth="2"
        />
        <line
          x1="20"
          y1="34"
          x2="250"
          y2="34"
          className="stroke-foreground/45"
          strokeWidth="2"
        />
        <line
          x1="250"
          y1="34"
          x2="720"
          y2="34"
          className="stroke-primary"
          strokeWidth="2"
        />
        <text x="20" y="18" className="fill-muted-foreground">
          web-app@2
        </text>
        <text x="258" y="18" className="fill-muted-foreground">
          web-app@3 · rebased
        </text>
        <rect
          x="244"
          y="28"
          width="12"
          height="12"
          rx="2"
          className="fill-card stroke-foreground/60"
          strokeWidth="1.5"
        />
        {[
          [90, "deps ok"],
          [180, "before auth"],
          [400, "cart works"],
        ].map(([x, label]) => (
          <g key={label}>
            <circle
              cx={x}
              cy="34"
              r="5"
              className="fill-card stroke-foreground/70"
              strokeWidth="1.5"
            />
            <text x={Number(x) - 22} y="56" className="fill-muted-foreground">
              {label}
            </text>
          </g>
        ))}
        <path
          d="M400 34 C 440 34, 440 72, 480 72 L 600 72"
          fill="none"
          className="stroke-(--acc-git)"
          strokeWidth="1.5"
          strokeDasharray="0"
        />
        <circle cx="600" cy="72" r="4" className="fill-(--acc-git)" />
        <text x="610" y="75" className="fill-(--acc-git)">
          shop-api-spike
        </text>
        <circle
          cx="720"
          cy="34"
          r="7"
          className="fill-primary/20 stroke-primary"
          strokeWidth="2"
        />
        <text x="700" y="56" className="fill-foreground">
          now
        </text>
      </svg>
    </div>
  )
}
