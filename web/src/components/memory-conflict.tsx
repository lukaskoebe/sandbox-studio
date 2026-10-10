import { useState } from "react"
import { useQueryClient } from "@tanstack/react-query"
import { toast } from "sonner"
import { MoonStarsIcon } from "@phosphor-icons/react"
import { Badge } from "@/components/ui/badge"
import { Button } from "@/components/ui/button"
import { Field, FieldDescription, FieldLabel } from "@/components/ui/field"
import { Input } from "@/components/ui/input"
import {
  Sheet,
  SheetContent,
  SheetDescription,
  SheetHeader,
  SheetTitle,
} from "@/components/ui/sheet"
import { Spinner } from "@/components/ui/spinner"
import {
  invalidateMemory,
  textareaClass,
  type MemoryFact,
} from "@/components/memory-dialogs"
import {
  $api,
  errorMessage,
  errorStatus,
  type Approval,
} from "@/lib/api/client"
import type { components } from "@/lib/api/schema.gen"
import { formatAge } from "@/lib/utils"

type ConflictDetail = components["schemas"]["ConflictDetail"]
type Source = components["schemas"]["Source"]
type DreamRun = components["schemas"]["DreamRun"]
type Resolution = components["schemas"]["ConflictResolution"]

/** What the judge's verdicts mean, for the conflict header. */
const verdictText: Record<string, string> = {
  contradiction: "The two facts cannot both be true.",
  temporal_supersession:
    "B looks like a newer value of A, but it comes from a less trusted source or would replace a shared fact, so it was not applied by itself.",
  context_dependent: "Both may be true in different contexts.",
  duplicate: "The two facts say the same thing.",
}

/**
 * A wide sheet with one conflict: both facts side by side with their sources and tiers, the
 * timeline around them, and the ways to resolve it. Shared facts win at recall until it is
 * resolved.
 */
export function ConflictSheet({
  env,
  conflictId,
  onOpenChange,
}: {
  env: string
  conflictId?: string
  onOpenChange: (open: boolean) => void
}) {
  const detail = $api.useQuery(
    "get",
    "/api/environments/{env}/memory/conflicts/{id}",
    { params: { path: { env, id: conflictId ?? "" } } },
    { enabled: conflictId !== undefined }
  )
  const c = detail.data
  return (
    <Sheet open={conflictId !== undefined} onOpenChange={onOpenChange}>
      <SheetContent className="data-[side=right]:sm:max-w-3xl">
        <SheetHeader>
          <SheetTitle>Conflicting facts</SheetTitle>
          <SheetDescription>
            {c ? (verdictText[c.verdict] ?? c.verdict) : "Two facts disagree."}
          </SheetDescription>
        </SheetHeader>
        <div className="min-h-0 flex-1 space-y-4 overflow-auto px-6 pb-6 text-xs">
          {detail.isPending ? (
            <Spinner className="mx-auto block" />
          ) : detail.isError || !c ? (
            <p className="text-destructive">{errorMessage(detail.error)}</p>
          ) : (
            <ConflictBody
              key={c.id}
              env={env}
              conflict={c}
              onResolved={() => onOpenChange(false)}
            />
          )}
        </div>
      </SheetContent>
    </Sheet>
  )
}

