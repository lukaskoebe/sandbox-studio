import { useState } from "react"
import { useDecideApproval } from "@/components/approval-decision"
import { Button } from "@/components/ui/button"
import { Input } from "@/components/ui/input"
import { Textarea } from "@/components/ui/textarea"
import type { Approval } from "@/lib/api/client"

/**
 * GitHub's new-issue page takes the title and text from its address, which browsers and GitHub
 * cut off somewhere past 8,000 characters: a longer report asks to be shortened.
 */
const maxURL = 7_500

/** GitHub's new-issue page for a report, filled in. */
export function reportIssueURL(base: string, title: string, body: string) {
  const u = new URL(base)
  u.searchParams.set("title", title)
  u.searchParams.set("body", body)
  return u.toString()
}

/**
 * Answers a studio.report card: an agent found something in Studio or its computer that
 * confused or misled it, and offers to report it. Studio took out the names, addresses and
 * paths it found; the user reads and edits the report here and opens it as a GitHub issue,
 * from their browser and under their own account. Don't send sends nothing.
 */
export function ReportDecision({ approval }: { approval: Approval }) {
  const r = approval.report
  const decide = useDecideApproval()
  const [title, setTitle] = useState(r?.title ?? "")
  const [body, setBody] = useState(r?.body ?? "")
  if (!r) return null
  const env = approval.environmentId
  const url = reportIssueURL(
    r.issueUrl,
    title.trim(),
    `${body.trim()}\n\n---\n${r.footer}`
  )
  const tooLong = url.length > maxURL
  const ready = !decide.isPending && !tooLong && !!title.trim() && !!body.trim()
  const send = (action: "allow" | "dismiss") =>
    decide.mutate({
      params: { path: { env, id: approval.id } },
      body: { action, scope: "environment", remember: false },
    })
  return (
    <div className="grid min-w-0 gap-2 text-xs">
      <p className="text-muted-foreground">
        {r.replaced === 0
          ? "Studio found no names, addresses or paths to take out."
          : r.replaced === 1
            ? "Studio took out one name, address or path it found."
            : `Studio took out ${r.replaced} names, addresses and paths it found.`}{" "}
        Read it before you send it: the issue on{" "}
        <span className="font-mono text-foreground">{r.repository}</span> is
        public.
      </p>
      <Input
        aria-label="Title"
        value={title}
        maxLength={200}
        onChange={(e) => setTitle(e.target.value)}
        className="h-8 text-xs"
      />
      <Textarea
        aria-label="Report"
        value={body}
        onChange={(e) => setBody(e.target.value)}
        className="max-h-72 min-h-28 font-mono text-xs"
      />
      <p className="text-meta text-muted-foreground">{r.footer}</p>
      {tooLong && (
        <p className="text-destructive" role="alert">
          Too long for GitHub's link: shorten it by {url.length - maxURL}{" "}
          characters.
        </p>
      )}
      <div className="flex flex-wrap items-center gap-1.5">
        <Button
          variant="ghost"
          size="xs"
          disabled={decide.isPending}
          onClick={() => send("dismiss")}
        >
          Don't send
        </Button>
        <Button
          size="sm"
          className="ml-auto"
          disabled={!ready}
          title="Opens GitHub's new-issue page with the report filled in: you send it there"
          render={
            <a
              // Without an address while it can't go, so no key opens it either.
              role="link"
              href={ready ? url : undefined}
              target="_blank"
              rel="noreferrer"
              onClick={() => ready && send("allow")}
            />
          }
        >
          Open on GitHub…
        </Button>
      </div>
    </div>
  )
}
