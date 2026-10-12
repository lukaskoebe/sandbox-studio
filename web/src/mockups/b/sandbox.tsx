import { useState, type ReactNode } from "react"
import { Link, useSearch } from "@tanstack/react-router"
import {
  ArrowSquareOutIcon,
  BookmarkSimpleIcon,
  CaretDownIcon,
  ChatsCircleIcon,
  ClockCounterClockwiseIcon,
  DotsThreeIcon,
  DownloadSimpleIcon,
  GitForkIcon,
  PauseIcon,
  PlusIcon,
  StopIcon,
  TerminalWindowIcon,
  TrashIcon,
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
  CalmShell,
  KindChip,
  MobileBar,
  PageTitle,
  SectionTitle,
  keep,
} from "./shell"

const sb = sandboxes[0]

function OptionsMenu({ defaultOpen }: { defaultOpen?: boolean }) {
  const item = (icon: ReactNode, label: string, hint?: string) => (
    <DropdownMenuItem className="min-h-9 gap-2.5 rounded-lg px-2.5 text-[0.8125rem]">
      {icon}
      <span className="grid leading-tight">
        {label}
        {hint && (
          <span className="text-[0.6875rem] text-muted-foreground">{hint}</span>
        )}
      </span>
    </DropdownMenuItem>
  )
  return (
    <DropdownMenu defaultOpen={defaultOpen} modal={false}>
      <DropdownMenuTrigger
        render={
          <Button
            variant="outline"
            size="icon-lg"
            className="rounded-full bg-card"
            aria-label="Sandbox options"
          />
        }
      >
        <DotsThreeIcon weight="bold" />
      </DropdownMenuTrigger>
      <DropdownMenuContent
        align="end"
        className="w-60 rounded-xl p-1.5 shadow-[var(--cs-shadow)]"
      >
        <DropdownMenuGroup>
          <DropdownMenuLabel className="text-[0.6875rem]">
            shop-api
          </DropdownMenuLabel>
          {item(<GitForkIcon />, "Fork", "A copy to try something risky")}
          {item(
            <ClockCounterClockwiseIcon />,
            "Rebase",
            "Move to a newer template"
          )}
          {item(<DownloadSimpleIcon />, "Export")}
        </DropdownMenuGroup>
        <DropdownMenuSeparator />
        <DropdownMenuGroup>
          {item(<PauseIcon />, "Suspend")}
          {item(<StopIcon />, "Stop")}
        </DropdownMenuGroup>
        <DropdownMenuSeparator />
        <DropdownMenuItem
          variant="destructive"
          className="min-h-9 gap-2.5 rounded-lg px-2.5 text-[0.8125rem]"
        >
          <TrashIcon />
          Delete sandbox
        </DropdownMenuItem>
      </DropdownMenuContent>
    </DropdownMenu>
  )
}

