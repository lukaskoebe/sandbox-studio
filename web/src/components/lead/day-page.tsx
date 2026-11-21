import { useState, type ReactNode } from "react"
import { Link } from "@tanstack/react-router"
import { useQueryClient } from "@tanstack/react-query"
import { toast } from "sonner"
import {
  ArrowClockwiseIcon,
  CaretLeftIcon,
  CaretRightIcon,
  CopyIcon,
  GearIcon,
  WarningIcon,
} from "@phosphor-icons/react"
import {
  ApprovalCardList,
  useApprovalReview,
} from "@/components/approval-stack"
import { TeammateAvatar } from "@/components/teammate-avatar"
import { Button } from "@/components/ui/button"
import { ScrollArea } from "@/components/ui/scroll-area"
import { Spinner } from "@/components/ui/spinner"
import { usePendingApprovals } from "@/hooks/use-pending-approvals"
import { $api, errorMessage } from "@/lib/api/client"
import { useClaimApprovals } from "@/lib/approval-claims"
import { approvalCards } from "@/lib/approval-groups"
import { approvalKind } from "@/lib/approval-kinds"
import { copyText } from "@/lib/clipboard"
import {
  clock,
  dayName,
  dollars,
  plural,
  leadToday,
  plainText,
  refTargets,
  turnTime,
  type LeadDay,
  type LeadPush,
  type LeadTeammate,
} from "@/lib/lead"
import { cn } from "@/lib/utils"
import { AskComposer, LeadAsks, useLeadSeen } from "./ask"
import { LeadGlyph } from "./glyph"
import { Prose } from "./prose"

/**
 * One day as Lead wrote it (docs/lead.md): what waits for the user, each teammate's finished,
 * open and blocked work, what shipped, what is in the way, what it cost, and suggestions for
 * the next day. Every number, state and link comes from Studio's records; only the sentences
 * are the model's. Today before it is written shows the facts so far.
 */
export function LeadDayPage({ env, day }: { env: string; day: string }) {
  const lead = $api.useQuery("get", "/api/environments/{env}/lead", {
    params: { path: { env } },
  })
  const page = $api.useQuery("get", "/api/environments/{env}/lead/days/{day}", {
    params: { path: { env, day } },
  })
  const v = page.data
  const name = lead.data?.settings.name ?? "Lead"
  const isToday = !!lead.data && day === leadToday(lead.data)
  useLeadSeen(env, isToday)

  return (
    <>
      <header data-slot="page-header" className="shrink-0 px-4 md:px-6">
        <div className="mx-auto flex min-h-14 w-full max-w-5xl flex-wrap items-center gap-x-2 gap-y-1 py-2">
          <LeadGlyph className="size-5" />
          <h1 className="min-w-0 flex-1 basis-40 text-sm font-medium text-balance">
            {dayName(day)}
            {v?.prose?.heading && (
              <span className="font-normal text-muted-foreground">
                {" "}
                · {v.prose.heading}
              </span>
            )}
          </h1>
          <div className="ml-auto flex shrink-0 items-center gap-1">
            <DayArrows env={env} prev={v?.prev} next={v?.next} />
            {v && <CopyButton markdown={v.markdown} />}
            <SummarizeButton env={env} writing={!!lead.data?.writing} />
            <Button
              size="icon-sm"
              variant="ghost"
              aria-label={`${name}'s settings`}
              title={`${name}'s settings`}
              render={<Link to="/e/$env/lead/settings" params={{ env }} />}
            >
              <GearIcon />
            </Button>
          </div>
        </div>
      </header>
      <ScrollArea
        className="min-h-0 flex-1"
        viewportClassName="px-4 pb-12 md:px-6"
      >
        <div className="mx-auto grid w-full max-w-5xl grid-cols-1 content-start gap-5">
          {page.isPending ? (
            <p className="flex items-center gap-2 text-muted-foreground">
              <Spinner /> Reading the day…
            </p>
          ) : page.error ? (
            <p role="alert" className="text-destructive">
              {page.error.status === 404
                ? `${name} wrote nothing on ${dayName(day)}.`
                : errorMessage(page.error)}
            </p>
          ) : (
            v && (
              <Day
                env={env}
                v={v}
                lead={name}
                today={leadToday(lead.data)}
                showCosts={lead.data?.settings.showCosts ?? true}
              />
            )
          )}
          {v && <LeadAsks env={env} day={day} name={name} />}
        </div>
      </ScrollArea>
      {v && (
        <AskComposer
          env={env}
          day={day}
          name={name}
          about={isToday ? "today" : dayName(day)}
        />
      )}
    </>
  )
}