function ConflictBody({
  env,
  conflict: c,
  onResolved,
}: {
  env: string
  conflict: ConflictDetail
  onResolved: () => void
}) {
  return (
    <>
      <div className="flex flex-wrap items-center gap-1.5">
        <Badge variant="outline">{c.verdict.replaceAll("_", " ")}</Badge>
        <Badge variant={c.status === "open" ? "destructive" : "secondary"}>
          {c.status === "open" ? "open" : `resolved: ${c.resolution}`}
        </Badge>
        <span className="text-muted-foreground">{formatAge(c.createdAt)}</span>
      </div>
      {c.reason && (
        <p>
          <span className="text-muted-foreground">Why: </span>
          {c.reason}
        </p>
      )}
      {c.note && <p className="text-muted-foreground">{c.note}</p>}
      <div className="grid gap-2 md:grid-cols-2">
        <FactSide name="A" fact={c.factA} sources={c.sourcesA ?? []} />
        <FactSide name="B" fact={c.factB} sources={c.sourcesB ?? []} />
      </div>
      {(c.timeline ?? []).length > 0 && (
        <div className="space-y-1">
          <h3 className="font-medium">Timeline</h3>
          <ol className="space-y-1">
            {(c.timeline ?? []).map((e) => (
              <li key={e.id} className="flex gap-2">
                <span className="w-20 shrink-0 text-muted-foreground">
                  {formatAge(e.at)}
                </span>
                <span className="whitespace-pre-wrap">{e.text}</span>
              </li>
            ))}
          </ol>
        </div>
      )}
      {c.status === "open" && (
        <ResolveForm env={env} conflict={c} onResolved={onResolved} />
      )}
    </>
  )
}

function FactSide({
  name,
  fact,
  sources,
}: {
  name: string
  fact: MemoryFact
  sources: Source[]
}) {
  return (
    <div className="space-y-1.5 rounded-md bg-muted/50 p-2">
      <div className="flex items-center gap-1.5">
        <span className="font-medium">{name}</span>
        <span className="font-mono text-[0.625rem] text-muted-foreground">
          {fact.scope}
        </span>
      </div>
      <p className="whitespace-pre-wrap">{fact.text}</p>
      <div className="flex flex-wrap gap-1">
        <Badge variant={fact.tier === "user" ? "default" : "outline"}>
          {fact.tier}
        </Badge>
        <Badge variant={fact.status === "disputed" ? "destructive" : "outline"}>
          {fact.status}
        </Badge>
        <span className="text-muted-foreground">
          observed {formatAge(fact.observedAt)}
        </span>
      </div>
      {sources.length > 0 && (
        <ul className="space-y-0.5 border-t border-foreground/10 pt-1.5">
          {sources.map((s) => (
            <li key={s.id} className="text-muted-foreground">
              <span className="text-foreground">{s.kind}</span>
              {s.authorPersona && ` · persona:${s.authorPersona}`}
              {` · ${formatAge(s.createdAt)}`}
              {s.evidence && (
                <span className="block italic">“{s.evidence}”</span>
              )}
            </li>
          ))}
        </ul>
      )}
    </div>
  )
}

