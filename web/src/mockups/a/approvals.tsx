import { useState, type ReactNode } from "react"
import {
  ArrowSquareOutIcon,
  CaretDownIcon,
  ShieldCheckIcon,
  XIcon,
} from "@phosphor-icons/react"
import { Button } from "@/components/ui/button"
import { Checkbox } from "@/components/ui/checkbox"
import { cn } from "@/lib/utils"
import {
  browserAction,
  environment,
  gitPush,
  groupedHosts,
  longHost,
  type Tone,
} from "../data"
import { toneLabels } from "../shared"
import { Bar, Kbd, Section, Tile, WaitingCount, WorkbenchShell } from "./shell"

export function WorkbenchApprovals() {
  return (
    <WorkbenchShell active="network">
      <Bar crumbs={[environment.slug, "approvals"]} />
      <div className="min-h-0 flex-1 overflow-y-auto p-3 md:p-5">
        <Section
          title="Approval cards"
          icon={ShieldCheckIcon}
          count={<WaitingCount n={4} />}
        >
          <p className="mb-4 max-w-prose text-muted-foreground">
            The cards as they stack in the bottom-right corner of every page, at
            their real width. Only the left rule and the tile take the kind's
            hue.
          </p>
          <div className="columns-1 gap-5 md:columns-[22rem]">
            <Specimen label="Network · long host">
              <NetworkCard />
            </Specimen>
            <Specimen label="Network · grouped (C1 §3)">
              <GroupedCard />
            </Specimen>
            <Specimen label="Git push">
              <GitCard />
            </Specimen>
            <Specimen label="Browser action">
              <BrowserCard />
            </Specimen>
          </div>
        </Section>
      </div>
    </WorkbenchShell>
  )
}

function Specimen({ label, children }: { label: string; children: ReactNode }) {
  return (
    <figure className="mb-5 w-full max-w-[22rem] break-inside-avoid">
      <figcaption className="mb-1.5 font-mono text-[0.625rem] tracking-wider text-muted-foreground uppercase">
        {label}
      </figcaption>
      {children}
    </figure>
  )
}

function Card({
  tone,
  kind,
  source,
  age,
  children,
}: {
  tone: Tone
  kind?: string
  source: string
  age: string
  children: ReactNode
}) {
  return (
    <article
      data-tone={tone}
      className="overflow-hidden rounded-(--radius) border border-l-[3px] border-l-(--tone) bg-popover text-xs shadow-[0_8px_24px_-12px_oklch(0_0_0/0.25)]"
    >
      <header className="flex h-8 items-center gap-2 border-b bg-(--tone)/[0.05] px-2.5">
        <Tile tone={tone} className="size-[1.125rem]" />
        <span className="font-mono text-[0.625rem] font-semibold tracking-wider text-(--tone) uppercase">
          {kind ?? toneLabels[tone]}
        </span>
        <span className="min-w-0 truncate font-mono text-[0.625rem] text-muted-foreground">
          {source}
        </span>
        <span className="ml-auto font-mono text-[0.625rem] text-muted-foreground">
          {age}
        </span>
        <button
          type="button"
          className="text-muted-foreground hover:text-foreground"
          aria-label="Hide"
        >
          <XIcon className="size-3" />
        </button>
      </header>
      <div className="grid gap-2.5 p-2.5">{children}</div>
    </article>
  )
}

function Actions({ children }: { children: ReactNode }) {
  return <div className="flex items-center gap-1">{children}</div>
}

function Allow({ label = "Allow" }: { label?: string }) {
  return (
    <Button size="sm" className="rounded-[4px]">
      {label}
      <span className="rounded-[2px] bg-primary-foreground/20 px-1 font-mono text-[0.5625rem]">
        A
      </span>
    </Button>
  )
}

function Deny() {
  return (
    <Button variant="outline" size="sm" className="rounded-[4px]">
      Deny
      <span className="font-mono text-[0.5625rem] text-muted-foreground">
        D
      </span>
    </Button>
  )
}

function NetworkCard() {
  const patterns = [`${longHost}`, "*.example-cloud.com"]
  const [pattern, setPattern] = useState(0)
  return (
    <Card tone="network" source="shop-api" age="6m">
      <div>
        <p className="font-mono text-[0.8125rem] leading-snug font-medium break-all">
          {longHost}
          <span className="text-muted-foreground">:443</span>
        </p>
        <p className="mt-1 font-mono text-[0.625rem] text-muted-foreground">
          4 attempts · tls · first by node
        </p>
      </div>
      <div className="grid gap-1">
        <span className="font-mono text-[0.625rem] tracking-wider text-muted-foreground uppercase">
          Rule
        </span>
        <div className="grid grid-cols-[minmax(0,1fr)_auto] overflow-hidden rounded-[4px] border font-mono text-[0.6875rem]">
          {patterns.map((p, i) => (
            <button
              key={p}
              type="button"
              onClick={() => setPattern(i)}
              className={cn(
                "truncate px-2 py-1 text-left",
                i > 0 && "border-l",
                pattern === i
                  ? "bg-(--tone)/10 text-foreground"
                  : "text-muted-foreground hover:bg-muted"
              )}
              title={p}
            >
              {p}
            </button>
          ))}
        </div>
      </div>
      <Actions>
        <Button variant="ghost" size="xs" className="rounded-[3px]">
          Ignore
        </Button>
        <Button variant="ghost" size="xs" className="ml-auto rounded-[3px]">
          Options <CaretDownIcon />
        </Button>
        <Deny />
        <Allow />
      </Actions>
    </Card>
  )
}