function Day({
  env,
  v,
  lead,
  today,
  showCosts,
}: {
  env: string
  v: LeadDay
  lead: string
  /** Studio's today. */
  today: string
  showCosts: boolean
}) {
  const f = v.facts
  const refs = refTargets(f)
  const teammates = f.teammates ?? []
  const pushes = f.pushes ?? []
  const blockers = f.blockers ?? []
  const lines = new Map((v.prose?.teammates ?? []).map((t) => [t.ref, t.lines]))
  const isToday = v.day === today
  // Today's count is of the requests waiting now, the cards below; a past day's is of those
  // that waited when it was written.
  const pending = usePendingApprovals()
  const waiting = isToday
    ? (pending.data ?? []).filter((a) => a.environmentId === env).length
    : (f.waiting?.length ?? 0)
  const merged = pushes.filter((p) => p.prUrl).length
  // What the agents reported, and what Lead's own calls cost; settings can hide both.
  const costs = f.costed || v.leadCost > 0
  return (
    <>
      <p className="text-caption text-muted-foreground">
        {v.written ? (
          <>
            written {v.writtenAt && clock(v.writtenAt)}
            {v.trigger === "now" && " on request"}
            {v.model && ` · ${v.model}`}
            {v.priced && v.cost > 0 && ` · ${dollars(v.cost)}`}
            {" · "}
          </>
        ) : null}
        {plural(teammates.length, "teammate", "teammates")},{" "}
        {plural(f.computers, "computer", "computers")}
      </p>
      {v.note && (
        <p
          className={cn(
            "flex items-start gap-2 rounded-(--radius) border px-3 py-2 text-xs",
            v.written ? "border-(--st-waiting)/40" : "border-dashed"
          )}
        >
          {v.written && (
            <WarningIcon className="mt-0.5 size-3.5 shrink-0 text-(--st-waiting-fg)" />
          )}
          <span>{v.note}</span>
        </p>
      )}
      {v.prose?.summary && (
        <p className="max-w-3xl text-sm/relaxed">
          <Prose env={env} text={v.prose.summary} refs={refs} />
        </p>
      )}
      <dl className="grid grid-cols-2 overflow-hidden rounded-(--radius) border sm:grid-cols-4">
        <Number
          value={String(waiting)}
          label={isToday ? "need you" : "needed you"}
          tone={waiting > 0 ? "waiting" : undefined}
        />
        <Number
          value={`${pushes.length}${merged ? ` · ${merged}` : ""}`}
          label={merged ? "pushes · with a PR" : "pushes"}
        />
        <Number
          value={turnTime(f.turnSeconds)}
          label={`turns across ${plural(f.threads, "thread", "threads")}`}
        />
        <Number
          value={costs ? dollars(f.cost + v.leadCost) : "—"}
          label={
            f.costed
              ? `reported, ${lead} included`
              : costs
                ? `${lead}'s calls; no agent reported a cost`
                : showCosts
                  ? "no cost reported"
                  : "costs are hidden in the settings"
          }
        />
      </dl>
      <div className="grid grid-cols-1 items-start gap-5 lg:grid-cols-[minmax(0,1fr)_20rem]">
        <div className="grid min-w-0 gap-5">
          {isToday ? (
            <WaitingNow env={env} />
          ) : (
            (f.waiting?.length ?? 0) > 0 && (
              <Section title="Needed you">
                <ul className="divide-y rounded-(--radius) border">
                  {(f.waiting ?? []).map((w) => (
                    <li key={w.id} className="px-3 py-2 text-xs">
                      <span className="font-medium">{w.subject}</span>
                      <span className="block text-caption text-muted-foreground">
                        {kindTitle(w.kind)}
                        {w.sandboxName && ` · ${w.sandboxName}`} · since{" "}
                        {clock(w.since)}
                      </span>
                    </li>
                  ))}
                </ul>
              </Section>
            )
          )}
          {teammates.length === 0 ? (
            <p className="rounded-(--radius) border border-dashed p-5 text-muted-foreground">
              Nobody had a turn on this day.
            </p>
          ) : (
            <div className="divide-y rounded-(--radius) border">
              {teammates.map((t) => (
                <TeammateBlock
                  key={t.ref}
                  env={env}
                  t={t}
                  lines={lines.get(t.ref)}
                  refs={refs}
                  costed={f.costed}
                />
              ))}
            </div>
          )}
        </div>
        <div className="grid min-w-0 gap-5">
          {pushes.length > 0 && (
            <Section title="Shipped">
              <ul className="divide-y rounded-(--radius) border">
                {pushes.map((p) => (
                  <PushRow key={p.id} p={p} />
                ))}
              </ul>
            </Section>
          )}
          {blockers.length > 0 && (
            <Section title="Blockers">
              <ul className="grid gap-2 text-xs">
                {blockers.map((b, i) => (
                  <li key={i} className="flex items-start gap-2">
                    <WarningIcon className="mt-0.5 size-3.5 shrink-0 text-(--st-failed)" />
                    <span>{b.text}</span>
                  </li>
                ))}
              </ul>
            </Section>
          )}
          {costs && (
            <Section title="Costs" aside={dollars(f.cost + v.leadCost)}>
              <dl className="grid grid-cols-[1fr_auto] gap-x-3 gap-y-1 text-xs">
                {teammates
                  .filter((t) => t.costed)
                  .map((t) => (
                    <CostRow key={t.ref} label={t.name} value={t.cost} />
                  ))}
                {v.leadCost > 0 && <CostRow label={lead} value={v.leadCost} />}
              </dl>
              <p className="text-caption text-muted-foreground">
                What the agents reported their sessions cost, and what Lead's
                calls cost.
              </p>
            </Section>
          )}
          {(v.prose?.tomorrow?.length ?? 0) > 0 && (
            <Section title="Tomorrow">
              <ul className="grid gap-2 text-xs/relaxed">
                {(v.prose?.tomorrow ?? []).map((s, i) => (
                  <li
                    key={i}
                    className="flex items-start justify-between gap-2"
                  >
                    <span className="min-w-0">
                      <Prose env={env} text={s} refs={refs} />
                    </span>
                    <Button
                      size="xs"
                      variant="ghost"
                      className="shrink-0 text-muted-foreground"
                      render={
                        <Link
                          to="/e/$env/lead/plans/new"
                          params={{ env }}
                          search={{ text: plainText(s, refs) }}
                        />
                      }
                    >
                      Make a plan
                    </Button>
                  </li>
                ))}
              </ul>
              <p className="text-caption text-muted-foreground">
                Suggestions only. Make a plan turns one into messages to
                teammates, sent once you approve them.
              </p>
            </Section>
          )}
        </div>
      </div>
    </>
  )
}

