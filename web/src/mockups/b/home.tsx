import type { ReactNode } from "react"
import { Link } from "@tanstack/react-router"
import {
  ArrowRightIcon,
  CaretRightIcon,
  CheckIcon,
  PlusIcon,
} from "@phosphor-icons/react"
import { Button } from "@/components/ui/button"
import { TeammateAvatar } from "@/components/teammate-avatar"
import { cn } from "@/lib/utils"
import {
  browserAction,
  gitPush,
  groupedHosts,
  longHost,
  sandboxes,
  setupSteps,
  teammates,
} from "../data"
import {
  PresenceAvatar,
  StatusDot,
  presenceLabels,
  statusLabels,
} from "../shared"
import {
  CalmShell,
  KindChip,
  MobileBar,
  PageTitle,
  SectionTitle,
  keep,
} from "./shell"

export function CalmHome() {
  return (
    <CalmShell active="home">
      <MobileBar title="Home" />
      <div className="min-h-0 flex-1 overflow-y-auto">
        <div className="mx-auto grid max-w-[60rem] gap-10 px-5 pt-4 pb-16 md:px-10 md:pt-10">
          <PageTitle
            title="Good afternoon, Lukas"
            intro={
              <>Robin is working in shop-api. Mika needs you for one click.</>
            }
            actions={
              <Button size="lg" className="h-9 rounded-full px-4">
                <PlusIcon weight="bold" /> New sandbox
              </Button>
            }
          />
          <Setup />
          <NeedsYou />
          <People />
          <SandboxList />
        </div>
      </div>
    </CalmShell>
  )
}

function Setup() {
  const done = setupSteps.filter((s) => s.done).length
  const next = setupSteps.find((s) => !s.done)
  return (
    <section className="grid grid-cols-[auto_minmax(0,1fr)] items-center gap-x-4 gap-y-4 rounded-2xl border bg-card p-5 shadow-[var(--cs-shadow)] sm:flex sm:gap-x-6">
      <div className="relative size-12 shrink-0">
        <svg viewBox="0 0 36 36" className="size-12 -rotate-90">
          <circle
            cx="18"
            cy="18"
            r="15"
            fill="none"
            className="stroke-muted"
            strokeWidth="3.5"
          />
          <circle
            cx="18"
            cy="18"
            r="15"
            fill="none"
            className="stroke-primary"
            strokeWidth="3.5"
            strokeLinecap="round"
            strokeDasharray={`${(done / setupSteps.length) * 94.2} 94.2`}
          />
        </svg>
        <span className="absolute inset-0 flex items-center justify-center text-xs font-semibold tabular-nums">
          {done}/{setupSteps.length}
        </span>
      </div>
      <div className="min-w-0 flex-1">
        <h2 className="text-[0.9375rem] font-semibold">Finish setting up</h2>
        <p className="text-muted-foreground">
          Next: <span className="text-foreground">{next?.title}</span> —{" "}
          {next?.hint}.
        </p>
        <ol className="mt-3 flex flex-wrap gap-1.5">
          {setupSteps.map((s) => (
            <li
              key={s.title}
              className={cn(
                "inline-flex h-6 items-center gap-1 rounded-full px-2.5 text-xs",
                s.done
                  ? "bg-primary/10 text-[color-mix(in_oklch,var(--primary),var(--foreground)_25%)]"
                  : s === next
                    ? "bg-foreground text-background"
                    : "bg-muted text-muted-foreground"
              )}
            >
              {s.done && <CheckIcon weight="bold" className="size-3" />}
              {s.title}
            </li>
          ))}
        </ol>
      </div>
      <div className="col-span-2 flex items-center justify-end gap-2">
        <Button
          variant="ghost"
          className="h-8 rounded-full px-3 text-[0.8125rem]"
        >
          Later
        </Button>
        <Button className="h-8 rounded-full px-4 text-[0.8125rem]">
          Continue <ArrowRightIcon />
        </Button>
      </div>
    </section>
  )
}

function NeedsYou() {
  return (
    <section>
      <SectionTitle
        title="Needs you"
        count={4}
        actions={
          <Button
            variant="ghost"
            className="h-7 rounded-full text-[0.8125rem] text-muted-foreground"
            nativeButton={false}
            render={
              <Link
                to="/mockups/$direction/$screen"
                params={{ direction: "b", screen: "approvals" }}
                search={keep}
              />
            }
          >
            See full cards <ArrowRightIcon />
          </Button>
        }
      />
      <div className="grid gap-3 md:grid-cols-2">
        <Ask
          tone="browser"
          who={browserAction.teammate}
          text={
            <>
              wants to click{" "}
              <b className="font-semibold">“{browserAction.label}”</b> on{" "}
              {browserAction.origin}
            </>
          }
          primary="Allow"
        />
        <Ask
          tone="git"
          who={gitPush.teammate}
          text={
            <>
              wants to push 3 commits to{" "}
              <span className="font-mono text-[0.8125rem]">
                {gitPush.branch}
              </span>
            </>
          }
          primary="Review"
        />
        <Ask
          tone="network"
          who="Mika Laine"
          text={
            <>
              docs.example.com wants {groupedHosts.length} hosts while browsing
            </>
          }
          primary="Allow site"
          secondary="Review"
        />
        <Ask
          tone="network"
          who="Robin"
          text={
            <>
              wants to reach{" "}
              <span className="font-mono text-[0.8125rem] break-all">
                {longHost}
              </span>
            </>
          }
          primary="Allow"
        />
      </div>
    </section>
  )
}