function ResolveForm({
  env,
  conflict: c,
  onResolved,
}: {
  env: string
  conflict: ConflictDetail
  onResolved: () => void
}) {
  const queryClient = useQueryClient()
  const [mode, setMode] = useState<"pick" | "both" | "edit">("pick")
  const [note, setNote] = useState("")
  const [textA, setTextA] = useState(c.factA.text)
  const [textB, setTextB] = useState(c.factB.text)
  const [text, setText] = useState(c.factB.text)
  const resolve = $api.useMutation(
    "post",
    "/api/environments/{env}/memory/conflicts/{id}/resolve",
    {
      onSuccess: () => {
        toast.success("Conflict resolved")
        onResolved()
      },
      onSettled: () => {
        invalidateMemory(queryClient)
        queryClient.invalidateQueries({ queryKey: ["get", "/api/approvals"] })
      },
      onError: (err) =>
        toast.error("Could not resolve the conflict", {
          description: errorMessage(err),
        }),
    }
  )
  const submit = (body: Resolution) =>
    resolve.mutate({
      params: { path: { env, id: c.id } },
      body: { ...body, note: note.trim() || undefined },
    })

  return (
    <div className="space-y-3 border-t border-foreground/10 pt-3">
      <h3 className="font-medium">Resolve</h3>
      <Field>
        <FieldLabel htmlFor="conflict-note">Note</FieldLabel>
        <Input
          id="conflict-note"
          value={note}
          maxLength={1000}
          placeholder="Optional: why"
          onChange={(e) => setNote(e.target.value)}
        />
      </Field>
      {mode === "pick" && (
        <div className="flex flex-wrap gap-1.5">
          <Button
            size="sm"
            disabled={resolve.isPending}
            onClick={() => submit({ resolution: "keep_a" })}
          >
            Keep A
          </Button>
          <Button
            size="sm"
            disabled={resolve.isPending}
            onClick={() => submit({ resolution: "keep_b" })}
          >
            Keep B
          </Button>
          <Button size="sm" variant="outline" onClick={() => setMode("both")}>
            Keep both…
          </Button>
          <Button size="sm" variant="outline" onClick={() => setMode("edit")}>
            Edit…
          </Button>
        </div>
      )}
      {mode === "both" && (
        <div className="space-y-2">
          <FieldDescription>
            Both stay true. Qualify them so they no longer disagree, for example
            “In project X, …”.
          </FieldDescription>
          <QualifiedText
            id="conflict-a"
            label="A"
            value={textA}
            set={setTextA}
          />
          <QualifiedText
            id="conflict-b"
            label="B"
            value={textB}
            set={setTextB}
          />
          <FormButtons
            pending={resolve.isPending}
            onCancel={() => setMode("pick")}
            onSubmit={() =>
              submit({
                resolution: "keep_both",
                textA: textA.trim() !== c.factA.text ? textA.trim() : undefined,
                textB: textB.trim() !== c.factB.text ? textB.trim() : undefined,
              })
            }
            label="Keep both"
          />
        </div>
      )}
      {mode === "edit" && (
        <div className="space-y-2">
          <QualifiedText
            id="conflict-text"
            label="One fact replaces both"
            value={text}
            set={setText}
          />
          <FormButtons
            pending={resolve.isPending}
            disabled={text.trim() === ""}
            onCancel={() => setMode("pick")}
            onSubmit={() => submit({ resolution: "edit", text: text.trim() })}
            label="Replace both"
          />
        </div>
      )}
      <p className="text-muted-foreground">
        Keeping one retracts the other. Every choice is recorded as yours, at
        tier user.
      </p>
    </div>
  )
}

function QualifiedText({
  id,
  label,
  value,
  set,
}: {
  id: string
  label: string
  value: string
  set: (v: string) => void
}) {
  return (
    <Field>
      <FieldLabel htmlFor={id}>{label}</FieldLabel>
      <textarea
        id={id}
        rows={3}
        maxLength={2000}
        className={textareaClass}
        value={value}
        onChange={(e) => set(e.target.value)}
      />
    </Field>
  )
}

function FormButtons({
  pending,
  disabled,
  onCancel,
  onSubmit,
  label,
}: {
  pending: boolean
  disabled?: boolean
  onCancel: () => void
  onSubmit: () => void
  label: string
}) {
  return (
    <div className="flex gap-1.5">
      <Button size="sm" disabled={pending || disabled} onClick={onSubmit}>
        {pending && <Spinner />}
        {label}
      </Button>
      <Button size="sm" variant="ghost" onClick={onCancel}>
        Cancel
      </Button>
    </div>
  )
}