/** Today's approvals, answered in place as on Home. */
function WaitingNow({ env }: { env: string }) {
  const [split, setSplit] = useState<ReadonlySet<string>>(() => new Set())
  const pending = usePendingApprovals()
  const envs = $api.useQuery("get", "/api/environments")
  const approvals = (pending.data ?? []).filter((a) => a.environmentId === env)
  useClaimApprovals(approvals.map((a) => a.id))
  const { sheet, review } = useApprovalReview()
  const cards = approvalCards(approvals, split)
  if (cards.length === 0) return null
  return (
    <Section title="Needs you">
      <div className="grid gap-2.5">
        <ApprovalCardList
          cards={cards}
          max={cards.length}
          envName={() => envs.data?.find((e) => e.id === env)?.name}
          onReview={review}
          onSplit={(ids) => setSplit((prev) => new Set([...prev, ...ids]))}
        />
      </div>
      {sheet}
    </Section>
  )
}

const states = {
  done: "text-(--st-running)",
  open: "text-muted-foreground",
  blocked: "text-(--st-failed)",
} as const

function TeammateBlock({
  env,
  t,
  lines,
  refs,
  costed,
}: {
  env: string
  t: LeadTeammate
  lines?: { state: "done" | "open" | "blocked"; text: string }[] | null
  refs: ReturnType<typeof refTargets>
  costed: boolean
}) {
  const threads = t.threads ?? []
  const computers = t.computers ?? []
  return (
    <section aria-label={t.name} className="grid gap-2 p-3">
      <header className="flex flex-wrap items-baseline gap-x-2 gap-y-0.5">
        <TeammateAvatar
          name={t.name}
          avatar={t.avatar}
          className="size-5 self-center text-[0.5625rem]"
        />
        {t.id ? (
          <Link
            to="/e/$env/teammates/$id"
            params={{ env, id: t.id }}
            className="text-sm font-medium hover:underline"
          >
            {t.name}
          </Link>
        ) : (
          <span className="text-sm font-medium">{t.name}</span>
        )}
        <span className="text-caption text-muted-foreground">
          {plural(threads.length, "thread", "threads")}
          {computers.length > 0 && ` · ${computers.join(", ")}`}
        </span>
        <span className="ml-auto font-mono text-meta text-muted-foreground">
          {turnTime(t.turnSeconds)}
          {costed && t.costed && ` · ${dollars(t.cost)}`}
        </span>
      </header>
      {lines && lines.length > 0 ? (
        <ul className="grid gap-1.5 pl-7 text-xs/relaxed">
          {lines.map((l, i) => (
            <li key={i} className="grid grid-cols-[4.5rem_1fr] gap-2">
              <span
                className={cn(
                  "font-mono text-meta tracking-wide uppercase",
                  states[l.state]
                )}
              >
                {l.state}
              </span>
              <span>
                <Prose env={env} text={l.text} refs={refs} />
              </span>
            </li>
          ))}
        </ul>
      ) : (
        <div className="grid gap-1.5 pl-7 text-xs/relaxed">
          {t.note && <p>{t.note}</p>}
          <ul className="grid gap-1">
            {threads.map((th) => (
              <li key={th.id} className="flex flex-wrap gap-x-2">
                {t.id ? (
                  <Link
                    to="/e/$env/teammates/$id"
                    params={{ env, id: t.id }}
                    search={{ thread: th.id }}
                    className="font-medium hover:underline"
                  >
                    {th.title}
                  </Link>
                ) : (
                  <Link
                    to="/e/$env/chats/$chat"
                    params={{ env, chat: th.id }}
                    className="font-medium hover:underline"
                  >
                    {th.title}
                  </Link>
                )}
                <span className="text-muted-foreground">
                  {plural(th.turns, "turn", "turns")} · {turnTime(th.seconds)}
                  {th.running && " · working"}
                  {th.stop && ` · ${th.stop}`}
                </span>
              </li>
            ))}
          </ul>
        </div>
      )}
    </section>
  )
}

