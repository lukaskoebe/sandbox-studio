import type { ReactNode } from "react"
import { Link } from "@tanstack/react-router"
import {
  ArrowRightIcon,
  CheckIcon,
  CubeIcon,
  ListChecksIcon,
  PlusIcon,
  ShieldCheckIcon,
  UsersThreeIcon,
} from "@phosphor-icons/react"
import { Button } from "@/components/ui/button"
import { TeammateAvatar } from "@/components/teammate-avatar"
import { cn } from "@/lib/utils"
import {
  environment,
  gitPush,
  groupedHosts,
  longHost,
  sandboxes,
  setupSteps,
  teammates,
  type Tone,
} from "../data"
import {
  StatusDot,
  presenceLabels,
  statusLabels,
  type MockupSearch,
} from "../shared"
import {
  Bar,
  Meter,
  Mono,
  SearchBox,
  Section,
  Tile,
  WaitingCount,
  WorkbenchShell,
} from "./shell"

const keep = (prev: MockupSearch) => ({ theme: prev.theme, bare: prev.bare })

export function WorkbenchHome() {
  return (
    <WorkbenchShell active="home">
      <Bar crumbs={[environment.slug, "home"]}>
        <SearchBox />
        <Button size="sm" className="rounded-[4px]">
          <PlusIcon weight="bold" />
          <span className="max-sm:hidden">New sandbox</span>
        </Button>
      </Bar>
      <div className="min-h-0 flex-1 overflow-y-auto">
        <Stats />
        <div className="grid gap-6 p-3 md:p-4 xl:grid-cols-[minmax(0,1fr)_22rem]">
          <div className="grid min-w-0 content-start gap-6">
            <SandboxTable />
            <Activity />
          </div>
          <div className="grid min-w-0 content-start gap-6">
            <Approvals />
            <Teammates />
            <Setup />
          </div>
        </div>
      </div>
    </WorkbenchShell>
  )
}

function Stats() {
  const items = [
    { label: "Sandboxes", value: "2", rest: "/ 4 running" },
    { label: "Approvals", value: "4", rest: "waiting", waiting: true },
    { label: "Teammates", value: "1", rest: "working · 1 waiting" },
    { label: "Egress", value: "312", rest: "req / h · 2 held" },
    { label: "Host", value: "7.4", rest: "/ 16 GiB in use" },
  ]
  return (
    <div className="grid grid-cols-2 border-b bg-[linear-gradient(var(--wb-grid)_1px,transparent_1px),linear-gradient(90deg,var(--wb-grid)_1px,transparent_1px)] bg-size-[12px_12px] sm:grid-cols-3 lg:grid-cols-5">
      {items.map((s) => (
        <div
          key={s.label}
          className="border-r border-b px-4 py-2.5 last:border-r-0 lg:border-b-0"
        >
          <div className="font-mono text-[0.625rem] tracking-wider text-muted-foreground uppercase">
            {s.label}
          </div>
          <div className="mt-0.5 flex items-baseline gap-1.5">
            <span
              data-status={s.waiting ? "waiting" : undefined}
              className={cn(
                "font-mono text-lg leading-6 font-semibold",
                s.waiting &&
                  "text-[color-mix(in_oklch,var(--status),var(--foreground)_30%)]"
              )}
            >
              {s.value}
            </span>
            <span className="truncate text-muted-foreground">{s.rest}</span>
          </div>
        </div>
      ))}
    </div>
  )
}

