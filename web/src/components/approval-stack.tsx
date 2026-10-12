import { useEffect, useRef, useState } from "react"
import { Link, useMatchRoute } from "@tanstack/react-router"
import { ApprovalDecision } from "@/components/approval-decision"
import {
  BrowserActionDecision,
  BrowserCredentialDecision,
} from "@/components/browser-approval"
import {
  GitApprovalSummary,
  GitReviewSheet,
  isGitApproval,
} from "@/components/git-review"
import {
  ConflictSheet,
  MemoryConflictDecision,
} from "@/components/memory-conflict"
import { MemoryShareDecision } from "@/components/memory-sessions"
import { PermissionDecision } from "@/components/chat/permission-card"
import { Button } from "@/components/ui/button"
import {
  Card,
  CardContent,
  CardDescription,
  CardHeader,
  CardTitle,
} from "@/components/ui/card"
import { $api, type Approval } from "@/lib/api/client"
import {
  approvalKind,
  approvalSource,
  approvalTeammateId,
} from "@/lib/approval-kinds"
import { cn, formatAge } from "@/lib/utils"

/** Cards shown at sm and wider; below that only the newest one is. */
const maxCards = 3

/**
 * Pending approvals, shown on every page: a held connection waits on the answer, a staged
 * git push waits for review. The network page lists its own environment's network requests
 * itself, so the stack leaves them out there.
 */
export function ApprovalStack() {
  const pending = $api.useQuery(
    "get",
    "/api/approvals",
    { params: { query: { status: "pending" } } },
    { refetchInterval: 15000 }
  )
  const envs = $api.useQuery("get", "/api/environments")
  const matchRoute = useMatchRoute()
  const notifications = useNotifications(pending.data)
  const [reviewing, setReviewing] = useState<string>()
  const [conflict, setConflict] = useState<Approval>()
  const all = pending.data ?? []
  useTitleCount(all.length)

  // Pages that show these requests themselves: the network page its environment's
  // network requests, a chat its own permission requests.
  const onNetwork = matchRoute({ to: "/e/$env/network" })
  const onChat = matchRoute({ to: "/e/$env/chats/$chat" })
  const items = all.filter(
    (a) =>
      !(
        onNetwork &&
        a.kind === "network" &&
        a.environmentId === onNetwork.env
      ) && !(onChat && a.chatPermission?.chatId === onChat.chat)
  )
  const review = all.find((a) => a.id === reviewing)
  const sheet = (
    <>
      <GitReviewSheet
        approval={review}
        onOpenChange={(open) => {
          if (!open) setReviewing(undefined)
        }}
      />
      <ConflictSheet
        env={conflict?.environmentId ?? ""}
        conflictId={conflict?.memoryConflict?.conflictId}
        onOpenChange={(open) => {
          if (!open) setConflict(undefined)
        }}
      />
    </>
  )
  if (items.length === 0) return sheet

  return (
    <section
      aria-label="Approvals"
      className="fixed right-4 bottom-4 z-40 flex w-80 max-w-[calc(100vw-2rem)] flex-col gap-2"
    >
      <div className="flex items-center gap-2 px-1 text-xs">
        <span className="font-medium">Approvals</span>
        <span className="text-muted-foreground">{items.length}</span>
        {notifications.canAsk && (
          <Button
            variant="link"
            size="xs"
            className="ml-auto"
            onClick={notifications.request}
          >
            Enable notifications
          </Button>
        )}
      </div>
      {items.slice(0, maxCards).map((a, i) => (
        <div key={a.id} className={cn(i > 0 && "hidden sm:block")}>
          <ApprovalCard
            approval={a}
            envName={envs.data?.find((e) => e.id === a.environmentId)?.name}
            onReview={() =>
              a.kind === "memory.conflict" ? setConflict(a) : setReviewing(a.id)
            }
          />
        </div>
      ))}
      {items.length > 1 && (
        <MoreLink
          approval={items[1]}
          count={items.length - 1}
          className="sm:hidden"
        />
      )}
      {items.length > maxCards && (
        <MoreLink
          approval={items[maxCards]}
          count={items.length - maxCards}
          className="hidden sm:block"
        />
      )}
      {sheet}
    </section>
  )
}

/** Links to the environment page that lists the next hidden approval's kind. */
function MoreLink({
  approval,
  count,
  className,
}: {
  approval: Approval
  count: number
  className?: string
}) {
  return (
    <Link
      to={approvalKind(approval).page}
      params={{
        env: approval.environmentId,
        id: approvalTeammateId(approval) ?? "",
      }}
      className={cn(
        "px-1 text-xs text-muted-foreground underline-offset-4 hover:text-foreground hover:underline",
        className
      )}
    >
      +{count} more
    </Link>
  )
}

