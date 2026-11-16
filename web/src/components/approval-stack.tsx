import {
  useEffect,
  useRef,
  useState,
  useSyncExternalStore,
  type CSSProperties,
  type ReactNode,
} from "react"
import { useQueryClient } from "@tanstack/react-query"
import { toast } from "sonner"
import { Link } from "@tanstack/react-router"
import { CaretDownIcon, CaretUpIcon } from "@phosphor-icons/react"
import {
  ApprovalDecision,
  decideEach,
  decisionBody,
  refreshDecisions,
} from "@/components/approval-decision"
import { NetworkGroupCard } from "@/components/approval-group"
import {
  BrowserActionDecision,
  BrowserControlDecision,
  BrowserShowDecision,
  BrowserCredentialDecision,
} from "@/components/browser-approval"
import {
  GitApprovalSummary,
  useOpenGitReview,
  isGitApproval,
} from "@/components/git-review"
import {
  ConflictSheet,
  MemoryConflictDecision,
} from "@/components/memory-conflict"
import { GitCredentialDecision } from "@/components/git-credential"
import { SecretRejectedDecision } from "@/components/secret-rejected"
import { CredentialIssueDecision } from "@/components/credential-issue"
import { MemoryShareDecision } from "@/components/memory-sessions"
import { PermissionDecision } from "@/components/chat/permission-card"
import { Ident } from "@/components/ident"
import { ApprovalTip } from "@/components/feature-tip"
import { usePendingApprovals } from "@/hooks/use-pending-approvals"
import {
  shouldNotify,
  shouldNotifyConnections,
  useNotificationPermission,
} from "@/lib/notifications"
import { KindChip } from "@/components/kind-chip"
import { WaitingCount } from "@/components/waiting-count"
import { approvalHost } from "@/components/waiting-requests"
import { Button } from "@/components/ui/button"
import { ScrollArea } from "@/components/ui/scroll-area"
import {
  Card,
  CardContent,
  CardDescription,
  CardHeader,
  CardTitle,
} from "@/components/ui/card"
import { $api, type Approval } from "@/lib/api/client"
import { useClaimedApprovals } from "@/lib/approval-claims"
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
  isNetworkApproval,
} from "@/lib/approval-kinds"
import { cn, formatAge } from "@/lib/utils"
import {
  heldFor,
  isConnection,
  waitingConnections,
} from "@/lib/connection-holds"
import { appAllowAllEvent, appNotifies, notifyApp } from "@/lib/desktop"
import { useComposerClearance } from "@/lib/composer-clearance"

/** Cards the open stack lists; the rest are one link away. */
const maxCards = 3

/** The open stack's cards, which the pill and the crumb row's button name. */
const panelId = "approval-stack-cards"

// Whether the stack is open, and whether a crumb row holds its button, which on a phone takes
// the pill's place: up there it covers no composer or terminal keys.
let stackOpen = false
let buttonHosts = 0
const stackListeners = new Set<() => void>()
const subscribeStack = (l: () => void) => {
  stackListeners.add(l)
  return () => void stackListeners.delete(l)
}
const emitStack = () => stackListeners.forEach((l) => l())
const setStackOpen = (open: boolean) => {
  stackOpen = open
  emitStack()
}
const useStackOpen = () => useSyncExternalStore(subscribeStack, () => stackOpen)
const useButtonHosted = () =>
  useSyncExternalStore(subscribeStack, () => buttonHosts > 0)

/** The approvals the stack shows: those pending that the page doesn't show in place. */
function useStackItems() {
  const pending = usePendingApprovals()
  const claimed = useClaimedApprovals()
  const all = pending.data ?? []
  return { all, items: all.filter((a) => !claimed.has(a.id)) }
}

/**
 * The stack's button for the crumb row on a phone: the count of approvals, which opens their
 * cards below the row. It shows nothing while none wait.
 */
