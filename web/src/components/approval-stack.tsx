import { useEffect, useRef, useState } from "react"
import { Link, useMatchRoute } from "@tanstack/react-router"
import { ApprovalDecision } from "@/components/approval-decision"
import {
  GitApprovalSummary,
  GitReviewSheet,
  gitSummary,
  isGitApproval,
} from "@/components/git-review"
import { Button } from "@/components/ui/button"
import {
  Card,
  CardContent,
  CardDescription,
  CardHeader,
  CardTitle,
} from "@/components/ui/card"
import { $api, type Approval } from "@/lib/api/client"
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
  const all = pending.data ?? []
  useTitleCount(all.length)

  const onNetwork = matchRoute({ to: "/e/$env/network" })
  const items = onNetwork
    ? all.filter((a) => isGitApproval(a) || a.environmentId !== onNetwork.env)
    : all
  const review = all.find((a) => a.id === reviewing)
  const sheet = (
    <GitReviewSheet
      approval={review}
      onOpenChange={(open) => {
        if (!open) setReviewing(undefined)
      }}
    />
  )
  if (items.length === 0) return sheet

  return (
    <div className="fixed right-4 bottom-4 z-40 flex w-80 max-w-[calc(100vw-2rem)] flex-col gap-2">
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
            onReview={() => setReviewing(a.id)}
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
    </div>
  )
}

/** Links to the page of the environment the next hidden request belongs to. */
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
      to={isGitApproval(approval) ? "/e/$env/git" : "/e/$env/network"}
      params={{ env: approval.environmentId }}
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
  const git = isGitApproval(approval)
  const details = [
    approval.network?.sandboxName ?? approval.git?.review?.sandbox,
    envName,
    formatAge(approval.createdAt),
    approval.attempts > 1 ? `${approval.attempts} attempts` : undefined,
  ]
    .filter(Boolean)
    .join(" · ")
  return (
    <Card size="sm" className="gap-2 shadow-lg">
      <CardHeader>
        <CardTitle className={cn("break-all", !git && "font-mono")}>
          {git ? gitSummary(approval) : approval.subject}
        </CardTitle>
        <CardDescription>{details}</CardDescription>
      </CardHeader>
      <CardContent>
        {git ? (
          <GitApprovalSummary approval={approval} onReview={onReview} />
        ) : (
          <ApprovalDecision approval={approval} compact />
        )}
      </CardContent>
    </Card>
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
    const git = isGitApproval(a)
    const n = new Notification(
      git ? "Git review requested" : "Network access requested",
      {
        body: git
          ? gitSummary(a)
          : `${a.network?.sandboxName ?? "A sandbox"} wants to connect to ${a.subject}`,
        tag: a.id,
      }
    )
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