const pushStates: Record<LeadPush["state"], string> = {
  pending: "waits for review",
  pushed: "pushed",
  failed: "failed",
  rejected: "declined",
  superseded: "superseded",
}

function PushRow({ p }: { p: LeadPush }) {
  return (
    <li className="grid grid-cols-[auto_minmax(0,1fr)_auto] items-baseline gap-2 px-3 py-2 text-xs">
      {p.prUrl ? (
        <a
          href={p.prUrl}
          target="_blank"
          rel="noreferrer"
          className="font-mono underline underline-offset-3"
        >
          #{p.prNumber}
        </a>
      ) : (
        <span className="font-mono text-muted-foreground">push</span>
      )}
      <span className="min-w-0 truncate" title={`${p.branch} to ${p.repo}`}>
        <span className="font-mono">{p.branch}</span>
        <span className="text-muted-foreground"> to {p.repo}</span>
      </span>
      <span
        className={cn(
          "font-mono text-meta",
          p.state === "failed" || p.state === "rejected"
            ? "text-(--st-failed)"
            : p.state === "pending"
              ? "text-(--st-waiting-fg)"
              : "text-muted-foreground"
        )}
      >
        {pushStates[p.state]}
      </span>
    </li>
  )
}

function CostRow({ label, value }: { label: string; value: number }) {
  return (
    <>
      <dt className="min-w-0 truncate">{label}</dt>
      <dd className="text-right font-mono">{dollars(value)}</dd>
    </>
  )
}

