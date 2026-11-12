import { useState } from "react"
import { useQueryClient } from "@tanstack/react-query"
import { toast } from "sonner"
import {
  ArrowSquareOutIcon,
  DotsThreeIcon,
  EyeSlashIcon,
} from "@phosphor-icons/react"
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
import { Ident } from "@/components/ident"
import { StatusDot } from "@/components/status-dot"
import { textareaClass } from "@/components/memory-dialogs"
import {
  rememberAttribution,
  rememberedAttribution,
} from "@/components/forge-review"
import {
  $api,
  errorMessage,
  errorStatus,
  type Approval,
} from "@/lib/api/client"

type Draft = NonNullable<Approval["forgeComment"]>

/** The pull request a comment is on, as owner/repo#number. */
export function commentPullRequest(d: Draft) {
  return `${d.owner}/${d.repo}#${d.number}`
}

/** What reviewing a git.comment approval decides, under its title. */
export function forgeCommentDescription(a: Approval) {
  const d = a.forgeComment
  const who = d?.teammate || "The agent"
  const where = d && !d.teammate ? ` in ${d.sandbox}` : ""
  return `${who}${where} wants to comment on a pull request's conversation. Nothing reaches the forge until you post it, as you, with your edits.`
}

/** What became of a decided comment. */
export function ForgeCommentOutcome({ draft }: { draft: Draft }) {
  switch (draft.state) {
    case "posted":
      return draft.postedUrl?.startsWith("http") ? (
        <a
          href={draft.postedUrl}
          target="_blank"
          rel="noreferrer"
          className="text-xs underline underline-offset-4"
        >
          Comment posted
        </a>
      ) : (
        <span className="text-xs">Comment posted</span>
      )
    case "failed":
      return (
        <span className="flex min-w-0 items-center gap-1.5 text-xs">
          <StatusDot status="failed" />
          <span className="truncate text-muted-foreground">
            {draft.result || "Posting failed"}
          </span>
        </span>
      )
    case "discarded":
      return (
        <span className="truncate text-xs text-muted-foreground">
          Discarded{draft.note ? ` · Note: ${draft.note}` : ""}
        </span>
      )
    default:
      return <span className="text-xs text-muted-foreground">Posting…</span>
  }
}

/** A compact card body for the approval stack: the comment's start, and a button to review it. */
export function ForgeCommentSummary({
  approval,
  onReview,
}: {
  approval: Approval
  onReview: () => void
}) {
  const d = approval.forgeComment
  return (
    <div className="grid gap-2 text-xs">
      {d && (
        <p className="line-clamp-3 break-words whitespace-pre-wrap text-muted-foreground">
          {d.body}
        </p>
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

/** The full comment: the pull request, the editable body, and the decision while pending. */
export function ForgeComment({ approval }: { approval: Approval }) {
  const draft = approval.forgeComment
  const who = draft ? draft.teammate || draft.sandbox : ""
  const [body, setBody] = useState(draft?.body ?? "")
  const [attribution, setAttribution] = useState(
    () => !!draft?.attribution || (!!who && rememberedAttribution(who))
  )
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
        const d = a.forgeComment
        if (vars.body.action !== "allow" || !d) return
        if (d.state === "posted") toast.success("Comment posted")
        else if (d.state === "failed")
          toast.error("The comment wasn't posted", { description: d.result })
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
  const empty = !body.trim()
  const post = () =>
    decide.mutate({
      ...params,
      body: {
        action: "allow",
        scope: "sandbox",
        comment: { body: body.trim(), attribution },
      },
    })
  const discard = (action: "deny" | "dismiss") =>
    decide.mutate({
      ...params,
      body: { action, scope: "sandbox", note: note.trim() || undefined },
    })

  return (
    <div className="grid min-w-0 gap-4">
      <header className="flex min-w-0 flex-wrap items-baseline gap-x-2 gap-y-1">
        <Ident>{commentPullRequest(draft)}</Ident>
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
            <ForgeCommentOutcome draft={draft} />
          </dd>
        </dl>
      )}

      <section aria-label="Comment" className="grid gap-3">
        <label className="grid gap-1.5 text-xs">
          <span className="font-medium">Comment</span>
          <textarea
            rows={8}
            maxLength={20000}
            value={body}
            readOnly={!pending}
            onChange={(e) => setBody(e.target.value)}
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
            End with “Comment by {draft.teammate || "an agent"}
            {draft.teammate ? " (agent)" : ` in ${draft.sandbox}`}”
            <span className="text-muted-foreground">
              {" "}
              · off by default, remembered per teammate
            </span>
          </span>
        </label>
      </section>

      {pending && (
        <div
          role="group"
          aria-label="Decision"
          className="sticky bottom-0 z-10 flex flex-wrap items-center gap-1.5 rounded-lg border bg-background/95 p-2 shadow-md backdrop-blur"
        >
          <p className="min-w-0 flex-1 basis-56 text-xs text-muted-foreground">
            Posts as <Ident>{draft.account || "you"}</Ident> on{" "}
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
                  onClick={() => discard("dismiss")}
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
                Discard…
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
                  onClick={() => discard("deny")}
                >
                  Discard the comment
                </Button>
              </PopoverContent>
            </Popover>
            <Button
              size="sm"
              disabled={decide.isPending || empty}
              title={empty ? "Write the comment first" : undefined}
              onClick={post}
            >
              {busy("allow") && <Spinner />}
              Post comment
            </Button>
          </div>
        </div>
      )}
    </div>
  )
}