function SandboxHeader({ page }: { page: "dashboard" | "workspace" }) {
  const search: MockupSearch = useSearch({ strict: false })
  return (
    <header className="flex shrink-0 flex-wrap items-center gap-x-4 gap-y-2 border-b px-4 py-3 md:px-6">
      <div className="flex min-w-0 items-center gap-3">
        <TeammateAvatar
          name="Robin"
          className="size-8 text-[0.6875rem] max-sm:hidden"
        />
        <div className="min-w-0 leading-tight">
          <div className="flex items-center gap-2">
            <h1 className="truncate text-lg font-semibold tracking-[-0.01em]">
              {sb.name}
            </h1>
            <span
              data-status="running"
              className="inline-flex h-5 items-center gap-1.5 rounded-full bg-(--status)/12 px-2 text-[0.6875rem] font-medium text-[color-mix(in_oklch,var(--status),var(--foreground)_35%)]"
            >
              <StatusDot status="running" className="size-1.5" />
              Running
            </span>
          </div>
          <div className="text-xs text-muted-foreground max-sm:hidden">
            Robin's sandbox · up {sb.uptime}
          </div>
        </div>
      </div>
      <nav className="order-last flex w-full gap-5 text-[0.8125rem] md:order-none md:ml-6 md:w-auto">
        {(
          [
            ["dashboard", "Overview"],
            ["workspace", "Work"],
          ] as const
        ).map(([p, label]) => (
          <Link
            key={p}
            to="/mockups/$direction/$screen"
            params={{ direction: "b", screen: p }}
            search={keep}
            className={cn(
              "relative py-1",
              p === page
                ? "font-semibold after:absolute after:inset-x-0 after:-bottom-[0.8rem] after:h-0.5 after:rounded-full after:bg-primary"
                : "text-muted-foreground hover:text-foreground"
            )}
          >
            {label}
          </Link>
        ))}
      </nav>
      <div className="ml-auto flex items-center gap-2">
        {page === "dashboard" && (
          <Button
            className="h-9 rounded-full px-4 text-[0.8125rem]"
            nativeButton={false}
            render={
              <Link
                to="/mockups/$direction/$screen"
                params={{ direction: "b", screen: "workspace" }}
                search={keep}
              />
            }
          >
            <TerminalWindowIcon /> Open workspace
          </Button>
        )}
        <OptionsMenu defaultOpen={search.menu} />
      </div>
    </header>
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
const verdictLabels: Record<Connection["verdict"], string> = {
  held: "Waiting for you",
  allowed: "Allowed",
  denied: "Blocked",
  open: "Open",
}

export function CalmWorkspace() {
  const [panel, setPanel] = useState<Panel | null>(() =>
    window.innerWidth >= 768 ? "connections" : null
  )
  return (
    <CalmShell active={sb.name}>
      <MobileBar title={sb.name} />
      <SandboxHeader page="workspace" />
      <div className="flex shrink-0 flex-wrap items-center gap-2 px-4 py-2.5 md:px-6">
        <DropdownMenu modal={false}>
          <DropdownMenuTrigger
            render={
              <Button
                variant="outline"
                className="h-8 shrink-0 rounded-full bg-card px-3 text-[0.8125rem]"
              />
            }
          >
            <TerminalWindowIcon />
            dev server
            <CaretDownIcon className="size-3 text-muted-foreground" />
          </DropdownMenuTrigger>
          <DropdownMenuContent className="w-56 rounded-xl p-1.5">
            {sessions.map((s) => (
              <DropdownMenuItem
                key={s.name}
                className="rounded-lg text-[0.8125rem]"
              >
                {s.kind === "chat" ? (
                  <ChatsCircleIcon className="text-(--acc-agent)" />
                ) : (
                  <TerminalWindowIcon />
                )}
                {s.name}
              </DropdownMenuItem>
            ))}
          </DropdownMenuContent>
        </DropdownMenu>
        <Button
          variant="ghost"
          className="h-8 shrink-0 rounded-full px-3 text-[0.8125rem] text-muted-foreground"
        >
          <PlusIcon /> New
        </Button>
        <Button
          variant="ghost"
          className="h-8 shrink-0 rounded-full px-3 text-[0.8125rem] text-muted-foreground"
        >
          <ArrowSquareOutIcon /> Preview{" "}
          <span className="font-mono text-xs">:5173</span>
        </Button>
        <div className="ml-auto flex shrink-0 gap-1 rounded-full bg-muted p-1">
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
                className={cn(
                  "flex h-7 items-center gap-1.5 rounded-full px-3 text-[0.8125rem]",
                  on
                    ? "bg-card font-medium shadow-sm"
                    : "text-muted-foreground hover:text-foreground"
                )}
              >
                <I
                  weight="duotone"
                  className={cn("size-4", on && "text-(--tone)")}
                />
                <span className="max-lg:hidden">{panelLabels[p]}</span>
              </button>
            )
          })}
        </div>
      </div>
      <div className="flex min-h-0 flex-1 gap-0 px-3 pb-3 md:px-4 md:pb-4">
        <div className="min-w-0 flex-1 overflow-auto rounded-xl border bg-(--cs-term-bg) p-4 font-mono text-[0.8125rem] leading-[1.6] text-(--cs-term-fg)">
          {terminal.map((line, i) => (
            <TermLine key={i} line={line} />
          ))}
          <Cursor />
        </div>
        {panel && (
          <>
            <div className="group hidden w-3 shrink-0 cursor-col-resize items-center justify-center md:flex">
              <span className="h-10 w-1 rounded-full bg-foreground/15 group-hover:bg-primary" />
            </div>
            <aside className="flex min-h-0 flex-col overflow-hidden bg-card max-md:fixed max-md:inset-0 max-md:z-40 md:w-[25rem] md:rounded-xl md:border md:shadow-[var(--cs-shadow)]">
              <header className="flex shrink-0 items-start gap-3 px-4 pt-4 pb-3">
                <div className="min-w-0 flex-1">
                  <h2 className="font-semibold">{panelLabels[panel]}</h2>
                  <p className="text-[0.8125rem] text-muted-foreground">
                    {panel === "connections"
                      ? "Who shop-api talked to in the last hour."
                      : panel === "changes"
                        ? `Uncommitted and unpushed work on ${gitPush.branch}.`
                        : "Robin's browser, live."}
                  </p>
                </div>
                <Button
                  variant="ghost"
                  size="icon"
                  className="rounded-full"
                  onClick={() => setPanel(null)}
                  aria-label="Close panel"
                >
                  <XIcon />
                </Button>
              </header>
              {panel === "connections" ? (
                <ConnectionsPanel />
              ) : panel === "changes" ? (
                <ChangesPanel />
              ) : (
                <div className="m-4 mt-0 grid flex-1 place-items-center rounded-lg border border-dashed text-muted-foreground">
                  Live view
                </div>
              )}
            </aside>
          </>
        )}
      </div>
    </CalmShell>
  )
}

