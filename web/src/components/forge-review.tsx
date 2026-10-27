import { useId, useState } from "react"
import { useQueryClient } from "@tanstack/react-query"
import { toast } from "sonner"
import {
  ArrowSquareOutIcon,
  ChatCircleTextIcon,
  DotsThreeIcon,
  EyeSlashIcon,
  InfoIcon,
  XIcon,
} from "@phosphor-icons/react"
import { Badge } from "@/components/ui/badge"
import { Button } from "@/components/ui/button"
import { Checkbox } from "@/components/ui/checkbox"
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
import { ToggleGroup, ToggleGroupItem } from "@/components/ui/toggle-group"
import { Ident } from "@/components/ident"
import { StatusDot } from "@/components/status-dot"
import { textareaClass } from "@/components/memory-dialogs"
import {
  FeedbackTargetSelect,
  UnifiedDiff,
  newChatTarget,
  parseDiff,
  useSendFeedback,
  type DiffLine,
  type FeedbackTarget,
  type LineComment,
} from "@/components/review-feedback"
import {
  $api,
  errorMessage,
  errorStatus,
  type Approval,
} from "@/lib/api/client"
import { plural, pullRequestName, verdictLabels } from "@/lib/approval-kinds"

type Detail = NonNullable<Approval["forgeReview"]>
type Draft = Detail["draft"]
type Verdict = Draft["verdict"]
type DraftComment = NonNullable<Draft["comments"]>[number]

const verdicts: { value: Verdict; label: string }[] = [
  { value: "comment", label: "Comment" },
  { value: "approve", label: "Approve" },
  { value: "request_changes", label: "Request changes" },
]

/** Who drafted a review: the teammate, or the sandbox for one without. */
function drafter(d: Draft) {
  return d.teammate || d.sandbox
}

/** What reviewing a git.review approval decides, under its title. */
export function forgeReviewDescription(a: Approval) {
  const d = a.forgeReview?.draft
  const who = d?.teammate || "The agent"
  const where = d && !d.teammate ? ` in ${d.sandbox}` : ""
  return `${who}${where} drafted a review of a pull request. Nothing reaches the forge until you post it, as you, with your edits; discarding it tells ${d?.teammate || "the agent"} your note.`
}

// The attribution line is a per-teammate choice remembered in this browser.
const attributionKey = (who: string) => `studio.review-attribution.${who}`

function rememberedAttribution(who: string) {
  try {
    return localStorage.getItem(attributionKey(who)) === "1"
  } catch {
    return false
  }
}

function rememberAttribution(who: string, on: boolean) {
  try {
    if (on) localStorage.setItem(attributionKey(who), "1")
    else localStorage.removeItem(attributionKey(who))
  } catch {
    // Storage can be unavailable (private windows); the checkbox still applies to this review.
  }
}

/** The index in lines of the diff line a draft comment is on, or -1. */
function lineIndex(lines: DiffLine[], c: DraftComment) {
  const removed = c.side === "old"
  return lines.findIndex(
    (l) => l.file === c.path && l.line === c.line && !!l.removed === removed
  )
}

/** The state of a decided review, for lists. */
export function ForgeReviewOutcome({ review }: { review: Detail["review"] }) {
  switch (review.state) {
    case "posted":
      return review.url?.startsWith("http") ? (
        <a
          href={review.url}
          target="_blank"
          rel="noreferrer"
          className="text-xs underline underline-offset-4"
        >
          Review posted
        </a>
      ) : (
        <span className="text-xs">Review posted</span>
      )
    case "failed":
      return (
        <span className="flex min-w-0 items-center gap-1.5 text-xs">
          <StatusDot status="failed" />
          <span className="truncate text-muted-foreground">
            {review.result || "Posting failed"}
          </span>
        </span>
      )
    case "discarded":
      return (
        <span className="truncate text-xs text-muted-foreground">
          Discarded{review.note ? ` · Note: ${review.note}` : ""}
        </span>
      )
    case "superseded":
      return (
        <span className="text-xs text-muted-foreground">
          Replaced by a newer draft
        </span>
      )
    default:
      return null
  }
}

