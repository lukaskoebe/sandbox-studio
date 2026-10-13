import { useEffect, useId, useRef, useState } from "react"
import { Link, useMatchRoute } from "@tanstack/react-router"
import { CaretDownIcon, CaretUpIcon } from "@phosphor-icons/react"
import { ApprovalDecision } from "@/components/approval-decision"
import { NetworkGroupCard } from "@/components/approval-group"
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
import { Ident } from "@/components/ident"
import { KindChip } from "@/components/kind-chip"
import { WaitingCount } from "@/components/waiting-count"
import { Button } from "@/components/ui/button"
import {
  Card,
  CardContent,
  CardDescription,
  CardHeader,
  CardTitle,
} from "@/components/ui/card"
import { $api, type Approval } from "@/lib/api/client"
import { ruleClass } from "@/lib/accents"
import {
  approvalCards,
  cardApproval,
  type ApprovalCard,
} from "@/lib/approval-groups"
import {
  approvalKind,
  approvalSource,
  approvalTeammateId,
} from "@/lib/approval-kinds"
import { cn, formatAge } from "@/lib/utils"

/** Cards the open stack lists; the rest are one link away. */
const maxCards = 3

/**
 * Pending approvals, shown on every page: a held connection waits on the answer, a staged
 * git push waits for review. The network page's Requests tab lists its environment's network
 * requests itself, so the stack leaves them out there.
 *
 * One approval shows its card at sm and wider. More than one, or a phone, folds the cards
 * into an "N approvals" pill that opens them on click, so the stack covers the page only when
 * someone asks for it (docs/design/ui-style.md).
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
  const [expanded, setExpanded] = useState(false)
  const panelId = useId()
  // Requests the user chose to review one by one, which then no longer group.
  const [split, setSplit] = useState<ReadonlySet<string>>(() => new Set())
  const all = pending.data ?? []
  useTitleCount(all.length)

  // Pages that list these requests themselves: the network page's Requests tab lists its
  // environment's network requests, a chat lists its own permission requests.
  const onNetwork = matchRoute({ to: "/e/$env/network/requests" })
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
  const cards = approvalCards(items, split)
  if (cards.length === 0) return sheet
  const envName = (a: Approval) =>
    envs.data?.find((e) => e.id === a.environmentId)?.name

  // One card needs no pill on a wide page, so it shows there; below sm the CSS hides it until the
  // pill opens it. Several cards start folded.
  const single = cards.length === 1
  return (
    <section
      aria-label="Approvals"
      className="fixed right-4 bottom-4 z-40 flex max-w-[calc(100vw-2rem)] flex-col items-end gap-2"
    >
      <div
        id={panelId}
        className={cn(
          "flex max-h-[60vh] w-80 max-w-full flex-col gap-2 overflow-y-auto",
          !expanded && (single ? "max-sm:hidden" : "hidden")
        )}
      >
        {notifications.canAsk && (
          <Button
            variant="link"
            size="xs"
            className="self-end"
            onClick={notifications.request}
          >
            Enable notifications
          </Button>
        )}
        {cards.slice(0, maxCards).map((card) => (
          <StackCard
            key={cardApproval(card).id}
            card={card}
            envName={envName}
            onReview={(a) =>
              a.kind === "memory.conflict" ? setConflict(a) : setReviewing(a.id)
            }
            onSplit={(ids) => setSplit((prev) => new Set([...prev, ...ids]))}
          />
        ))}
        {cards.length > maxCards && (
          <MoreLink
            approval={cardApproval(cards[maxCards])}
            count={cards.length - maxCards}
          />
        )}
      </div>
      <Button
        variant="outline"
        size="sm"
        aria-expanded={expanded}
        aria-controls={panelId}
        onClick={() => setExpanded((o) => !o)}
        className={cn("rounded-full bg-card shadow-lg", single && "sm:hidden")}
      >
        <WaitingCount count={items.length} />
        {items.length === 1 ? "approval" : "approvals"}
        {expanded ? <CaretDownIcon aria-hidden /> : <CaretUpIcon aria-hidden />}
      </Button>
      {sheet}
    </section>
  )
}

/** One card of the stack: a single approval, or the network requests of one sandbox together. */
function StackCard({
  card,
  envName,
  onReview,
  onSplit,
}: {
  card: ApprovalCard
  envName: (a: Approval) => string | undefined
  onReview: (a: Approval) => void
  onSplit: (ids: string[]) => void
}) {
  if (card.kind === "single") {
    return (
      <ApprovalCard
        approval={card.approval}
        envName={envName(card.approval)}
        onReview={() => onReview(card.approval)}
      />
    )
  }
  return (
    <NetworkGroupCard
      approvals={card.approvals}
      envName={envName(card.approvals[0])}
      onReview={() => onSplit(card.approvals.map((a) => a.id))}
    />
  )
}

/** Links to the environment page that lists the next hidden approval's kind. */
function MoreLink({ approval, count }: { approval: Approval; count: number }) {
  return (
    <Link
      to={approvalKind(approval).page}
      params={{
        env: approval.environmentId,
        id: approvalTeammateId(approval) ?? "",
      }}
      className="px-1 text-xs text-muted-foreground underline-offset-4 hover:text-foreground hover:underline"
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
  const summary = kind.summary(approval)
  return (
    <Card
      size="sm"
      className={cn("gap-2 border-l-[3px] shadow-lg", ruleClass(kind.area))}
      data-testid="approval-card"
    >
      <CardHeader>
        <div className="flex flex-wrap items-center gap-x-2 gap-y-1">
          <KindChip area={kind.area} icon={kind.icon}>
            {kind.title}
          </KindChip>
          <CardDescription className="min-w-0 break-words">
            {details}
          </CardDescription>
        </div>
        <CardTitle className="min-w-0 break-words">
          {kind.mono ? <Ident>{summary}</Ident> : summary}
        </CardTitle>
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
