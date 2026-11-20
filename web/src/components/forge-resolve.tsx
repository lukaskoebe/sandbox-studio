import { useState } from "react"
import { useQueryClient } from "@tanstack/react-query"
import { toast } from "sonner"
import {
  ArrowSquareOutIcon,
  CheckIcon,
  DotsThreeIcon,
  EyeSlashIcon,
} from "@phosphor-icons/react"
import { Button } from "@/components/ui/button"
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuTrigger,
} from "@/components/ui/dropdown-menu"
import {
  Popover,
  PopoverContent,
  PopoverTrigger,
} from "@/components/ui/popover"
import { Spinner } from "@/components/ui/spinner"
import { Ident } from "@/components/ident"
import { StatusDot } from "@/components/status-dot"
import { textareaClass } from "@/components/memory-dialogs"
import { DecisionRow } from "@/components/review-feedback"
import {
  $api,
  errorMessage,
  errorStatus,
  type Approval,
} from "@/lib/api/client"
import { plural } from "@/lib/approval-kinds"

type Draft = NonNullable<Approval["forgeResolve"]>
type Thread = NonNullable<Draft["threads"]>[number]

/** A draft's threads; Go sends none as null. */
const threadsOf = (d: Draft) => d.threads ?? []

/** The pull request the threads are on, as owner/repo#number. */
export function resolvePullRequest(d: Draft) {
  return `${d.owner}/${d.repo}#${d.number}`
}

/** What reviewing a git.resolve approval decides, under its title. */
export function forgeResolveDescription(a: Approval) {
  const d = a.forgeResolve
  const who = d?.teammate || "The agent"
  const where = d && !d.teammate ? ` in ${d.sandbox}` : ""
  return `${who}${where} wants to mark review threads resolved. Nothing changes on the forge until you resolve them, as you.`
}

/** Where a thread is, as path:line. */
function place(t: Thread) {
  return `${t.path}${t.line ? `:${t.line}` : ""}${t.side === "old" ? " (old)" : ""}`
}

/** What became of a decided ask. */
export function ForgeResolveOutcome({ draft }: { draft: Draft }) {
  const n = threadsOf(draft).length
  switch (draft.state) {
    case "resolved":
      return <span className="text-xs">{plural(n, "thread")} resolved</span>
    case "failed": {
      const done = threadsOf(draft).filter((t) => t.done).length
      return (
        <span className="flex min-w-0 items-center gap-1.5 text-xs">
          <StatusDot status="failed" />
          <span className="truncate text-muted-foreground">
            {done > 0 ? `${done} of ${n} resolved · ` : ""}
            {draft.result || "Resolving failed"}
          </span>
        </span>
      )
    }
    case "discarded":
      return (
        <span className="truncate text-xs text-muted-foreground">
          Left as they are{draft.note ? ` · Note: ${draft.note}` : ""}
        </span>
      )
    default:
      return <span className="text-xs text-muted-foreground">Resolving…</span>
  }
}

/** A compact card body for the approval stack: the threads, and a button to review them. */
export function ForgeResolveSummary({
  approval,
  onReview,
}: {
  approval: Approval
  onReview: () => void
}) {
  const d = approval.forgeResolve
  return (
    <div className="grid gap-2 text-xs">
      {d && (
        <ul className="grid gap-0.5 text-muted-foreground">
          {threadsOf(d)
            .slice(0, 3)
            .map((t) => (
              <li key={t.id} className="truncate">
                <span className="font-mono">{place(t)}</span> · {t.body}
              </li>
            ))}
          {threadsOf(d).length > 3 && (
            <li>and {threadsOf(d).length - 3} more</li>
          )}
        </ul>
      )}
      <div className="flex items-center gap-2">
        {d && (
          <span className="truncate text-muted-foreground">
            As {d.account || "you"}, on {d.forgeHost}
          </span>
        )}
        <Button size="sm" className="ml-auto" onClick={onReview}>
          Review
        </Button>
      </div>
    </div>
  )
}

