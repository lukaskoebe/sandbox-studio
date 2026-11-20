import { Link } from "@tanstack/react-router"
import { ArrowRightIcon } from "@phosphor-icons/react"
import { Button } from "@/components/ui/button"
import { $api } from "@/lib/api/client"
import { clock, dollars, plural, turnTime } from "@/lib/lead"
import { SummarizeButton } from "./day-page"
import { LeadGlyph } from "./glyph"

/**
 * Home's card on the day so far (docs/lead.md): the written day's summary, or until it is
 * written each working teammate's latest note joined without a new model call, then the day's
 * numbers, Summarize now and the way to the day. A day without a turn shows no card.
 */
export function LeadTodayCard({ env }: { env: string }) {
  const lead = $api.useQuery("get", "/api/environments/{env}/lead", {
    params: { path: { env } },
  })
  const v = lead.data
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
    </section>
  )
}
