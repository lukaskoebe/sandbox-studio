import { useEffect, useState } from "react"
import { Link } from "@tanstack/react-router"
import { ArrowRightIcon } from "@phosphor-icons/react"
import { Button } from "@/components/ui/button"
import { $api } from "@/lib/api/client"
import {
  clock,
  dollars,
  plural,
  turnTime,
  type LeadSince,
  type LeadView,
} from "@/lib/lead"
import { postLeadSeen } from "./ask"
import { SummarizeButton } from "./day-page"
import { LeadGlyph } from "./glyph"

/**
 * Home's card on the day so far (docs/lead.md): the written day's summary, or until it is
 * written each working teammate's latest note joined without a new model call, then the day's
 * numbers, what happened since the user last looked, Summarize now and the way to the day. A
 * day without a turn shows no card.
 */
export function LeadTodayCard({ env }: { env: string }) {
  const lead = $api.useQuery("get", "/api/environments/{env}/lead", {
    params: { path: { env } },
  })
  const v = lead.data
  const since = useSinceYouLooked(env, v)
  if (!v) return null
  const t = v.today
  if (t.turns === 0 && !t.writtenAt) return null
  const name = v.settings.name
  const notes = t.notes ?? []
  return (
    <section
      aria-label="Today so far"
      className="grid gap-3 rounded-(--radius) border bg-card p-4"
    >
      <header className="flex flex-wrap items-center gap-x-2 gap-y-1">
        <LeadGlyph className="size-5" />
        <h2 className="text-sm font-medium">
          {t.writtenAt ? "Today" : "Today so far"}
        </h2>
        <span className="text-caption text-muted-foreground">
          {name}
          {t.writtenAt && ` · written ${clock(t.writtenAt)}`}
        </span>
        <span className="ml-auto flex items-center gap-1">
          <SummarizeButton env={env} writing={v.writing} />
          <Button
            size="sm"
            variant="ghost"
            render={<Link to="/e/$env/lead" params={{ env }} />}
          >
            Open the day
            <ArrowRightIcon />
          </Button>
        </span>
      </header>
      {t.summary ? (
        <p className="max-w-3xl text-xs/relaxed">{t.summary}</p>
      ) : notes.length > 0 ? (
        <p className="max-w-3xl text-xs/relaxed">
          {notes.map((n, i) => (
            <span key={n.teammateId}>
              {i > 0 && " "}
              <Link
                to="/e/$env/teammates/$id"
                params={{ env, id: n.teammateId }}
                className="font-medium hover:underline"
              >
                {n.name}
              </Link>
              : {n.note}
            </span>
          ))}
        </p>
      ) : (
        <p className="text-xs text-muted-foreground">
          {name} keeps a note on each teammate, within a quarter of an hour of
          their turns.
        </p>
      )}
      <ul className="flex flex-wrap gap-x-5 gap-y-1 font-mono text-meta text-muted-foreground">
        {t.pushes > 0 && (
          <li>
            {plural(t.pushes, "push", "pushes")}
            {t.prs > 0 && ` · ${plural(t.prs, "PR", "PRs")}`}
          </li>
        )}
        <li>{turnTime(t.turnSeconds)} of turns</li>
        {t.costed && <li>{dollars(t.cost)} so far</li>}
      </ul>
      {since && <SinceLine s={since} />}
    </section>
  )
}

/**
 * What happened since the user last looked at today, as it was when Home opened: it stays
 * while Home is open, and opening Home records the look.
 */
function useSinceYouLooked(env: string, v?: LeadView) {
  const [snap, setSnap] = useState<LeadSince | null | undefined>(undefined)
  if (v && snap === undefined) setSnap(v.today.since ?? null)
  const took = snap !== undefined
  useEffect(() => {
    if (took) void postLeadSeen(env)
  }, [env, took])
  return snap
}

function SinceLine({ s }: { s: LeadSince }) {
  const who = s.teammates ?? []
  const parts = [
    who.length > 0 &&
      `${names(who)} worked in ${plural(s.threads, "thread", "threads")}`,
    s.pushes > 0 &&
      `${plural(s.pushes, "push", "pushes")}${s.prs > 0 ? `, ${s.prs} with a PR` : ""}`,
    s.waiting > 0 &&
      `${plural(s.waiting, "request waits", "requests wait")} for you`,
    s.tasks > 0 && `${plural(s.tasks, "task ran", "tasks ran")}`,
  ].filter(Boolean)
  if (parts.length === 0) return null
  return (
    <p className="border-t pt-2 text-xs text-muted-foreground">
      <span className="font-medium text-foreground">
        Since you looked at {clock(s.at)}:
      </span>{" "}
      {parts.join(" · ")}
    </p>
  )
}

/** "Ada", "Ada and Juno", "Ada, Juno and 2 others". */
function names(n: string[]): string {
  if (n.length <= 2) return n.join(" and ")
  if (n.length === 3) return `${n[0]}, ${n[1]} and ${n[2]}`
  return `${n[0]}, ${n[1]} and ${n.length - 2} others`
}