/** The approval stack's body for a memory.conflict approval. */
export function MemoryConflictDecision({
  approval,
  onResolve,
}: {
  approval: Approval
  onResolve: () => void
}) {
  const queryClient = useQueryClient()
  const decide = $api.useMutation(
    "post",
    "/api/environments/{env}/approvals/{id}/decide",
    {
      onSettled: () =>
        queryClient.invalidateQueries({ queryKey: ["get", "/api/approvals"] }),
      onError: (err) => {
        if (errorStatus(err) === 409) return
        toast.error("Could not dismiss the conflict", {
          description: errorMessage(err),
        })
      },
    }
  )
  const p = approval.memoryConflict
  return (
    <div className="grid gap-2">
      {p && (
        <div className="grid gap-1">
          <p className="whitespace-pre-wrap">
            <span className="font-medium">A </span>
            {p.factA.text}
            <span className="text-muted-foreground"> · {p.factA.tier}</span>
          </p>
          <p className="whitespace-pre-wrap">
            <span className="font-medium">B </span>
            {p.factB.text}
            <span className="text-muted-foreground"> · {p.factB.tier}</span>
          </p>
        </div>
      )}
      <div className="flex items-center gap-1.5">
        <Button
          variant="outline"
          size="sm"
          className="ml-auto"
          title="Hide it here; the conflict stays open on the memory page"
          disabled={decide.isPending}
          onClick={() =>
            decide.mutate({
              params: {
                path: { env: approval.environmentId, id: approval.id },
              },
              body: { action: "dismiss", scope: "environment" },
            })
          }
        >
          Later
        </Button>
        <Button size="sm" onClick={onResolve}>
          Resolve…
        </Button>
      </div>
    </div>
  )
}

const statusVariant: Record<
  DreamRun["status"],
  "secondary" | "outline" | "destructive"
> = {
  running: "outline",
  done: "secondary",
  stopped: "outline",
  failed: "destructive",
}

function runSummary(r: DreamRun) {
  const s = r.stats
  const parts = [
    `${s.facts} new facts`,
    `${s.judged + s.cached} pairs judged`,
    s.merged && `${s.merged} merged`,
    s.superseded && `${s.superseded} superseded`,
    s.conflicts && `${s.conflicts} conflicts`,
    s.pages && `${s.pages} pages`,
    s.costMicros && `$${(s.costMicros / 1e6).toFixed(4)}`,
  ]
  return parts.filter(Boolean).join(" · ")
}

/**
 * Consolidation of one scope: start a dream now, and the latest runs. Dreams also run
 * nightly and after enough new facts.
 */
export function DreamPanel({ env, scope }: { env: string; scope: string }) {
  const queryClient = useQueryClient()
  const runs = $api.useQuery(
    "get",
    "/api/environments/{env}/memory/dreams",
    { params: { path: { env }, query: { scope, limit: 5 } } },
    {
      refetchInterval: (q) =>
        q.state.data?.some((r) => r.status === "running") ? 2000 : false,
    }
  )
  const start = $api.useMutation(
    "post",
    "/api/environments/{env}/memory/dream",
    {
      onSettled: () => invalidateMemory(queryClient),
      onError: (err) =>
        toast.error("Could not start consolidation", {
          description: errorMessage(err),
        }),
    }
  )
  const list = runs.data ?? []
  const running = list.some((r) => r.status === "running")
  return (
    <div className="space-y-2">
      <div className="flex items-center gap-2">
        <Button
          size="sm"
          variant="outline"
          disabled={start.isPending || running}
          onClick={() =>
            start.mutate({ params: { path: { env } }, body: { scope } })
          }
        >
          {start.isPending || running ? <Spinner /> : <MoonStarsIcon />}
          Dream now
        </Button>
        <span className="text-muted-foreground">
          Merges duplicates, retires outdated facts and finds contradictions.
          Runs nightly and after 25 new facts too.
        </span>
      </div>
      {list.length > 0 && (
        <ul className="space-y-1">
          {list.map((r) => (
            <li key={r.id} className="flex flex-wrap items-center gap-1.5">
              <Badge variant={statusVariant[r.status]}>{r.status}</Badge>
              <span className="text-muted-foreground">
                {r.trigger} · {formatAge(r.startedAt)}
                {r.resumes > 0 && ` · resumed ${r.resumes}×`}
              </span>
              <span>{runSummary(r)}</span>
              {r.note && (
                <span className="basis-full text-muted-foreground">
                  {r.note}
                </span>
              )}
            </li>
          ))}
        </ul>
      )}
    </div>
  )
}
