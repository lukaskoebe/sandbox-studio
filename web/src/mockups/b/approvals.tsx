import { useState, type ReactNode } from "react"
import { CheckIcon } from "@phosphor-icons/react"
import { Button } from "@/components/ui/button"
import { Checkbox } from "@/components/ui/checkbox"
import { TeammateAvatar } from "@/components/teammate-avatar"
import { cn } from "@/lib/utils"
import {
  browserAction,
  gitPush,
  groupedHosts,
  longHost,
  type Tone,
} from "../data"
import { CalmShell, KindChip, MobileBar, PageTitle } from "./shell"

export function CalmApprovals() {
  return (
    <CalmShell active="network">
      <MobileBar title="Approval cards" />
      <div className="min-h-0 flex-1 overflow-y-auto">
        <div className="mx-auto grid max-w-[60rem] gap-8 px-5 pt-4 pb-16 md:px-10 md:pt-10">
          <PageTitle
            title="Approval cards"
            intro="How a teammate asks you for something. They stack in the corner of every page."
          />
          <div className="columns-1 gap-6 md:columns-2">
            <NetworkCard />
            <GitCard />
            <GroupedCard />
            <BrowserCard />
          </div>
        </div>
      </div>
    </CalmShell>
  )
}

function Card({
  tone,
  who,
  age,
  children,
  sentence,
}: {
  tone: Tone
  who: string
  age: string
  sentence: ReactNode
  children: ReactNode
}) {
  return (
    <article className="mb-6 grid max-w-[24rem] break-inside-avoid gap-4 rounded-2xl border bg-card p-5 shadow-[var(--cs-shadow)]">
      <div className="flex items-center gap-2">
        <KindChip tone={tone} />
        <span className="ml-auto text-xs text-muted-foreground">{age}</span>
      </div>
      <div className="flex items-start gap-3">
        <TeammateAvatar name={who} className="size-9 text-xs" />
        <p className="min-w-0 flex-1 text-[0.9375rem] leading-snug">
          <span className="font-semibold">{who.split(" ")[0]}</span> {sentence}
        </p>
      </div>
      {children}
    </article>
  )
}

function Buttons({
  primary,
  secondary = "Deny",
  quiet = "Not now",
}: {
  primary: string
  secondary?: string
  quiet?: string | null
}) {
  return (
    <div className="flex flex-wrap items-center gap-2">
      {quiet && (
        <Button
          variant="ghost"
          className="h-9 rounded-full px-3 text-[0.8125rem] text-muted-foreground"
        >
          {quiet}
        </Button>
      )}
      <Button
        variant="outline"
        className="ml-auto h-9 rounded-full bg-card px-4 text-[0.8125rem]"
      >
        {secondary}
      </Button>
      <Button className="h-9 rounded-full px-5 text-[0.8125rem]">
        {primary}
      </Button>
    </div>
  )
}

function NetworkCard() {
  const choices = ["This host", "*.example-cloud.com"]
  const [choice, setChoice] = useState(0)
  return (
    <Card
      tone="network"
      who="Robin"
      age="6 min ago"
      sentence="wants to connect to"
    >
      <div className="-mt-1 rounded-xl bg-muted px-3.5 py-3">
        <p className="font-mono text-[0.8125rem] leading-relaxed break-all">
          {longHost}
        </p>
        <p className="mt-1 text-xs text-muted-foreground">
          Port 443 · tried 4 times · from shop-api
        </p>
      </div>
      <div className="flex flex-wrap items-center gap-1.5 text-[0.8125rem]">
        <span className="mr-1 text-muted-foreground">Allow for</span>
        {choices.map((c, i) => (
          <button
            key={c}
            type="button"
            onClick={() => setChoice(i)}
            className={cn(
              "inline-flex h-7 max-w-full items-center gap-1 rounded-full border px-3",
              i > 0 && "font-mono text-xs",
              choice === i
                ? "border-foreground/80 bg-foreground text-background"
                : "bg-card text-muted-foreground hover:text-foreground"
            )}
          >
            {choice === i && <CheckIcon weight="bold" className="size-3" />}
            <span className="truncate">{c}</span>
          </button>
        ))}
      </div>
      <Buttons primary="Allow" />
    </Card>
  )
}