/** A compact card body for the approval stack: the verdict, and a button to review it. */
export function ForgeReviewSummary({
  approval,
  onReview,
}: {
  approval: Approval
  onReview: () => void
}) {
  const d = approval.forgeReview?.draft
  return (
    <div className="flex items-center gap-2 text-xs">
      {d && (
        <span className="truncate text-muted-foreground">
          {verdictLabels[d.verdict]} ·{" "}
          {plural(d.comments?.length ?? 0, "comment")} · as {d.account || "you"}
          , on {d.forgeHost}
        </span>
      )}
      <Button size="sm" className="ml-auto" onClick={onReview}>
        Review
      </Button>
    </div>
  )
}

/** Where the pull request is now against the commit the draft was written for. */
function HeadNote({ approval, draft }: { approval: Approval; draft: Draft }) {
  const head = $api.useQuery(
    "get",
    "/api/environments/{env}/approvals/{id}/review-head",
    { params: { path: { env: approval.environmentId, id: approval.id } } },
    { retry: false, staleTime: 30_000 }
  )
  const h = head.data
  if (!h || h.sha === draft.pullRequest.head.sha) return null
  const who = drafter(draft)
  const text = h.merged
    ? "It was merged since the draft was written."
    : h.state === "closed"
      ? "It was closed since the draft was written."
      : h.rewritten
        ? `Its branch was rewritten since ${who} read it.`
        : h.newCommits > 0
          ? `${plural(h.newCommits, "new commit")} since ${who} read it.`
          : `It changed since ${who} read it.`
  return (
    <Badge variant="outline" className="font-sans">
      {text}
    </Badge>
  )
}

/**
 * The review a teammate drafted of a pull request on a forge: the verdict, the summary and
 * the line comments, all editable while it waits. Post review sends it as the user's account;
 * Discard drops it and tells the agent the note.
 */