export function ApprovalsButton({ className }: { className?: string }) {
  const { items } = useStackItems()
  const expanded = useStackOpen()
  useEffect(() => {
    buttonHosts++
    emitStack()
    return () => {
      buttonHosts--
      emitStack()
    }
  }, [])
  if (items.length === 0) return null
  return (
    <Button
      variant="ghost"
      size="sm"
      aria-expanded={expanded}
      aria-controls={panelId}
      onClick={() => setStackOpen(!expanded)}
      className={cn("h-7 shrink-0 gap-1.5 px-1.5 text-xs", className)}
    >
      <WaitingCount count={items.length} />
      {items.length === 1 ? "approval" : "approvals"}
      {expanded ? <CaretUpIcon aria-hidden /> : <CaretDownIcon aria-hidden />}
    </Button>
  )
}

/**
 * Pending approvals, shown on every page: a held connection waits on the answer, a staged
 * git push waits for review. Approvals the page already shows in place are left out
 * (lib/approval-claims.ts), so each shows once.
 *
 * One approval shows its card at sm and wider. More than one, or a phone, folds the cards
 * into an "N approvals" pill that opens them on click, so the stack covers the page only when
 * someone asks for it (docs/design/ui-style.md).
 */
export function ApprovalStack() {
  const pending = usePendingApprovals()
  const { all, items } = useStackItems()
  const envs = $api.useQuery("get", "/api/environments")
  const notifications = useNotifications(pending.data)
  const expanded = useStackOpen()
  const hosted = useButtonHosted()
  // Requests the user chose to review one by one, which then no longer group.
  const [split, setSplit] = useState<ReadonlySet<string>>(() => new Set())
  useTitleCount(all.length)
  // 1rem up from the corner, or above the composer it would rest on, clear of its Send button.
  const [section, setSection] = useState<HTMLElement | null>(null)
  const bottom = useComposerClearance(section, 16)

  // What the page shows in place (a chat's permission cards, Home's and a teammate's cards,
  // the browser's questions) stays out of the stack.
  const { sheet, review } = useApprovalReview()
  const cards = approvalCards(items, split)
  if (cards.length === 0) return sheet
  const envName = (a: Approval) =>
    envs.data?.find((e) => e.id === a.environmentId)?.name

  // One card needs no pill on a wide page, so it shows there; below sm the CSS hides it until the
  // pill opens it. Several cards start folded. On a phone with a crumb row, its button stands in
  // for the pill and the cards open below the row.
  const single = cards.length === 1
  return (
    <section
      ref={setSection}
      aria-label="Approvals"
      style={{ "--stack-bottom": `${bottom}px` } as CSSProperties}
      className={cn(
        "fixed right-4 bottom-(--stack-bottom) z-40 flex max-w-[calc(100vw-2rem)] flex-col items-end gap-2",
        hosted && "max-sm:top-12 max-sm:bottom-auto"
      )}
    >
      <ScrollArea
        id={panelId}
        className={cn(
          "w-80 max-w-full",
          !expanded && (single ? "max-sm:hidden" : "hidden")
        )}
        // Lifted over a tall draft, the list keeps to the room left above it.
        viewportClassName="flex max-h-[min(60vh,calc(100dvh_-_var(--stack-bottom)_-_6rem))] flex-col gap-2 *:shrink-0 *:data-[slot=card]:shadow-lg"
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
        <ApprovalCardList
          cards={cards}
          max={maxCards}
          envName={envName}
          onReview={review}
          onSplit={(ids) => {
            setSplit((prev) => new Set([...prev, ...ids]))
            // The hosts become cards of their own, which would fold the stack away.
            setStackOpen(true)
          }}
        />
      </ScrollArea>
      <Button
        variant="outline"
        size="sm"
        aria-expanded={expanded}
        aria-controls={panelId}
        onClick={() => setStackOpen(!expanded)}
        className={cn(
          "rounded-full bg-card shadow-lg",
          single && "sm:hidden",
          hosted && "max-sm:hidden"
        )}
      >
        <WaitingCount count={items.length} />
        {items.length === 1 ? "approval" : "approvals"}
        {expanded ? <CaretDownIcon aria-hidden /> : <CaretUpIcon aria-hidden />}
      </Button>
      {sheet}
    </section>
  )
}

/**
 * Reviewing an approval: a git push opens in its computer's Changes tab, a memory conflict in a
 * sheet, which is rendered wherever its caller lists the cards.
 */