function SandboxTable() {
  return (
    <Section
      title="Sandboxes"
      icon={CubeIcon}
      count={sandboxes.length}
      actions={
        <>
          <Button variant="ghost" size="xs" className="rounded-[3px]">
            Import
          </Button>
          <Button variant="outline" size="xs" className="rounded-[3px]">
            <PlusIcon /> New
          </Button>
        </>
      }
    >
      <div className="overflow-hidden rounded-(--radius) border bg-card">
        <div className="grid h-7 grid-cols-[1rem_minmax(9rem,1.4fr)_minmax(6rem,1fr)_6.5rem_7.5rem_minmax(5rem,0.8fr)_2rem] items-center gap-3 border-b bg-muted/50 px-3 font-mono text-[0.625rem] tracking-wider text-muted-foreground uppercase max-lg:hidden">
          <span />
          <span>Name</span>
          <span>Owner</span>
          <span>Template</span>
          <span>CPU · Mem</span>
          <span>Ports</span>
          <span className="text-right">Ask</span>
        </div>
        {sandboxes.map((sb) => (
          <Link
            key={sb.id}
            to="/mockups/$direction/$screen"
            params={{ direction: "a", screen: "dashboard" }}
            search={keep}
            className="grid grid-cols-[1rem_minmax(0,1fr)_auto] items-center gap-x-3 gap-y-0.5 border-b px-3 py-1.5 last:border-b-0 hover:bg-muted/40 lg:h-(--wb-row) lg:grid-cols-[1rem_minmax(9rem,1.4fr)_minmax(6rem,1fr)_6.5rem_7.5rem_minmax(5rem,0.8fr)_2rem] lg:py-0"
          >
            <StatusDot status={sb.status} />
            <span className="flex min-w-0 items-baseline gap-2">
              <span className="truncate font-mono font-medium">{sb.name}</span>
              <span className="truncate font-mono text-[0.625rem] text-muted-foreground max-sm:hidden">
                {sb.id}
              </span>
            </span>
            <span className="flex min-w-0 items-center gap-1.5 text-muted-foreground max-lg:col-start-2 max-lg:row-start-2">
              {sb.owner ? (
                <>
                  <TeammateAvatar
                    name={sb.owner}
                    className="size-4 rounded-[3px] text-[0.5rem]"
                  />
                  <span className="truncate">{sb.owner}</span>
                </>
              ) : (
                <span className="text-muted-foreground/60">—</span>
              )}
              <span className="lg:hidden">·</span>
              <span
                data-status={sb.status}
                className={cn(
                  "truncate lg:hidden",
                  sb.status === "failed" && "text-(--status)"
                )}
              >
                {sb.note ?? statusLabels[sb.status]}
              </span>
            </span>
            <span className="max-lg:hidden">
              <Mono>{sb.template}</Mono>
            </span>
            <span className="flex items-center gap-2 font-mono text-[0.6875rem] text-muted-foreground max-lg:hidden">
              {sb.status === "running" || sb.status === "waiting" ? (
                <>
                  <Meter pct={sb.cpuPct} className="w-6" />
                  <span className="w-7 text-right">{sb.cpuPct}%</span>
                  <Meter
                    pct={(sb.memUsedGiB / sb.memGiB) * 100}
                    className="w-6"
                  />
                </>
              ) : (
                <span
                  data-status={sb.status}
                  className={cn(sb.status === "failed" && "text-(--status)")}
                >
                  {sb.uptime}
                </span>
              )}
            </span>
            <span className="flex gap-1 max-lg:hidden">
              {sb.ports.map((p) => (
                <Mono key={p}>:{p}</Mono>
              ))}
            </span>
            <span className="row-span-2 flex justify-end lg:row-span-1">
              <WaitingCount n={sb.approvals} />
            </span>
          </Link>
        ))}
      </div>
    </Section>
  )
}

const activity: {
  tone: Tone
  what: ReactNode
  who: string
  when: string
}[] = [
  {
    tone: "network",
    what: (
      <>
        Allowed <span className="font-mono">registry.npmjs.org</span> by curated
        list
      </>
    ),
    who: "shop-api",
    when: "40 s",
  },
  {
    tone: "git",
    what: (
      <>
        Pushed 2 commits to <span className="font-mono">main</span> on{" "}
        <span className="font-mono">github/acme/docs</span>
      </>
    ),
    who: "Juno",
    when: "1 h",
  },
  {
    tone: "memory",
    what: "Shared “Stripe test keys live in the vault as STRIPE_TEST”",
    who: "Robin",
    when: "2 h",
  },
  {
    tone: "agent",
    what: "Started chat “Checkout with Stripe”",
    who: "Robin",
    when: "2 h",
  },
  {
    tone: "browser",
    what: (
      <>
        Filled <span className="font-mono">SHOP_ADMIN</span> on{" "}
        <span className="font-mono">admin.shop.example.com</span>
      </>
    ),
    who: "Mika Laine",
    when: "3 h",
  },
]

function Activity() {
  return (
    <Section
      title="Activity"
      icon={ListChecksIcon}
      actions={
        <Button variant="ghost" size="xs" className="rounded-[3px]">
          All activity <ArrowRightIcon />
        </Button>
      }
    >
      <ol className="relative ml-2.5 border-l">
        {activity.map((a, i) => (
          <li key={i} className="relative flex items-center gap-2 py-1.5 pl-5">
            <Tile
              tone={a.tone}
              className="absolute -left-2.5 bg-background ring-3 ring-background"
            />
            <span className="min-w-0 flex-1 truncate">{a.what}</span>
            <span className="shrink-0 text-muted-foreground max-sm:hidden">
              {a.who}
            </span>
            <span className="w-8 shrink-0 text-right font-mono text-[0.6875rem] text-muted-foreground">
              {a.when}
            </span>
          </li>
        ))}
      </ol>
    </Section>
  )
}