export function ForgeReview({ approval }: { approval: Approval }) {
  const detail = approval.forgeReview
  const draft = detail?.draft
  const ids = useId()
  const lines = parseDiff(draft?.diff ?? "")
  const placed = (draft?.comments ?? []).map((c) => ({
    c,
    index: lineIndex(lines, c),
  }))
  const [verdict, setVerdict] = useState<Verdict>(draft?.verdict ?? "comment")
  const [summary, setSummary] = useState(draft?.summary ?? "")
  const [comments, setComments] = useState<LineComment[]>(() =>
    placed
      .filter((p) => p.index >= 0)
      .map((p) => ({ index: p.index, text: p.c.body }))
  )
  // Comments on lines the shown diff lacks (a truncated diff) post as drafted unless dropped.
  const [elsewhere, setElsewhere] = useState<DraftComment[]>(() =>
    placed.filter((p) => p.index < 0).map((p) => p.c)
  )
  const who = draft ? drafter(draft) : ""
  const [attribution, setAttribution] = useState(
    () => !!draft?.attribution || (!!who && rememberedAttribution(who))
  )
  const [discardNote, setDiscardNote] = useState("")
  const [target, setTarget] = useState<FeedbackTarget>(
    draft?.teammate ? newChatTarget : ""
  )
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
      onError: (err) => {
        if (errorStatus(err) === 409) return
        toast.error("Could not decide", { description: errorMessage(err) })
      },
    }
  )
  const feedback = useSendFeedback()
  if (!detail || !draft) return null
  const pr = draft.pullRequest
  const pending = approval.status === "pending"
  const params = {
    params: { path: { env: approval.environmentId, id: approval.id } },
  }
  const count = comments.length + elsewhere.length
  const additions = (pr.files ?? []).reduce((n, f) => n + f.additions, 0)
  const deletions = (pr.files ?? []).reduce((n, f) => n + f.deletions, 0)
  const busy = (action: string) =>
    decide.isPending && decide.variables.body.action === action
  // A new chat starts only when there is a note to tell it; a chat or terminal gets the outcome.
  const sends = (note: string) =>
    !!target && (target !== newChatTarget || !!note)
  const tell = (note: string) => {
    if (!sends(note)) return
    const body =
      target === newChatTarget
        ? { note, newChat: true }
        : target.startsWith("chat:")
          ? { note, chat: target.slice(5) }
          : { note, terminal: target.slice(9) }
    feedback.mutate({ ...params, body })
  }
  const post = () =>
    decide.mutate(
      {
        ...params,
        body: {
          action: "allow",
          scope: "sandbox",
          review: {
            verdict,
            summary: summary.trim(),
            attribution,
            comments: [
              ...[...comments]
                .sort((a, b) => a.index - b.index)
                .map(({ index, text }) => {
                  const l = lines[index]
                  return {
                    path: l.file ?? "",
                    line: l.line ?? 0,
                    side: l.removed ? ("old" as const) : ("new" as const),
                    body: text,
                  }
                }),
              ...elsewhere,
            ],
          },
        },
      },
      { onSuccess: () => tell("") }
    )
  const discard = (action: "deny" | "dismiss") => {
    const note = discardNote.trim()
    decide.mutate(
      {
        ...params,
        body: { action, scope: "sandbox", note: note || undefined },
      },
      { onSuccess: () => action === "deny" && tell(note) }
    )
  }
  // The forge takes an approval without text; anything else needs a summary.
  const empty = !summary.trim() && verdict !== "approve"

  return (
    <div className="grid min-w-0 gap-4">
      <header className="grid gap-1.5">
        <div className="flex min-w-0 flex-wrap items-baseline gap-x-2 gap-y-1">
          <Ident>{pullRequestName(draft)}</Ident>
          <span className="min-w-0 font-medium">{pr.title}</span>
          {pr.url?.startsWith("http") && (
            <Button
              variant="ghost"
              size="xs"
              className="ml-auto"
              render={<a href={pr.url} target="_blank" rel="noreferrer" />}
            >
              <ArrowSquareOutIcon />
              Open on the forge
            </Button>
          )}
        </div>
        <p className="flex flex-wrap items-center gap-x-1.5 gap-y-1 text-xs text-muted-foreground">
          <span>
            By {draft.ownPullRequest ? "you" : pr.author} ·{" "}
            <Ident>{`${pr.head.ref} → ${pr.base.ref}`}</Ident> ·{" "}
            {plural((pr.files?.length ?? 0) + (pr.moreFiles ? 1 : 0), "file")}
            {pr.moreFiles && "+"}{" "}
            <span className="text-emerald-600 dark:text-emerald-400">
              +{additions}
            </span>{" "}
            <span className="text-destructive">−{deletions}</span>
          </span>
          {pr.merged ? (
            <Badge variant="outline">merged</Badge>
          ) : (
            pr.state === "closed" && <Badge variant="outline">closed</Badge>
          )}
          {pending && <HeadNote approval={approval} draft={draft} />}
        </p>
        {pr.body && (
          <details className="text-xs">
            <summary className="cursor-pointer text-muted-foreground">
              Description
            </summary>
            <pre className="mt-1 max-h-60 overflow-auto rounded-md bg-muted/50 p-2 whitespace-pre-wrap">
              {pr.body}
            </pre>
          </details>
        )}
      </header>

      {!pending && (
        <dl className="grid grid-cols-[auto_1fr] gap-x-3 gap-y-1 text-xs">
          <dt className="text-muted-foreground">Outcome</dt>
          <dd className="min-w-0">
            <ForgeReviewOutcome review={detail.review} />
          </dd>
        </dl>
      )}

      <section aria-label="Review" className="grid gap-3">
        <ToggleGroup
          aria-label="Verdict"
          variant="outline"
          size="sm"
          spacing={0}
          value={[verdict]}
          disabled={!pending}
          onValueChange={(v) => {
            const next = verdicts.find((x) => x.value === v[0])
            if (next) setVerdict(next.value)
          }}
        >
          {verdicts.map((v) => (
            <ToggleGroupItem key={v.value} value={v.value}>
              {v.label}
            </ToggleGroupItem>
          ))}
        </ToggleGroup>
        {draft.ownPullRequest && verdict !== "comment" && (
          <p className="flex items-start gap-1.5 text-xs text-muted-foreground">
            <InfoIcon className="mt-0.5 size-3.5 shrink-0" />
            You opened this pull request, so the forge takes it as a comment
            that starts with the verdict.
          </p>
        )}
        <label className="grid gap-1.5 text-xs">
          <span className="font-medium">Summary</span>
          <textarea
            rows={4}
            maxLength={20000}
            value={summary}
            readOnly={!pending}
            onChange={(e) => setSummary(e.target.value)}
            className={textareaClass}
          />
        </label>
        <label className="flex items-center gap-2 text-xs">
          <Checkbox
            checked={attribution}
            disabled={!pending}
            onCheckedChange={(on) => {
              setAttribution(on)
              if (who) rememberAttribution(who, on)
            }}
          />
          <span>
            End with “Review by {draft.teammate || "an agent"}
            {draft.teammate ? " (agent)" : ` in ${draft.sandbox}`}”
            <span className="text-muted-foreground">
              {" "}
              · off by default, remembered per teammate
            </span>
          </span>
        </label>
      </section>

      <section aria-label="Changes" className="grid min-w-0 gap-2">
        {draft.diffTruncated && (
          <Badge variant="outline" className="justify-self-end">
            diff truncated
          </Badge>
        )}
        <UnifiedDiff
          diff={draft.diff}
          comments={comments}
          onComment={pending ? setComments : undefined}
          fill
          lineId={(i) => `${ids}-line-${i}`}
          commentLabel={draft.teammate ? `${draft.teammate}'s draft` : "Draft"}
        />
        {elsewhere.length > 0 && (
          <ul
            aria-label="Comments outside the diff shown"
            className="divide-y rounded-md text-xs ring-1 ring-foreground/10"
          >
            {elsewhere.map((c, i) => (
              <li
                key={`${c.path}:${c.line}:${i}`}
                className="flex items-start gap-2 px-2 py-1.5"
              >
                <ChatCircleTextIcon className="mt-0.5 shrink-0 text-muted-foreground" />
                <span className="grid min-w-0 flex-1 gap-0.5">
                  <Ident className="text-muted-foreground">{`${c.path}:${c.line}`}</Ident>
                  <span className="break-words whitespace-pre-wrap">
                    {c.body}
                  </span>
                </span>
                {pending && (
                  <Button
                    size="icon-xs"
                    variant="ghost"
                    aria-label="Remove the comment"
                    onClick={() =>
                      setElsewhere(elsewhere.filter((_, j) => j !== i))
                    }
                  >
                    <XIcon />
                  </Button>
                )}
              </li>
            ))}
          </ul>
        )}
      </section>

      {pending && (
        <div
          role="group"
          aria-label="Decision"
          className="sticky bottom-0 z-10 grid gap-2 rounded-lg border bg-background/95 p-2 shadow-md backdrop-blur"
        >
          <div className="flex flex-wrap items-center gap-1.5">
            <p className="min-w-0 flex-1 basis-56 text-xs text-muted-foreground">
              Posts as <Ident>{draft.account || "you"}</Ident> on{" "}
              {draft.forgeHost} · {plural(count, "comment")}
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
                    onClick={() => discard("dismiss")}
                  >
                    <EyeSlashIcon />
                    <span className="grid">
                      Ignore
                      <span className="text-caption text-muted-foreground">
                        Close it without an answer to the agent
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
                  Discard…
                </PopoverTrigger>
                <PopoverContent align="end" className="grid w-80 gap-2">
                  <textarea
                    aria-label={`Note for ${who}`}
                    placeholder={`Why, for ${who} (optional)`}
                    rows={3}
                    maxLength={4000}
                    value={discardNote}
                    onChange={(e) => setDiscardNote(e.target.value)}
                    className={textareaClass}
                  />
                  {approval.sandboxId && (
                    <FeedbackTargetSelect
                      env={approval.environmentId}
                      sandboxId={approval.sandboxId}
                      newChat={!!draft.teammate}
                      value={target}
                      onChange={setTarget}
                    />
                  )}
                  <Button
                    size="sm"
                    variant="destructive"
                    className="justify-self-end"
                    disabled={decide.isPending}
                    onClick={() => discard("deny")}
                  >
                    Discard the review
                  </Button>
                </PopoverContent>
              </Popover>
              <Button
                size="sm"
                disabled={decide.isPending || empty}
                title={empty ? "Write a summary first" : undefined}
                onClick={post}
              >
                {busy("allow") && <Spinner />}
                Post review
              </Button>
            </div>
          </div>
        </div>
      )}
    </div>
  )
}
