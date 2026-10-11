import { useState } from "react"
import { ShieldCheckIcon } from "@phosphor-icons/react"
import { useDecideApproval } from "@/components/approval-decision"
import { Diff, Locations, toolKind } from "@/components/chat/tool-call-card"
import { Badge } from "@/components/ui/badge"
import { Button } from "@/components/ui/button"
import { Checkbox } from "@/components/ui/checkbox"
import { ScrollArea } from "@/components/ui/scroll-area"
import { Spinner } from "@/components/ui/spinner"
import type { ChatItem, PermissionOption } from "@/lib/chats"

type Action = "allow" | "deny" | "dismiss"

/**
 * Allow, deny or ignore an agent's permission request. Studio maps the decision to one of
 * the options the agent offered (docs/design/chat-mode.md §2): "Don't ask again" picks an
 * always-option where there is one, and Ignore cancels the request. The approval stack
 * and the chat's permission card both decide through this.
 */
export function PermissionDecision({
  env,
  approvalId,
  options,
}: {
  env: string
  approvalId: string
  options: PermissionOption[]
}) {
  const [remember, setRemember] = useState(false)
  const decide = useDecideApproval()
  const has = (kind: string) => options.some((o) => o.kind === kind)
  const canRemember = has("allow_always") || has("reject_always")
  // A remembered decision may fall back to its one-time option; a one-time one never
  // becomes permanent, so it needs its own option.
  const canAllow = has("allow_once") || (remember && has("allow_always"))
  const canDeny = has("reject_once") || (remember && has("reject_always"))
  const busy = (action: Action) =>
    decide.isPending && decide.variables.body.action === action
  const send = (action: Action) =>
    decide.mutate({
      params: { path: { env, id: approvalId } },
      body: {
        action,
        scope: "environment",
        remember: action !== "dismiss" && remember,
      },
    })
  return (
    <div className="grid gap-2">
      {canRemember && (
        <label className="flex items-center gap-2 text-xs">
          <Checkbox checked={remember} onCheckedChange={setRemember} />
          Don't ask again for this
        </label>
      )}
      <div className="flex items-center gap-1.5">
        <Button
          variant="ghost"
          size="xs"
          disabled={decide.isPending}
          title="Say no without an answer; the agent stops waiting"
          onClick={() => send("dismiss")}
        >
          {busy("dismiss") && <Spinner className="size-3" />}
          Ignore
        </Button>
        <Button
          variant="outline"
          size="sm"
          className="ml-auto"
          disabled={decide.isPending || !canDeny}
          onClick={() => send("deny")}
        >
          {busy("deny") && <Spinner />}
          Deny
        </Button>
        <Button
          size="sm"
          disabled={decide.isPending || !canAllow}
          onClick={() => send("allow")}
        >
          {busy("allow") && <Spinner />}
          Allow
        </Button>
      </div>
    </div>
  )
}

const outcomes: Record<string, string> = {
  approved: "Allowed",
  denied: "Denied",
  dismissed: "Ignored",
  cancelled: "No longer needed",
}

/** A permission request inside the chat: what the agent wants to do, and the decision. */
export function PermissionCard({
  env,
  item,
  teammateName,
}: {
  env: string
  item: ChatItem
  teammateName: string
}) {
  const p = item.permission
  if (!p) return null
  const tc = p.toolCall
  const kind = toolKind(tc.kind)
  const diffs = (tc.content ?? []).filter((c) => c.type === "diff")
  const pending = item.status === "pending"
  const chosen = p.options?.find((o) => o.optionId === p.outcome)
  const outcome =
    (outcomes[item.status ?? ""] ?? item.status) +
    (chosen?.kind.endsWith("_always") ? " from now on" : "")
  return (
    <div className="grid min-w-0 gap-2.5 rounded-lg border border-primary/30 bg-card p-2.5 text-xs">
      <div className="flex items-start gap-2">
        <ShieldCheckIcon className="mt-0.5 size-3.5 shrink-0 text-primary" />
        <div className="grid min-w-0 flex-1 gap-0.5">
          <p className="font-medium">{teammateName} asks before going on</p>
          <p className="flex min-w-0 items-center gap-1.5 text-muted-foreground">
            <kind.icon className="size-3 shrink-0" aria-label={kind.label} />
            <span className="min-w-0 wrap-break-word">
              {tc.title || kind.label}
            </span>
          </p>
        </div>
        {!pending && (
          <Badge variant="outline" className="shrink-0">
            {outcome}
          </Badge>
        )}
      </div>
      {(diffs.length > 0 || (tc.locations?.length ?? 0) > 0) && (
        <ScrollArea className="min-h-0" viewportClassName="max-h-60">
          <div className="grid min-w-0 gap-2">
            {tc.locations && tc.locations.length > 0 && (
              <Locations list={tc.locations} />
            )}
            {diffs.map((d, i) => (
              <Diff
                key={i}
                path={d.path}
                oldText={d.oldText}
                newText={d.newText ?? ""}
              />
            ))}
          </div>
        </ScrollArea>
      )}
      {pending && item.approvalId && (
        <PermissionDecision
          env={env}
          approvalId={item.approvalId}
          options={p.options ?? []}
        />
      )}
    </div>
  )
}