const groupsOf: { title: string; verdicts: Connection["verdict"][] }[] = [
  { title: "Waiting for you", verdicts: ["held"] },
  { title: "Allowed", verdicts: ["allowed", "open"] },
  { title: "Blocked", verdicts: ["denied"] },
]

function ConnectionsPanel() {
  return (
    <div className="min-h-0 flex-1 overflow-y-auto px-4 pb-4">
      {groupsOf.map((g) => {
        const items = connections.filter((c) => g.verdicts.includes(c.verdict))
        return (
          <section key={g.title} className="mb-4">
            <h3 className="mb-1.5 text-xs font-medium text-muted-foreground">
              {g.title} · {items.length}
            </h3>
            {g.verdicts[0] === "held" ? (
              <div className="grid gap-2">
                {items.map((c) => (
                  <div
                    key={c.host}
                    data-status="waiting"
                    className="rounded-lg border border-(--status)/40 bg-(--status)/8 p-3"
                  >
                    <p className="font-mono text-[0.8125rem] break-all">
                      {c.host}
                      <span className="text-muted-foreground">:{c.port}</span>
                    </p>
                    <div className="mt-2 flex items-center gap-2">
                      <span className="text-xs text-muted-foreground">
                        {c.requests === 1 ? "Held" : `${c.requests} tries`} ·{" "}
                        {c.last} ago
                      </span>
                      <Button
                        variant="ghost"
                        className="ml-auto h-7 rounded-full px-3 text-xs"
                      >
                        Deny
                      </Button>
                      <Button className="h-7 rounded-full px-3 text-xs">
                        Allow
                      </Button>
                    </div>
                  </div>
                ))}
              </div>
            ) : (
              <ul className="divide-y rounded-lg border">
                {items.map((c) => (
                  <li
                    key={c.host}
                    data-status={c.verdict === "denied" ? "failed" : "running"}
                    className="flex items-center gap-2.5 px-3 py-2"
                  >
                    <span className="size-1.5 shrink-0 rounded-full bg-(--status)" />
                    <div className="min-w-0 flex-1">
                      <div className="truncate font-mono text-[0.8125rem]">
                        {c.host}
                      </div>
                      <div className="text-xs text-muted-foreground">
                        {c.requests} requests
                        {c.rule && ` · ${c.rule}`}
                      </div>
                    </div>
                    <span className="text-xs text-muted-foreground">
                      {c.last}
                    </span>
                  </li>
                ))}
              </ul>
            )}
          </section>
        )
      })}
    </div>
  )
}

function ChangesPanel() {
  const files = [
    { path: "src/routes/checkout.tsx", add: 64, del: 9 },
    { path: "src/lib/cart.ts", add: 21, del: 5 },
    { path: "src/components/order-summary.tsx", add: 43, del: 0 },
  ]
  return (
    <ul className="mx-4 divide-y rounded-lg border">
      {files.map((f) => (
        <li
          key={f.path}
          className="flex items-center gap-2 px-3 py-2 font-mono text-xs"
        >
          <span className="min-w-0 flex-1 truncate">{f.path}</span>
          <span className="text-(--term-add)">+{f.add}</span>
          <span className="text-(--st-failed)">−{f.del}</span>
        </li>
      ))}
    </ul>
  )
}

/* ---------- Dashboard ---------- */

