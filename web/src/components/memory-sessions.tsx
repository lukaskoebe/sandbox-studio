import { useState } from "react"
import { useQueryClient } from "@tanstack/react-query"
import { toast } from "sonner"
import { Badge } from "@/components/ui/badge"
import { Button } from "@/components/ui/button"
import {
  Empty,
  EmptyDescription,
  EmptyHeader,
  EmptyTitle,
} from "@/components/ui/empty"
import { Spinner } from "@/components/ui/spinner"
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "@/components/ui/table"
import type { components } from "@/lib/api/schema.gen"
import {
  $api,
  errorMessage,
  errorStatus,
  type Approval,
} from "@/lib/api/client"
import { cn, formatAge } from "@/lib/utils"

type SessionView = components["schemas"]["SessionView"]
type LogEntry = components["schemas"]["LogEntry"]

const kindVariant: Record<
  LogEntry["kind"],
  "default" | "secondary" | "outline"
> = {
  context: "secondary",
  recall: "secondary",
  write: "default",
  extraction: "outline",
  tool: "outline",
}

/** Today's extraction spending against the daily budget. */
export function MemoryUsage({ env }: { env: string }) {
  const usage = $api.useQuery("get", "/api/environments/{env}/memory/usage", {
    params: { path: { env } },
  })
  if (!usage.data) return null
  const { today, budget, models } = usage.data
  const dollars = (micros: number) => `$${(micros / 1e6).toFixed(2)}`
  const extracting = (models ?? []).filter((m) => m.model)
  return (
    <p className="text-xs text-muted-foreground">
      Extraction today: {today.calls} calls, {dollars(today.costMicros)} of{" "}
      {dollars(budget.maxCostMicros)}
      {today.skipped > 0 && `, ${today.skipped} skipped`}
      {extracting.length > 0
        ? ` · ${extracting.map((m) => `${m.provider}: ${m.model}`).join(", ")}`
        : " · no provider can extract (subscriptions can't yet)"}
      {extracting.some((m) => !m.priced) &&
        ` · unpriced models: at most ${budget.maxCalls} calls a day`}
    </p>
  )
}

/** Harness sessions and, for the selected one, what memory gave it and wrote from it. */
export function MemorySessionsTab({
  env,
  persona,
  names,
}: {
  env: string
  persona?: string
  names: Map<string, string>
}) {
  const [selected, setSelected] = useState<string>()
  const sessions = $api.useQuery(
    "get",
    "/api/environments/{env}/memory/sessions",
    { params: { path: { env }, query: { persona } } }
  )
  const list = sessions.data ?? []
  if (sessions.isPending) return <Spinner className="mx-auto block" />
  if (sessions.error)
    return <p className="text-destructive">{errorMessage(sessions.error)}</p>
  if (list.length === 0)
    return (
      <Empty>
        <EmptyHeader>
          <EmptyTitle>No sessions yet</EmptyTitle>
          <EmptyDescription>
            Agent sessions show up here once their hooks call Studio, with
            everything memory injected and wrote and why.
          </EmptyDescription>
        </EmptyHeader>
      </Empty>
    )
  return (
    <div className="space-y-3">
      <Table>
        <TableHeader>
          <TableRow>
            <TableHead>Persona</TableHead>
            <TableHead>Sandbox</TableHead>
            <TableHead>Session</TableHead>
            <TableHead>Repository</TableHead>
            <TableHead className="text-right">Injected</TableHead>
            <TableHead className="text-right">Writes</TableHead>
            <TableHead>Last activity</TableHead>
          </TableRow>
        </TableHeader>
        <TableBody>
          {list.map((s: SessionView) => (
            <TableRow
              key={s.id}
              className={cn(
                "cursor-pointer",
                selected === s.id && "bg-muted/50"
              )}
              onClick={() => setSelected(selected === s.id ? undefined : s.id)}
            >
              <TableCell>{names.get(s.personaId) ?? s.personaId}</TableCell>
              <TableCell>{s.sandboxName}</TableCell>
              <TableCell className="font-mono text-[0.625rem]">
                {s.harness} · {s.sessionRef.slice(0, 12)}
              </TableCell>
              <TableCell className="font-mono text-[0.625rem]">
                {s.gitRemote}
              </TableCell>
              <TableCell className="text-right">{s.injections}</TableCell>
              <TableCell className="text-right">{s.writes}</TableCell>
              <TableCell>{formatAge(s.lastEventAt)}</TableCell>
            </TableRow>
          ))}
        </TableBody>
      </Table>
      {selected && <SessionLog env={env} id={selected} />}
    </div>
  )
}

function SessionLog({ env, id }: { env: string; id: string }) {
  const log = $api.useQuery(
    "get",
    "/api/environments/{env}/memory/sessions/{id}/log",
    { params: { path: { env, id } } }
  )
  if (log.isPending) return <Spinner className="mx-auto block" />
  if (log.error)
    return <p className="text-destructive">{errorMessage(log.error)}</p>
  if (!log.data?.length)
    return <p className="text-muted-foreground">Nothing logged.</p>
  return (
    <ol className="space-y-1.5">
      {log.data.map((e) => (
        <li key={e.id} className="rounded-md p-2 ring-1 ring-foreground/10">
          <div className="flex flex-wrap items-center gap-1.5">
            <Badge variant={kindVariant[e.kind]}>{e.kind}</Badge>
            <span className="min-w-0 flex-1 break-words">{e.summary}</span>
            {e.score != null && (
              <span className="font-mono text-muted-foreground">
                {e.score.toFixed(4)}
              </span>
            )}
            <span className="text-muted-foreground">{formatAge(e.at)}</span>
          </div>
          {e.why != null && (
            <details className="mt-1">
              <summary className="cursor-pointer text-muted-foreground">
                Why
              </summary>
              <pre className="mt-1 overflow-auto rounded bg-muted/50 p-2 font-mono text-[0.625rem]">
                {JSON.stringify(e.why, null, 2)}
              </pre>
            </details>
          )}
        </li>
      ))}
    </ol>
  )
}

/** Approves or declines a persona's proposal for shared memory. */
export function MemoryShareDecision({ approval }: { approval: Approval }) {
  const queryClient = useQueryClient()
  const decide = $api.useMutation(
    "post",
    "/api/environments/{env}/approvals/{id}/decide",
    {
      onSettled: () => {
        queryClient.invalidateQueries({ queryKey: ["get", "/api/approvals"] })
        queryClient.invalidateQueries({
          queryKey: ["get", "/api/environments/{env}/memory/facts"],
        })
      },
      onError: (err) => {
        if (errorStatus(err) === 409) return
        toast.error("Could not decide the share", {
          description: errorMessage(err),
        })
      },
    }
  )
  const act = (action: "allow" | "deny") =>
    decide.mutate({
      params: { path: { env: approval.environmentId, id: approval.id } },
      body: { action, scope: "environment" },
    })
  const share = approval.memoryShare
  return (
    <div className="grid gap-2">
      <p className="whitespace-pre-wrap">{share?.text ?? approval.subject}</p>
      <div className="flex items-center gap-1.5">
        <span className="text-xs text-muted-foreground">
          {share ? `${share.kind} · ${share.tier}` : ""}
        </span>
        <Button
          variant="outline"
          size="sm"
          className="ml-auto"
          disabled={decide.isPending}
          onClick={() => act("deny")}
        >
          Decline
        </Button>
        <Button
          size="sm"
          disabled={decide.isPending}
          onClick={() => act("allow")}
        >
          {decide.isPending && <Spinner />}
          Share
        </Button>
      </div>
    </div>
  )
}