function Approvals() {
  const rows: {
    tone: Tone
    title: ReactNode
    meta: string
    review?: boolean
  }[] = [
    {
      tone: "network",
      title: <span className="font-mono break-all">{longHost}:443</span>,
      meta: "shop-api · 6 min · 4 attempts",
    },
    {
      tone: "network",
      title: (
        <>
          <span className="font-mono">docs.example.com</span> wants{" "}
          {groupedHosts.length} hosts
        </>
      ),
      meta: "shop-web · browser · 1 min",
      review: true,
    },
    {
      tone: "git",
      title: (
        <>
          Push 3 commits to <span className="font-mono">{gitPush.branch}</span>
        </>
      ),
      meta: `${gitPush.remote} · +${gitPush.additions} −${gitPush.deletions}`,
      review: true,
    },
    {
      tone: "browser",
      title: "click “Place order” on shop.example.com",
      meta: "Mika Laine · shop-web · now",
    },
  ]
  return (
    <Section
      title="Approvals"
      icon={ShieldCheckIcon}
      count={<WaitingCount n={rows.length} />}
      actions={
        <Button
          variant="ghost"
          size="xs"
          className="rounded-[3px]"
          nativeButton={false}
          render={
            <Link
              to="/mockups/$direction/$screen"
              params={{ direction: "a", screen: "approvals" }}
              search={keep}
            />
          }
        >
          Open cards <ArrowRightIcon />
        </Button>
      }
    >
      <ul className="overflow-hidden rounded-(--radius) border bg-card">
        {rows.map((r, i) => (
          <li
            key={i}
            data-tone={r.tone}
            className="grid grid-cols-[auto_minmax(0,1fr)] gap-x-2.5 border-b border-l-2 border-l-(--tone) px-2.5 py-2 last:border-b-0"
          >
            <Tile tone={r.tone} className="mt-px" />
            <div className="min-w-0">
              <div className="leading-5 font-medium">{r.title}</div>
              <div className="mt-0.5 flex items-center gap-1">
                <span className="min-w-0 flex-1 truncate font-mono text-[0.625rem] text-muted-foreground">
                  {r.meta}
                </span>
                {r.review ? (
                  <Button variant="outline" size="xs" className="rounded-[3px]">
                    Review
                  </Button>
                ) : (
                  <>
                    <Button variant="ghost" size="xs" className="rounded-[3px]">
                      Deny
                    </Button>
                    <Button size="xs" className="rounded-[3px]">
                      Allow
                    </Button>
                  </>
                )}
              </div>
            </div>
          </li>
        ))}
      </ul>
    </Section>
  )
}

function Teammates() {
  return (
    <Section
      title="Teammates"
      icon={UsersThreeIcon}
      count={teammates.length}
      actions={
        <Button variant="ghost" size="icon-xs" className="rounded-[3px]">
          <PlusIcon />
        </Button>
      }
    >
      <ul className="overflow-hidden rounded-(--radius) border bg-card">
        {teammates.map((t) => (
          <li
            key={t.id}
            className="flex items-center gap-2.5 border-b px-2.5 py-2 last:border-b-0"
          >
            <TeammateAvatar
              name={t.name}
              className="size-6 rounded-[4px] text-[0.5625rem]"
            />
            <div className="min-w-0 flex-1">
              <div className="flex items-center gap-1.5">
                <span className="font-medium">{t.name}</span>
                <span
                  data-presence={t.presence}
                  className="inline-flex items-center gap-1 font-mono text-[0.625rem] text-muted-foreground"
                >
                  <span className="size-1.5 rounded-full bg-(--status)" />
                  {presenceLabels[t.presence].toLowerCase()}
                </span>
              </div>
              <div className="truncate text-muted-foreground">{t.activity}</div>
            </div>
            {t.sandbox && <Mono className="max-sm:hidden">{t.sandbox}</Mono>}
          </li>
        ))}
      </ul>
    </Section>
  )
}

function Setup() {
  const done = setupSteps.filter((s) => s.done).length
  const next = setupSteps.filter((s) => !s.done)
  return (
    <div className="rounded-(--radius) border border-dashed border-primary/40 bg-primary/[0.035] p-3">
      <div className="flex items-center gap-2">
        <Tile icon={CheckIcon} className="bg-primary/15 text-primary" />
        <span className="text-[0.8125rem] font-semibold">Finish setup</span>
        <span className="ml-auto font-mono text-[0.6875rem] text-muted-foreground">
          {done}/{setupSteps.length}
        </span>
      </div>
      <div className="mt-2.5 flex gap-0.5">
        {setupSteps.map((s) => (
          <span
            key={s.title}
            title={s.title}
            className={cn(
              "h-1 flex-1 rounded-[1px]",
              s.done ? "bg-primary" : "bg-foreground/10"
            )}
          />
        ))}
      </div>
      <ul className="mt-2.5 grid gap-1">
        {next.map((s, i) => (
          <li key={s.title} className="flex items-center gap-2">
            <span className="flex size-4 items-center justify-center rounded-full border font-mono text-[0.5625rem] text-muted-foreground">
              {setupSteps.indexOf(s) + 1}
            </span>
            <span className="font-medium">{s.title}</span>
            <span className="truncate text-muted-foreground">{s.hint}</span>
            {i === 0 && (
              <Button size="xs" className="ml-auto rounded-[3px]">
                Continue
              </Button>
            )}
          </li>
        ))}
      </ul>
    </div>
  )
}
