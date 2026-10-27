import { ChartBarIcon } from "@phosphor-icons/react"
import { SectionHeader } from "@/components/section-header"
import { $api } from "@/lib/api/client"
import { formatAge } from "@/lib/utils"

/**
 * The memory's quality numbers, compact: what the facts are, how the judge has decided, what
 * the last consolidation did, and what the sessions got in the last week.
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
  const last = q.lastRun
  return (
    <div className="space-y-2">
      <SectionHeader title="Quality" icon={ChartBarIcon} area="memory" />
      <dl className="grid grid-cols-2 gap-x-6 gap-y-3 rounded-lg p-3 text-xs ring-1 ring-foreground/10 sm:grid-cols-3 lg:grid-cols-6">
        <Stat
          label="Active facts"
          value={at(q.facts, "active")}
          note={`${at(q.facts, "disputed")} disputed · ${at(q.facts, "superseded")} superseded · ${at(q.facts, "retracted")} retracted`}
        />
        <Stat
          label="In a context"
          value={q.contextual}
          note="surface only where named"
        />
        <Stat
          label="Judge verdicts"
          value={count(q.verdicts)}
          note={byWord(q.verdicts) || "none cached yet"}
        />
        <Stat
          label="Conflicts"
          value={at(q.conflicts, "open")}
          note={`open · ${at(q.conflicts, "resolved")} resolved`}
        />
        <Stat
          label="Suggestions"
          value={count(q.suggestions)}
          note={byWord(q.suggestions) || "open"}
        />
        <Stat
          label="Last consolidation"
          value={last ? last.status : "none"}
          note={
            last
              ? `${last.scope === "shared" ? "shared" : "a teammate"} · ${last.trigger} · ${formatAge(last.startedAt)}`
              : "runs after new facts or on Dream now"
          }
        />
        <Stat
          label="Sessions, 7 days"
          value={q.sessions}
          note={`${q.injected} items injected · ${q.writes} facts written`}
        />
      </dl>
    </div>
  )
}

function Stat({
  label,
  value,
  note,
}: {
  label: string
  value: string | number
  note: string
}) {
  return (
    <div className="min-w-0">
      <dt className="text-muted-foreground">{label}</dt>
      <dd className="text-sm font-medium">{value}</dd>
      <dd className="truncate text-meta text-muted-foreground" title={note}>
        {note}
      </dd>
    </div>
  )
}