function GroupedCard() {
  const trackers = new Set([
    "www.googletagmanager.com",
    "consent.usercentrics.eu",
    "api.segment.io",
  ])
  const hosts = groupedHosts.slice(1)
  return (
    <Card
      tone="network"
      who="Mika Laine"
      age="1 min ago"
      sentence={
        <>
          opened <span className="font-medium">docs.example.com</span>, which
          wants{" "}
          <span className="font-semibold">{groupedHosts.length} hosts</span>
        </>
      }
    >
      <p className="-mt-1 text-[0.8125rem] text-muted-foreground">
        Fonts, a CDN and {trackers.size} trackers, requested together while
        loading one page.
      </p>
      <ul className="flex flex-wrap gap-1.5">
        {hosts.map((h) => (
          <li
            key={h}
            className={cn(
              "rounded-full px-2.5 py-0.5 font-mono text-xs",
              trackers.has(h)
                ? "bg-muted text-muted-foreground line-through decoration-foreground/30"
                : "bg-(--acc-network)/10 text-foreground"
            )}
            title={trackers.has(h) ? "Tracker" : undefined}
          >
            {h}
          </li>
        ))}
      </ul>
      <div className="grid gap-2">
        <Button className="h-9 w-full rounded-full text-[0.8125rem]">
          Allow all for this site
        </Button>
        <div className="flex gap-2">
          <Button
            variant="outline"
            className="h-9 flex-1 rounded-full bg-card text-[0.8125rem]"
          >
            Review individually
          </Button>
          <Button
            variant="ghost"
            className="h-9 rounded-full px-4 text-[0.8125rem] text-muted-foreground"
          >
            Deny
          </Button>
        </div>
      </div>
    </Card>
  )
}

function GitCard() {
  const total = gitPush.additions + gitPush.deletions
  return (
    <Card
      tone="git"
      who={gitPush.teammate}
      age="2 min ago"
      sentence={
        <>
          wants to push {gitPush.commits.length} commits to{" "}
          <span className="font-mono text-[0.8125rem]">{gitPush.branch}</span>
        </>
      }
    >
      <ol className="-mt-1 grid gap-1.5 border-l-2 border-(--acc-git)/40 pl-3.5 text-[0.8125rem]">
        {gitPush.commits.map((c) => (
          <li key={c.sha}>{c.message}</li>
        ))}
      </ol>
      <div className="flex items-center gap-3 text-xs text-muted-foreground">
        <span>acme/shop on GitHub</span>
        <span className="ml-auto">
          {gitPush.files} files{" "}
          <span className="text-(--term-add)">+{gitPush.additions}</span>{" "}
          <span className="text-(--st-failed)">−{gitPush.deletions}</span>
        </span>
        <span className="flex h-1.5 w-14 overflow-hidden rounded-full">
          <span
            className="bg-(--st-running)"
            style={{ width: `${(gitPush.additions / total) * 100}%` }}
          />
          <span className="flex-1 bg-(--st-failed)" />
        </span>
      </div>
      <Buttons primary="Review changes" quiet={null} />
    </Card>
  )
}

function BrowserCard() {
  return (
    <Card
      tone="browser"
      who={browserAction.teammate}
      age="Just now"
      sentence={
        <>
          wants to click{" "}
          <span className="font-semibold">“{browserAction.label}”</span> on{" "}
          {browserAction.origin}
        </>
      }
    >
      <div className="-mt-1 overflow-hidden rounded-xl border bg-muted/60">
        <div className="flex h-6 items-center gap-1 border-b px-2">
          <span className="size-1.5 rounded-full bg-foreground/20" />
          <span className="size-1.5 rounded-full bg-foreground/20" />
          <span className="ml-2 truncate font-mono text-[0.625rem] text-muted-foreground">
            {browserAction.url}
          </span>
        </div>
        <div className="grid gap-1.5 p-3">
          <span className="h-2 w-2/3 rounded-full bg-foreground/10" />
          <span className="h-2 w-1/2 rounded-full bg-foreground/10" />
          <span className="flex justify-end pt-1">
            <span className="rounded-md bg-foreground/80 px-3 py-1 text-[0.625rem] text-background ring-2 ring-(--acc-browser) ring-offset-2 ring-offset-muted">
              Place order
            </span>
          </span>
        </div>
      </div>
      <label className="flex items-center gap-2 text-[0.8125rem]">
        <Checkbox />
        Always let Mika click on this site
      </label>
      <Buttons primary="Allow" />
    </Card>
  )
}