function Ask({
  tone,
  who,
  text,
  primary,
  secondary = "Deny",
}: {
  tone: "network" | "git" | "browser"
  who: string
  text: ReactNode
  primary: string
  secondary?: string
}) {
  return (
    <article className="flex flex-col gap-3 rounded-2xl border bg-card p-4 shadow-[var(--cs-shadow)]">
      <div className="flex items-start gap-3">
        <TeammateAvatar name={who} className="size-8 text-[0.6875rem]" />
        <p className="min-w-0 flex-1 leading-snug">
          <span className="font-semibold">{who.split(" ")[0]}</span> {text}
        </p>
      </div>
      <div className="mt-auto flex items-center gap-2">
        <KindChip tone={tone} />
        <Button
          variant="ghost"
          className="ml-auto h-8 rounded-full px-3 text-[0.8125rem]"
        >
          {secondary}
        </Button>
        <Button className="h-8 rounded-full px-4 text-[0.8125rem]">
          {primary}
        </Button>
      </div>
    </article>
  )
}

function People() {
  return (
    <section>
      <SectionTitle
        title="Teammates"
        actions={
          <Button
            variant="ghost"
            className="h-7 rounded-full text-[0.8125rem] text-muted-foreground"
          >
            <PlusIcon /> Add teammate
          </Button>
        }
      />
      <ul className="grid gap-x-6 gap-y-5 sm:grid-cols-3">
        {teammates.map((t) => (
          <li
            key={t.id}
            className="flex items-start gap-3 [--presence-ring:var(--background)]"
          >
            <PresenceAvatar
              name={t.name}
              presence={t.presence}
              className="size-11 text-sm"
              dotClassName="size-3.5 -right-0 -bottom-0 ring-[3px]"
            />
            <div className="min-w-0">
              <div className="font-semibold">{t.name}</div>
              <div className="text-[0.8125rem] text-muted-foreground">
                {t.role}
              </div>
              <div
                data-presence={t.presence}
                className="mt-1.5 inline-flex items-center gap-1.5 text-[0.8125rem]"
              >
                <span className="size-1.5 shrink-0 rounded-full bg-(--status)" />
                <span
                  className={cn(
                    t.presence === "idle" && "text-muted-foreground"
                  )}
                >
                  {t.presence === "idle" ? presenceLabels.idle : t.activity}
                </span>
              </div>
            </div>
          </li>
        ))}
      </ul>
    </section>
  )
}

function SandboxList() {
  return (
    <section>
      <SectionTitle
        title="Sandboxes"
        count={sandboxes.length}
        actions={
          <Button
            variant="ghost"
            className="h-7 rounded-full text-[0.8125rem] text-muted-foreground"
          >
            Import
          </Button>
        }
      />
      <ul className="divide-y overflow-hidden rounded-2xl border bg-card">
        {sandboxes.map((sb) => (
          <li key={sb.id}>
            <Link
              to="/mockups/$direction/$screen"
              params={{ direction: "b", screen: "dashboard" }}
              search={keep}
              className="flex items-center gap-4 px-5 py-3.5 hover:bg-muted/40"
            >
              <StatusDot status={sb.status} className="size-2.5" />
              <div className="min-w-0 flex-1">
                <div className="font-medium">{sb.name}</div>
                <div
                  data-status={sb.status}
                  className={cn(
                    "truncate text-[0.8125rem] text-muted-foreground",
                    sb.status === "failed" && "text-(--status)"
                  )}
                >
                  {sb.note ??
                    (sb.status === "running"
                      ? `${statusLabels.running} for ${sb.uptime}`
                      : sb.uptime)}
                </div>
              </div>
              <div className="hidden gap-1.5 sm:flex">
                {sb.ports.map((p) => (
                  <span
                    key={p}
                    className="rounded-full bg-muted px-2 py-0.5 font-mono text-xs text-muted-foreground"
                  >
                    :{p}
                  </span>
                ))}
              </div>
              {sb.owner ? (
                <span className="hidden w-28 items-center gap-2 text-[0.8125rem] text-muted-foreground md:flex">
                  <TeammateAvatar
                    name={sb.owner}
                    className="size-5 text-[0.5rem]"
                  />
                  <span className="truncate">{sb.owner.split(" ")[0]}</span>
                </span>
              ) : (
                <span className="hidden w-28 text-[0.8125rem] text-muted-foreground md:block">
                  Unassigned
                </span>
              )}
              <CaretRightIcon className="size-4 text-muted-foreground" />
            </Link>
          </li>
        ))}
      </ul>
    </section>
  )
}