function GroupedCard() {
  const trackers = new Set([
    "www.googletagmanager.com",
    "consent.usercentrics.eu",
    "api.segment.io",
  ])
  return (
    <Card tone="network" kind="Network ×9" source="shop-web · browser" age="1m">
      <div>
        <p className="text-[0.8125rem] font-semibold">
          <span className="font-mono">docs.example.com</span> wants{" "}
          {groupedHosts.length} hosts
        </p>
        <p className="mt-0.5 text-muted-foreground">
          Requested together while loading{" "}
          <span className="font-mono">/guides/payments</span>
        </p>
      </div>
      <ul className="grid gap-px overflow-hidden rounded-[4px] border bg-border font-mono text-[0.6875rem]">
        {groupedHosts.slice(1, 7).map((h) => (
          <li
            key={h}
            className="flex items-center gap-2 bg-popover px-2 py-0.5"
          >
            <span className="min-w-0 flex-1 truncate">{h}</span>
            {trackers.has(h) && (
              <span className="text-[0.5625rem] text-muted-foreground uppercase">
                tracker
              </span>
            )}
          </li>
        ))}
        <li className="bg-popover px-2 py-0.5 text-muted-foreground">
          + 2 more
        </li>
      </ul>
      <Actions>
        <Deny />
        <Button variant="ghost" size="sm" className="ml-auto rounded-[4px]">
          Review each
        </Button>
        <Allow label="Allow site" />
      </Actions>
    </Card>
  )
}

function GitCard() {
  const total = gitPush.additions + gitPush.deletions
  return (
    <Card tone="git" kind="Git push" source="shop-api · Robin" age="2m">
      <div>
        <p className="text-[0.8125rem] font-semibold">
          {gitPush.commits.length} commits →{" "}
          <span className="font-mono">{gitPush.branch}</span>
        </p>
        <p className="mt-0.5 font-mono text-[0.6875rem] text-muted-foreground">
          {gitPush.remote}
        </p>
      </div>
      <ul className="grid gap-0.5 font-mono text-[0.6875rem]">
        {gitPush.commits.map((c) => (
          <li key={c.sha} className="flex gap-2">
            <span className="text-(--tone)">{c.sha}</span>
            <span className="truncate">{c.message}</span>
          </li>
        ))}
      </ul>
      <div className="flex items-center gap-2 font-mono text-[0.6875rem] text-muted-foreground">
        <span>{gitPush.files} files</span>
        <span className="text-(--term-add)">+{gitPush.additions}</span>
        <span className="text-(--st-failed)">−{gitPush.deletions}</span>
        <span className="ml-auto flex h-1.5 w-20 overflow-hidden rounded-[1px]">
          <span
            className="bg-(--st-running)"
            style={{ width: `${(gitPush.additions / total) * 100}%` }}
          />
          <span className="flex-1 bg-(--st-failed)" />
        </span>
      </div>
      <Actions>
        <Deny />
        <Button size="sm" className="ml-auto rounded-[4px]">
          Review diff <ArrowSquareOutIcon />
        </Button>
      </Actions>
    </Card>
  )
}

function BrowserCard() {
  return (
    <Card tone="browser" source="shop-web · Mika Laine" age="now">
      <div>
        <p className="text-[0.8125rem] font-semibold">
          <span className="font-mono text-(--tone)">
            {browserAction.action}
          </span>{" "}
          “{browserAction.label}”
        </p>
        <p className="mt-0.5 truncate font-mono text-[0.6875rem] text-muted-foreground">
          {browserAction.url}
        </p>
      </div>
      <label className="flex items-center gap-2">
        <Checkbox />
        Always allow <span className="font-mono">click</span> on{" "}
        <span className="font-mono">{browserAction.origin}</span>
      </label>
      <Actions>
        <Button variant="ghost" size="xs" className="rounded-[3px]">
          Ignore
        </Button>
        <span className="ml-auto" />
        <Deny />
        <Allow />
      </Actions>
      <p className="flex items-center gap-1 border-t pt-2 text-[0.625rem] text-muted-foreground">
        <Kbd>A</Kbd> allow <Kbd>D</Kbd> deny <Kbd>J</Kbd>
        <Kbd>K</Kbd> next card
      </p>
    </Card>
  )
}
