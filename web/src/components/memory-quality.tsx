import { $api } from "@/lib/api/client"
import { formatAge } from "@/lib/utils"

/**
 * The memory's numbers in one quiet line: what the week's sessions got, what a second model
 * checked, what disagrees, and when it was last tidied. A number that would be zero stays
 * out, and nothing shows before the first fact.
 */
export function MemoryQuality({ env }: { env: string }) {
  const quality = $api.useQuery(
    "get",
    "/api/environments/{env}/memory/quality",
    {
      params: { path: { env } },
    }
  )
  const q = quality.data
  if (!q) return null
  // Keys without rows are missing, not zero, in the generated types' records.
  const at = (m: Partial<Record<string, number>>, key: string) => m[key] ?? 0
  const count = (m: Record<string, number>) =>
    Object.values(m).reduce((sum, n) => sum + n, 0)
  const byWord = (m: Record<string, number>) =>
    Object.entries(m)
      .map(([k, n]) => `${n} ${k.replaceAll("_", " ")}`)
      .join(" · ")
  if (count(q.facts) === 0) return null
  const retired = at(q.facts, "superseded") + at(q.facts, "retracted")
  const stats: { label: string; value: number; note: string }[] = [
    {
      label: "Used in prompts this week",
      value: q.injected,
      note: `${q.injected} items put into ${q.sessions} sessions; ${q.writes} facts written from them`,
    },
    {
      label: "Checked by a second model",
      value: count(q.verdicts),
      note: byWord(q.verdicts),
    },
    {
      label: "Disagree with each other",
      value: at(q.conflicts, "open"),
      note: `${at(q.conflicts, "resolved")} settled before`,
    },
    {
      label: "Suggested changes",
      value: count(q.suggestions),
      note: byWord(q.suggestions),
    },
    {
      label: "Only where named",
      value: q.contextual,
      note: "Facts that hold in one context, and surface only where it's named",
    },
    {
      label: "Replaced or taken back",
      value: retired,
      note: `${at(q.facts, "superseded")} replaced · ${at(q.facts, "retracted")} retracted`,
    },
  ].filter((s) => s.value > 0)
  const last = q.lastRun
  const tidied =
    last &&
    (last.status === "running"
      ? "tidying now"
      : last.status === "failed"
        ? `tidying failed ${formatAge(last.startedAt)}`
        : `tidied ${formatAge(last.startedAt)}`)
  if (stats.length === 0 && !tidied) return null
  return (
    <dl
      aria-label="Memory numbers"
      className="flex flex-wrap items-baseline gap-x-5 gap-y-1 text-xs"
    >
      {stats.map((s) => (
        <div
          key={s.label}
          className="flex items-baseline gap-1.5"
          title={s.note}
        >
          <dt className="text-muted-foreground">{s.label}</dt>
          <dd className="font-medium tabular-nums">{s.value}</dd>
        </div>
      ))}
      {tidied && (
        <div
          className="flex items-baseline gap-1.5"
          title={`${last.scope === "shared" ? "Shared memory" : "A teammate's memory"}, ${last.trigger === "manual" ? "on Tidy now" : last.trigger === "nightly" ? "nightly" : "after new facts"}`}
        >
          <dt className="sr-only">Last tidied</dt>
          <dd className="text-muted-foreground">{tidied}</dd>
        </div>
      )}
    </dl>
  )
}