function ApprovalCard({
  approval,
  envName,
  onReview,
}: {
  approval: Approval
  envName?: string
  onReview: () => void
}) {
  const kind = approvalKind(approval)
  const details = [
    approvalSource(approval),
    envName,
    formatAge(approval.createdAt),
    approval.attempts > 1 ? `${approval.attempts} attempts` : undefined,
  ]
    .filter(Boolean)
    .join(" · ")
  return (
    <Card size="sm" className="gap-2 shadow-lg" data-testid="approval-card">
      <CardHeader>
        <CardTitle className="flex items-start gap-1.5">
          <kind.icon
            className="mt-0.5 shrink-0 text-muted-foreground"
            aria-label={kind.title}
          />
          <span
            className={cn(
              "min-w-0",
              kind.mono ? "font-mono break-all" : "break-words"
            )}
          >
            {kind.summary(approval)}
          </span>
        </CardTitle>
        <CardDescription>{details}</CardDescription>
      </CardHeader>
      <CardContent>
        {isGitApproval(approval) ? (
          <GitApprovalSummary approval={approval} onReview={onReview} />
        ) : approval.kind === "memory.share" ? (
          <MemoryShareDecision approval={approval} />
        ) : approval.kind === "memory.conflict" ? (
          <MemoryConflictDecision approval={approval} onResolve={onReview} />
        ) : approval.kind === "browser.action" ? (
          <BrowserActionDecision approval={approval} />
        ) : approval.kind === "browser.credential" ? (
          <BrowserCredentialDecision approval={approval} />
        ) : approval.kind === "chat.permission" && approval.chatPermission ? (
          <ChatPermissionSummary approval={approval} />
        ) : approval.kind === "network" ? (
          <ApprovalDecision approval={approval} compact />
        ) : (
          <p className="text-xs text-muted-foreground" role="status">
            This approval type is not supported by this version of Studio.
          </p>
        )}
      </CardContent>
    </Card>
  )
}

/** An agent's permission request: the files it touches, a link to its chat, and the decision. */
function ChatPermissionSummary({ approval }: { approval: Approval }) {
  const p = approval.chatPermission
  if (!p) return null
  const env = approval.environmentId
  return (
    <div className="grid gap-2 text-xs">
      {p.diffs && p.diffs.length > 0 && (
        <ul className="grid gap-0.5 font-mono text-[0.6875rem] text-muted-foreground">
          {p.diffs.slice(0, 3).map((d) => (
            <li key={d.path} className="flex gap-1.5">
              <span className="min-w-0 flex-1 truncate" title={d.path}>
                {d.path}
              </span>
              <span className="text-emerald-700 dark:text-emerald-400">
                +{d.added}
              </span>
              <span className="text-destructive">−{d.removed}</span>
            </li>
          ))}
          {p.diffs.length > 3 && <li>and {p.diffs.length - 3} more files</li>}
        </ul>
      )}
      <Link
        to="/e/$env/chats/$chat"
        params={{ env, chat: p.chatId }}
        className="text-muted-foreground underline-offset-2 hover:underline"
      >
        In {p.teammateName}'s chat “{p.chatTitle || "New chat"}”
      </Link>
      <PermissionDecision
        env={env}
        approvalId={approval.id}
        options={p.options ?? []}
      />
    </div>
  )
}

function notificationPermission(): NotificationPermission | "unsupported" {
  return typeof Notification === "undefined"
    ? "unsupported"
    : Notification.permission
}

/**
 * Notifies about approvals that arrive while the tab is in the background. The ones pending on
 * the first load are only remembered, so opening the app does not notify.
 */
function useNotifications(data: Approval[] | null | undefined) {
  const [permission, setPermission] = useState(notificationPermission)
  const seen = useRef<Set<string> | null>(null)

  useEffect(() => {
    if (!data) return
    const first = seen.current === null
    const known = seen.current ?? new Set<string>()
    for (const a of data) {
      if (known.has(a.id)) continue
      known.add(a.id)
      if (
        !first &&
        document.visibilityState !== "visible" &&
        notificationPermission() === "granted"
      ) {
        notify(a)
      }
    }
    seen.current = known
  }, [data])

  return {
    canAsk: permission === "default",
    request: () => {
      Notification.requestPermission().then(setPermission)
    },
  }
}

function notify(a: Approval) {
  try {
    const kind = approvalKind(a)
    const source = approvalSource(a)
    const summary = kind.summary(a)
    const n = new Notification(kind.title, {
      body: source ? `${source}: ${summary}` : summary,
      tag: a.id,
    })
    n.onclick = () => {
      window.focus()
      n.close()
    }
  } catch {
    // Some browsers only show notifications from a service worker; the stack still shows the request.
  }
}

/** Prefixes the tab title with the number of pending approvals, as chat apps do for unread messages. */
function useTitleCount(count: number) {
  useEffect(() => {
    const base = document.title.replace(/^\(\d+\) /, "")
    document.title = count > 0 ? `(${count}) ${base}` : base
    return () => {
      document.title = base
    }
  }, [count])
}