function Number({
  value,
  label,
  tone,
}: {
  value: string
  label: string
  tone?: "waiting"
}) {
  return (
    <div className="grid gap-0.5 border-r border-b px-4 py-3 last:border-r-0 sm:border-b-0 [&:nth-child(2)]:max-sm:border-r-0">
      <dd
        className={cn(
          "font-mono text-base",
          tone === "waiting" && "text-(--st-waiting-fg)"
        )}
      >
        {value}
      </dd>
      <dt className="order-last text-caption text-muted-foreground">{label}</dt>
    </div>
  )
}

function Section({
  title,
  aside,
  children,
}: {
  title: string
  aside?: ReactNode
  children: ReactNode
}) {
  return (
    <section aria-label={title} className="grid min-w-0 gap-2">
      <h2 className="flex items-baseline justify-between section-label text-muted-foreground">
        {title}
        {aside && <span className="font-mono">{aside}</span>}
      </h2>
      {children}
    </section>
  )
}

function kindTitle(kind: string): string {
  return approvalKind({ kind } as Parameters<typeof approvalKind>[0]).title
}

/** The days before and after, written or today. */
function DayArrows({
  env,
  prev,
  next,
}: {
  env: string
  prev?: string
  next?: string
}) {
  const arrow = (day: string | undefined, label: string, icon: ReactNode) =>
    day ? (
      <Button
        size="icon-sm"
        variant="ghost"
        aria-label={`${label}: ${dayName(day)}`}
        title={`${label}: ${dayName(day)}`}
        render={
          <Link to="/e/$env/lead/$day" params={{ env, day }} replace={false} />
        }
      >
        {icon}
      </Button>
    ) : (
      <Button size="icon-sm" variant="ghost" aria-label={label} disabled>
        {icon}
      </Button>
    )
  return (
    <span className="flex items-center max-sm:hidden">
      {arrow(prev, "The day before", <CaretLeftIcon />)}
      {arrow(next, "The day after", <CaretRightIcon />)}
    </span>
  )
}

function CopyButton({ markdown }: { markdown: string }) {
  return (
    <Button
      size="sm"
      variant="outline"
      onClick={() =>
        copyText(markdown).then(
          () => toast.success("The day is copied as Markdown"),
          (err: unknown) => toast.error(errorMessage(err))
        )
      }
    >
      <CopyIcon />
      <span className="max-sm:hidden">Copy as Markdown</span>
    </Button>
  )
}

/** Writes today's day now; it says so while it writes. */
export function SummarizeButton({
  env,
  writing,
  variant = "outline",
}: {
  env: string
  writing: boolean
  variant?: "outline" | "default"
}) {
  const queryClient = useQueryClient()
  const summarize = $api.useMutation(
    "post",
    "/api/environments/{env}/lead/summarize",
    {
      onSuccess: () =>
        void queryClient.invalidateQueries({
          queryKey: ["get", "/api/environments/{env}/lead"],
        }),
      onError: (err) => toast.error(errorMessage(err)),
    }
  )
  const busy = writing || summarize.isPending
  return (
    <Button
      size="sm"
      variant={variant}
      disabled={busy}
      onClick={() => summarize.mutate({ params: { path: { env } } })}
    >
      {busy ? <Spinner /> : <ArrowClockwiseIcon />}
      {busy ? "Writing…" : "Summarize now"}
    </Button>
  )
}