export function useApprovalReview(): {
  sheet: ReactNode
  review: (a: Approval) => void
} {
  const [conflict, setConflict] = useState<Approval>()
  const openReview = useOpenGitReview()
  const sheet = (
    <ConflictSheet
      env={conflict?.environmentId ?? ""}
      conflictId={conflict?.memoryConflict?.conflictId}
      onOpenChange={(open) => {
        if (!open) setConflict(undefined)
      }}
    />
  )
  return {
    sheet,
    review: (a) => {
      if (a.kind === "memory.conflict") setConflict(a)
      else {
        setStackOpen(false)
        void openReview(a)
      }
    },
  }
}

/**
 * Approval cards, up to max, then a link to the page that lists the rest. The stack shows them
 * floating; Home lists its environment's in its column.
 */
export function ApprovalCardList({
  cards,
  max,
  envName,
  onReview,
  onSplit,
}: {
  cards: ApprovalCard[]
  max: number
  envName: (a: Approval) => string | undefined
  onReview: (a: Approval) => void
  onSplit: (ids: string[]) => void
}) {
  return (
    <>
      {cards.slice(0, max).map((card) => (
        <StackCard
          key={cardApproval(card).id}
          card={card}
          envName={envName}
          onReview={onReview}
          onSplit={onSplit}
        />
      ))}
      {cards.length > max && (
        <MoreLink
          approval={cardApproval(cards[max])}
          count={cards.length - max}
        />
      )}
    </>
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
      search={
        approvalKind(approval).page === "/e/$env/teammates/$id"
          ? { tab: "browser" }
          : {}
      }
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
  const card = useRef<HTMLDivElement>(null)
  // The first held request and the first push explain themselves (docs/frontend.md#onboarding).
  const tip = isNetworkApproval(approval)
    ? "network"
    : approval.kind === "git.push" || approval.kind === "git.pr"
      ? "push"
      : undefined
  return (
    <Card
      ref={card}
      size="sm"
      className={cn("gap-2 border-l-[3px]", ruleClass(kind.area))}
      data-testid="approval-card"
    >
      {tip && (
        <ApprovalTip env={approval.environmentId} feature={tip} anchor={card} />
      )}
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
        ) : approval.kind === "browser.control" ? (
          <BrowserControlDecision approval={approval} />
        ) : approval.kind === "browser.show" ? (
          <BrowserShowDecision approval={approval} />
        ) : approval.kind === "git.credential" ? (
          <GitCredentialDecision approval={approval} />
        ) : approval.kind === "secret.rejected" ? (
          <SecretRejectedDecision approval={approval} />
        ) : approval.kind === "credential.issue" ? (
          <CredentialIssueDecision approval={approval} />
        ) : approval.kind === "chat.permission" && approval.chatPermission ? (
          <ChatPermissionSummary approval={approval} />
        ) : isNetworkApproval(approval) ? (
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
        <ul className="grid gap-0.5 font-mono text-caption text-muted-foreground">
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

/** How long a sandbox's connections gather into one notification, in ms. */
const gatherConnections = 3000
/** With more hosts than these a notification can't name, it offers no Allow all. */
const noticeHosts = 3

/**
 * Notifies about approvals that arrive while the window is not in front. The ones pending on
 * the first load are only remembered, so opening the app does not notify. A sandbox's held
 * connections gather into one notification, and one held again after it gave up counts as new.
 */
function useNotifications(data: Approval[] | null | undefined) {
  const { permission, request } = useNotificationPermission()
  const queryClient = useQueryClient()
  const seen = useRef<Map<string, string | undefined> | null>(null)
  const latest = useRef(data)
  const gathering = useRef(new Map<string, number>())
  // What each notification's Allow all answers: what it named, and nothing that came after.
  const named = useRef(new Map<string, Approval[]>())

  useEffect(() => {
    const timers = gathering.current
    return () => timers.forEach((t) => clearTimeout(t))
  }, [])

  useEffect(() => {
    const allowAll = (e: Event) => {
      const list = named.current.get((e as CustomEvent<string>).detail)
      if (!list) return
      // The narrowest rule: nobody saw Options, and the rule outlives this click.
      void decideEach(list, (a) =>
        decisionBody(a, "allow", {
          lifetime: "always",
          scope: "sandbox",
          reach: "browser",
          host: a.network?.host,
        })
      ).then((failure) => {
        refreshDecisions(queryClient)
        if (failure) {
          toast.error("Could not allow every host", { description: failure })
        }
      })
    }
    window.addEventListener(appAllowAllEvent, allowAll)
    return () => window.removeEventListener(appAllowAllEvent, allowAll)
  }, [queryClient])

  useEffect(() => {
    if (!data) return
    latest.current = data
    const first = seen.current === null
    const known = seen.current ?? new Map<string, string | undefined>()
    const gather = (sandboxId: string) => {
      if (gathering.current.has(sandboxId)) return
      gathering.current.set(
        sandboxId,
        window.setTimeout(() => {
          gathering.current.delete(sandboxId)
          const held = waitingConnections(latest.current, sandboxId).filter(
            (a) => heldFor(a, Date.now()) !== undefined
          )
          if (held.length === 0 || !shouldNotifyConnections()) return
          named.current.set(noticeId(sandboxId), held)
          notifyConnections(sandboxId, held)
        }, gatherConnections)
      )
    }
    for (const a of data) {
      const isNew = !known.has(a.id)
      const before = known.get(a.id)
      known.set(a.id, a.heldUntil)
      if (first) continue
      const heldAgain =
        !!a.heldUntil && (!before || Date.parse(before) <= Date.now())
      if (isConnection(a) && a.sandboxId) {
        if (isNew || heldAgain) gather(a.sandboxId)
      } else if ((isNew || (a.network?.write && heldAgain)) && shouldNotify()) {
        // A retried write is the same approval, held again.
        notify(a)
      }
    }
    seen.current = known
  }, [data])

  return {
    canAsk: permission === "default",
    request,
  }
}

const noticeId = (sandboxId: string) => `connections-${sandboxId}`

/** One sandbox's held connections as one notification, which replaces the one before it. */
function notifyConnections(sandboxId: string, held: Approval[]) {
  const id = noticeId(sandboxId)
  const title = `${approvalSource(held[0]) ?? "A sandbox"} is waiting on a connection`
  const names = held.slice(0, noticeHosts).map(approvalHost).join(", ")
  const more = held.length - noticeHosts
  const body = more > 0 ? `${names} and ${more} more` : names
  try {
    if (appNotifies) {
      notifyApp({ id, title, body, allowAll: more <= 0 })
      return
    }
    const n = new Notification(title, { body, tag: id })
    n.onclick = () => {
      window.focus()
      n.close()
    }
  } catch {
    // Some browsers only show notifications from a service worker; the page still shows them.
  }
}

function notify(a: Approval) {
  try {
    const kind = approvalKind(a)
    const source = approvalSource(a)
    const summary = kind.summary(a)
    const body = source ? `${source}: ${summary}` : summary
    if (appNotifies) {
      notifyApp({ id: a.id, title: kind.title, body })
      return
    }
    const n = new Notification(kind.title, {
      body,
      tag: `${a.id}/${a.attempts}`, // the same tag would replace it silently
    })
    n.onclick = () => {
      window.focus()
      n.close()
    }
  } catch {
    // Some browsers only show notifications from a service worker; the stack still shows the request.
  }
}

/**
 * Prefixes the tab title with the number of pending approvals, as chat apps do for unread
 * messages, and puts it on the app's icon when Studio is installed as an app.
 */
function useTitleCount(count: number) {
  useEffect(() => {
    const base = document.title.replace(/^\(\d+\) /, "")
    document.title = count > 0 ? `(${count}) ${base}` : base
    setAppBadge(count)
    return () => {
      document.title = base
      setAppBadge(0)
    }
  }, [count])
}

/** The Badging API, where the browser has it; it shows only for an installed app. */
function setAppBadge(count: number) {
  if (!("setAppBadge" in navigator)) return
  const done =
    count > 0 ? navigator.setAppBadge(count) : navigator.clearAppBadge()
  done.catch(() => {})
}