/** The full ask: the pull request, each thread's start, and the decision while pending. */
export function ForgeResolve({ approval }: { approval: Approval }) {
  const draft = approval.forgeResolve
  const who = draft ? draft.teammate || draft.sandbox : ""
  const [note, setNote] = useState("")
  const queryClient = useQueryClient()
  const decide = $api.useMutation(
    "post",
    "/api/environments/{env}/approvals/{id}/decide",
    {
      onSettled: () => {
        for (const path of [
          "/api/approvals",
          "/api/environments/{env}/approvals",
          "/api/environments/{env}/approvals/{id}",
        ])
          void queryClient.invalidateQueries({ queryKey: ["get", path] })
      },
      onSuccess: (a, vars) => {
        const d = a.forgeResolve
        if (vars.body.action !== "allow" || !d) return
        if (d.state === "resolved")
          toast.success(`${plural(threadsOf(d).length, "thread")} resolved`)
        else if (d.state === "failed")
          toast.error("Not every thread was resolved", {
            description: d.result,
          })
      },
      onError: (err) => {
        if (errorStatus(err) === 409) return
        toast.error("Could not decide", { description: errorMessage(err) })
      },
    }
  )
  if (!draft) return null
  const pending = approval.status === "pending"
  const params = {
    params: { path: { env: approval.environmentId, id: approval.id } },
  }
  const busy = (action: string) =>
    decide.isPending && decide.variables.body.action === action
  const resolve = () =>
    decide.mutate({ ...params, body: { action: "allow", scope: "sandbox" } })
  const leave = (action: "deny" | "dismiss") =>
    decide.mutate({
      ...params,
      body: { action, scope: "sandbox", note: note.trim() || undefined },
    })
  const n = threadsOf(draft).length

  return (
    <div className="grid min-w-0 gap-4">
      <header className="flex min-w-0 flex-wrap items-baseline gap-x-2 gap-y-1">
        <Ident>{resolvePullRequest(draft)}</Ident>
        <span className="min-w-0 font-medium">{draft.title}</span>
        {draft.url?.startsWith("http") && (
          <Button
            variant="ghost"
            size="xs"
            className="ml-auto"
            render={<a href={draft.url} target="_blank" rel="noreferrer" />}
          >
            <ArrowSquareOutIcon />
            Open on the forge
          </Button>
        )}
      </header>

      {!pending && (
        <dl className="grid grid-cols-[auto_1fr] gap-x-3 gap-y-1 text-xs">
          <dt className="text-muted-foreground">Outcome</dt>
          <dd className="min-w-0">
            <ForgeResolveOutcome draft={draft} />
          </dd>
        </dl>
      )}

      <section aria-label="Threads" className="grid gap-2">
        <h3 className="text-xs font-medium">
          {plural(n, "thread")} to resolve
        </h3>
        <ul className="grid gap-2">
          {threadsOf(draft).map((t) => (
            <li
              key={t.id}
              className="grid min-w-0 gap-1 rounded-md border bg-muted/30 px-3 py-2 text-xs"
            >
              <div className="flex min-w-0 items-baseline gap-2">
                <span className="truncate font-mono">{place(t)}</span>
                <span className="shrink-0 text-muted-foreground">
                  {t.author}
                  {t.replies
                    ? ` · ${t.replies} ${t.replies === 1 ? "reply" : "replies"}`
                    : ""}
                </span>
                {t.done && (
                  <CheckIcon
                    aria-label="Resolved"
                    className="ml-auto shrink-0 text-emerald-600 dark:text-emerald-400"
                  />
                )}
              </div>
              <p className="line-clamp-4 break-words whitespace-pre-wrap text-muted-foreground">
                {t.body}
              </p>
            </li>
          ))}
        </ul>
      </section>

      {pending && (
        <DecisionRow className="sticky bottom-0 z-10 flex flex-wrap items-center gap-1.5 rounded-lg border bg-background/95 p-2 shadow-md backdrop-blur">
          <p className="min-w-0 flex-1 basis-56 text-xs text-muted-foreground">
            Resolves as <Ident>{draft.account || "you"}</Ident> on{" "}
            {draft.forgeHost}
          </p>
          <div className="ml-auto flex items-center gap-1.5">
            <DropdownMenu>
              <DropdownMenuTrigger
                render={
                  <Button
                    variant="ghost"
                    size="icon-sm"
                    aria-label="More decisions"
                  />
                }
              >
                <DotsThreeIcon weight="bold" />
              </DropdownMenuTrigger>
              <DropdownMenuContent align="end" className="w-64">
                <DropdownMenuItem
                  disabled={decide.isPending}
                  onClick={() => leave("dismiss")}
                >
                  <EyeSlashIcon />
                  <span className="grid">
                    Ignore
                    <span className="text-caption text-muted-foreground">
                      Close it without a note
                    </span>
                  </span>
                </DropdownMenuItem>
              </DropdownMenuContent>
            </DropdownMenu>
            <Popover>
              <PopoverTrigger
                render={
                  <Button
                    variant="outline"
                    size="sm"
                    disabled={decide.isPending}
                  />
                }
              >
                {busy("deny") && <Spinner />}
                Leave open…
              </PopoverTrigger>
              <PopoverContent align="end" className="grid w-80 gap-2">
                <textarea
                  aria-label={`Note for ${who}`}
                  placeholder={`Why, for ${who} (optional)`}
                  rows={3}
                  maxLength={4000}
                  value={note}
                  onChange={(e) => setNote(e.target.value)}
                  className={textareaClass}
                />
                <Button
                  size="sm"
                  variant="destructive"
                  className="justify-self-end"
                  disabled={decide.isPending}
                  onClick={() => leave("deny")}
                >
                  Leave them open
                </Button>
              </PopoverContent>
            </Popover>
            <Button size="sm" disabled={decide.isPending} onClick={resolve}>
              {busy("allow") && <Spinner />}
              Resolve {n === 1 ? "thread" : `${n} threads`}
            </Button>
          </div>
        </DecisionRow>
      )}
    </div>
  )
}