export function CalmDashboard() {
  const facts: [string, ReactNode][] = [
    ["Template", "web-app, version 3"],
    ["Resources", "4 CPUs · 8 GiB memory"],
    ["Workspace", "6.2 of 20 GiB used"],
    ["Running for", "2 hours 14 minutes"],
  ]
  return (
    <CalmShell active={sb.name}>
      <MobileBar title={sb.name} />
      <SandboxHeader page="dashboard" />
      <div className="min-h-0 flex-1 overflow-y-auto">
        <div className="mx-auto grid max-w-[60rem] grid-cols-1 gap-10 px-5 pt-6 pb-16 md:px-10 md:pt-8">
          <PageTitle
            title="Checkout with Stripe"
            intro="Robin's backend sandbox for the shop. Two previews are open and one request waits for you."
            eyebrow="What's happening"
          />
          <dl className="grid grid-cols-2 gap-x-6 gap-y-4 md:grid-cols-4">
            {facts.map(([k, v]) => (
              <div key={k}>
                <dt className="text-xs text-muted-foreground">{k}</dt>
                <dd className="mt-0.5 font-medium">{v}</dd>
              </div>
            ))}
          </dl>
          <section>
            <SectionTitle
              title="History"
              actions={
                <Button
                  variant="ghost"
                  className="h-7 rounded-full text-[0.8125rem] text-muted-foreground"
                >
                  <BookmarkSimpleIcon /> Save checkpoint
                </Button>
              }
            />
            <div className="overflow-x-auto">
              <Lineage />
            </div>
          </section>
          <div className="grid gap-10 md:grid-cols-2">
            <section>
              <SectionTitle title="Previews" count={previews.length} />
              <ul className="divide-y rounded-2xl border bg-card">
                {previews.map((p) => (
                  <li
                    key={p.port}
                    className="flex items-center gap-3 px-4 py-3"
                  >
                    <div className="min-w-0 flex-1">
                      <div className="font-medium">{p.label}</div>
                      <div className="truncate font-mono text-xs text-muted-foreground">
                        port {p.port}
                      </div>
                    </div>
                    <Button
                      variant="outline"
                      className="h-8 rounded-full bg-card px-3 text-[0.8125rem]"
                    >
                      Open <ArrowSquareOutIcon />
                    </Button>
                  </li>
                ))}
              </ul>
            </section>
            <section>
              <SectionTitle
                title="Chats and sessions"
                count={sessions.length}
              />
              <ul className="divide-y rounded-2xl border bg-card">
                {sessions.map((s) => (
                  <li
                    key={s.name}
                    className="flex items-center gap-3 px-4 py-3"
                  >
                    {s.kind === "chat" ? (
                      <ChatsCircleIcon
                        weight="duotone"
                        className="size-5 text-(--acc-agent)"
                      />
                    ) : (
                      <TerminalWindowIcon className="size-5 text-muted-foreground" />
                    )}
                    <span className="flex-1">{s.name}</span>
                    <span className="text-xs text-muted-foreground">
                      {s.kind === "chat" ? "4 min ago" : "Terminal"}
                    </span>
                  </li>
                ))}
              </ul>
            </section>
          </div>
          <section>
            <SectionTitle
              title="Recent connections"
              actions={<KindChip tone="network" label="Network" />}
            />
            <ul className="divide-y rounded-2xl border bg-card">
              {connections.slice(0, 4).map((c) => (
                <li
                  key={c.host}
                  data-status={
                    c.verdict === "held"
                      ? "waiting"
                      : c.verdict === "denied"
                        ? "failed"
                        : "running"
                  }
                  className="flex items-center gap-3 px-4 py-2.5"
                >
                  <span className="size-2 shrink-0 rounded-full bg-(--status)" />
                  <span className="min-w-0 flex-1 truncate font-mono text-[0.8125rem]">
                    {c.host}
                  </span>
                  <span className="text-xs text-muted-foreground">
                    {verdictLabels[c.verdict]}
                  </span>
                </li>
              ))}
            </ul>
          </section>
        </div>
      </div>
    </CalmShell>
  )
}

function Lineage() {
  const points = [
    { x: 8, label: "Created", sub: "web-app 2" },
    { x: 30, label: "deps ok", sub: "checkpoint" },
    { x: 46, label: "Rebased", sub: "web-app 3" },
    { x: 66, label: "cart works", sub: "checkpoint" },
    { x: 94, label: "Now", sub: "" },
  ]
  return (
    <div className="relative h-28 min-w-[36rem] overflow-hidden rounded-2xl border bg-card px-6">
      <div className="absolute inset-x-[8%] top-10 h-0.5 rounded-full bg-foreground/15" />
      <div className="absolute top-10 right-[6%] left-[46%] h-0.5 rounded-full bg-primary/60" />
      <svg
        className="absolute top-10 left-[66%] h-12 w-[20%] overflow-visible"
        viewBox="0 0 100 48"
        preserveAspectRatio="none"
      >
        <path
          d="M0 1 C 30 1, 30 40, 60 40 L 100 40"
          fill="none"
          className="stroke-(--acc-git)"
          strokeWidth="2"
          vectorEffect="non-scaling-stroke"
        />
      </svg>
      <span className="absolute top-[4.1rem] left-[86%] -translate-x-1/2 rounded-full bg-(--acc-git)/12 px-2 text-xs whitespace-nowrap text-(--acc-git)">
        fork: shop-api-spike
      </span>
      {points.map((p) => (
        <div
          key={p.label}
          className="absolute top-10 flex -translate-x-1/2 -translate-y-1/2 flex-col items-center"
          style={{ left: `${p.x}%` }}
        >
          <span
            className={cn(
              "size-3 rounded-full border-2 bg-card",
              p.label === "Now"
                ? "size-4 border-primary bg-primary/30"
                : "border-foreground/40"
            )}
          />
          <span className="absolute top-4 text-center text-xs whitespace-nowrap">
            <span className="block font-medium">{p.label}</span>
            <span className="block text-muted-foreground">{p.sub}</span>
          </span>
        </div>
      ))}
    </div>
  )
}
