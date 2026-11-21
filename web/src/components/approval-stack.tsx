import {
  useEffect,
  useRef,
  useState,
  useSyncExternalStore,
  type ComponentProps,
  type ReactNode,
} from "react"
import { useQueryClient } from "@tanstack/react-query"
import { toast } from "sonner"
import { Link, useParams } from "@tanstack/react-router"
import { CaretDownIcon } from "@phosphor-icons/react"
import {
  ApprovalDecision,
  decideEach,
  decisionBody,
  initialDecision,
  initialDecisionBody,
  isBrowserRequest,
  refreshDecisions,
  ruleSummary,
  useDecideApproval,
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
import {
  PermissionDecision,
  PermissionInput,
} from "@/components/chat/permission-card"
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
import {
  Popover,
  PopoverContent,
  PopoverTrigger,
} from "@/components/ui/popover"
import { Spinner } from "@/components/ui/spinner"
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
import { useSettled } from "@/lib/settled"
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
import { LeadPlanSummary } from "@/components/lead/plan-approval"
import {
  heldFor,
  isConnection,
  useHoldClock,
  waitingConnections,
} from "@/lib/connection-holds"
import { appAllowAllEvent, appNotifies, notifyApp } from "@/lib/desktop"
import {
  useArrivals,
  useJustArrived,
  useRecordArrivals,
} from "@/lib/approval-arrivals"

/** The top bar's list, which its button opens and names. */
const listId = "approvals-list"

// Whether the top bar's list is open: reviewing an approval from it closes it.
let listOpen = false
const listListeners = new Set<() => void>()
const subscribeList = (l: () => void) => {
  listListeners.add(l)
  return () => void listListeners.delete(l)
}
const setListOpen = (open: boolean) => {
  listOpen = open
  listListeners.forEach((l) => l())
}
const useListOpen = () => useSyncExternalStore(subscribeList, () => listOpen)

/** The approvals pending, and those of them the page doesn't show in place. */
function usePendingItems() {
  const pending = usePendingApprovals()
  const claimed = useClaimedApprovals()
  const all = pending.data ?? []
  return { all, items: all.filter((a) => !claimed.has(a.id)) }
}

/**
 * What approvals do on every page without showing: notify while the window is behind others,
 * count in the tab's title, and note which arrive (lib/approval-arrivals.ts). The root mounts
 * it once; the top bar's ApprovalsButton shows them.
 */
export function ApprovalNotices() {
  const pending = usePendingApprovals()
  useNotifications(pending.data)
  useRecordArrivals(pending.data)
  useTitleCount(pending.data?.length ?? 0)
  return null
}

/**
 * The top bar's count of pending approvals, which opens them as a compact list under the bar,
 * as menus do, and closes again. Approvals the page shows in place are left out
 * (lib/approval-claims.ts), so each shows once: where the page shows them all, as Home and a
 * teammate's home do, it is only their count. A new one flashes the count once. Cards with
 * every detail and choice are on Home (docs/design/ui-style.md).
 */
export function ApprovalsButton({ className }: { className?: string }) {
  const { env } = useParams({ strict: false })
  const { all, items } = usePendingItems()
  const open = useListOpen()
  const arrivals = useArrivals()
  // Requests the user chose to see one by one, which then no longer group.
  const [split, setSplit] = useState<ReadonlySet<string>>(() => new Set())
  const { sheet, review } = useApprovalReview()
  const envs = $api.useQuery("get", "/api/environments")
  const { permission, request } = useNotificationPermission()
  const cards = approvalCards(items, split)
  const settled = useSettled(approvalIds(cards))
  // The list closes once nothing is left in it.
  useEffect(() => {
    if (cards.length === 0 && listOpen) setListOpen(false)
  }, [cards.length])
  if (all.length === 0) return sheet

  const n = cards.length > 0 ? items.length : all.length
  const count = (
    <>
      <WaitingCount
        key={arrivals}
        count={n}
        className={cn(arrivals > 0 && "motion-safe:animate-count-arrival")}
      />{" "}
      {n === 1 ? "needs you" : "need you"}
    </>
  )
  if (cards.length === 0)
    return (
      <>
        <span
          className={cn(
            "inline-flex h-7 shrink-0 items-center gap-1.5 px-1.5 text-xs text-muted-foreground",
            className
          )}
          title="Answer them on this page"
        >
          {count}
        </span>
        {sheet}
      </>
    )
  const envName = (a: Approval) =>
    envs.data?.find((e) => e.id === a.environmentId)?.name
  return (
    <>
      <Popover open={open} onOpenChange={setListOpen}>
        <PopoverTrigger
          render={
            <Button
              variant="outline"
              size="sm"
              aria-controls={listId}
              className={cn("h-7 shrink-0 gap-1.5 px-2 text-xs", className)}
            />
          }
        >
          {count}
          <CaretDownIcon aria-hidden className="size-3 opacity-70" />
        </PopoverTrigger>
        <PopoverContent
          align="end"
          className="w-104 max-w-[calc(100vw-1rem)] p-0"
          initialFocus={false}
        >
          <section id={listId} aria-label="Approvals" className="grid">
            <ScrollArea viewportClassName="max-h-[min(60vh,32rem)]">
              <ul className="grid divide-y">
                {cards.map((card) => (
                  <ApprovalRow
                    key={cardApproval(card).id}
                    card={card}
                    envName={envName}
                    onReview={review}
                    onSplit={(ids) =>
                      setSplit((prev) => new Set([...prev, ...ids]))
                    }
                    inert={!settled}
                  />
                ))}
              </ul>
            </ScrollArea>
            <footer className="flex flex-wrap items-center gap-x-3 gap-y-1 border-t px-3 py-2 text-caption text-muted-foreground">
              <span className="min-w-0 flex-1">
                Cards with the full detail are on Home.
              </span>
              {permission === "default" && (
                <Button variant="link" size="xs" onClick={request}>
                  Enable notifications
                </Button>
              )}
              {env && (
                <Link
                  to="/e/$env"
                  params={{ env }}
                  onClick={() => setListOpen(false)}
                  className="font-medium text-foreground underline-offset-4 hover:underline"
                >
                  Open Home →
                </Link>
              )}
            </footer>
          </section>
        </PopoverContent>
      </Popover>
      {sheet}
    </>
  )
}

/**
 * One approval of the top bar's list, or one sandbox's requests together: its kind, what it
 * asks, and the answer that needs no more choices. Permissions and single network requests
 * are allowed or denied here, as their cards start; a git ask opens for review, and the rest
 * open where their card is.
 */
function ApprovalRow({
  card,
  envName,
  onReview,
  onSplit,
  inert,
}: {
  card: ApprovalCard
  envName: (a: Approval) => string | undefined
  onReview: (a: Approval) => void
  onSplit: (ids: string[]) => void
  inert: boolean
}) {
  const a = cardApproval(card)
  const kind = approvalKind(a)
  const group = card.kind === "single" ? undefined : card.approvals
  const details = [approvalSource(a), envName(a), formatAge(a.createdAt)]
    .filter(Boolean)
    .join(" · ")
  const summary = group
    ? `${group.length} requests: ${group.map(approvalHost).join(", ")}`
    : kind.summary(a)
  // A connection the gateway holds says how long it waits; once it gave up, an answer is for
  // the next attempt.
  const connection = !group && (isConnection(a) || !!a.network?.write)
  const now = useHoldClock(connection ? [a] : [])
  const left = connection ? heldFor(a, now) : undefined
  let actions: ReactNode
  if (group)
    actions = (
      <Button
        size="sm"
        variant="outline"
        onClick={() => onSplit(group.map((g) => g.id))}
      >
        Show each
      </Button>
    )
  else if (a.kind === "chat.permission" && a.chatPermission)
    actions = <PermissionRowActions approval={a} />
  else if (isNetworkApproval(a))
    actions = (
      <NetworkRowActions
        approval={a}
        envName={envName(a)}
        gaveUp={connection && left === undefined}
      />
    )
  else if (isGitApproval(a) || a.kind === "memory.conflict")
    actions = (
      <Button size="sm" variant="outline" onClick={() => onReview(a)}>
        {a.kind === "memory.conflict" ? "Resolve" : "Review"}
      </Button>
    )
  else
    actions = (
      <Button
        size="sm"
        variant="outline"
        render={<CardPageLink approval={a} />}
      >
        Open
      </Button>
    )
  return (
    <li
      data-testid={group ? "approval-group" : "approval-card"}
      inert={inert}
      className="grid min-w-0 gap-1 px-3 py-2.5"
    >
      <div className="flex min-w-0 items-center gap-2">
        <KindChip area={kind.area} icon={kind.icon}>
          {kind.title}
        </KindChip>
        <span className="min-w-0 truncate text-caption text-muted-foreground">
          {details}
        </span>
        {connection && (
          <span
            data-testid="hold-status"
            className="ml-auto shrink-0 text-caption text-muted-foreground tabular-nums"
          >
            {left === undefined ? "No longer held" : `Held, ${left} s left`}
          </span>
        )}
      </div>
      <div className="flex min-w-0 items-center gap-3">
        <p className="line-clamp-2 min-w-0 flex-1 text-xs font-medium break-words">
          {kind.mono && !group ? <Ident>{summary}</Ident> : summary}
        </p>
        <div className="flex shrink-0 items-center gap-1.5">{actions}</div>
      </div>
    </li>
  )
}

/** Denies or allows an agent's permission request once, as its card's buttons do untouched. */
function PermissionRowActions({ approval }: { approval: Approval }) {
  const decide = useDecideApproval()
  const options = approval.chatPermission?.options ?? []
  const has = (kind: string) => options.some((o) => o.kind === kind)
  const busy = (action: string) =>
    decide.isPending && decide.variables.body.action === action
  const send = (action: "allow" | "deny") =>
    decide.mutate({
      params: { path: { env: approval.environmentId, id: approval.id } },
      body: { action, scope: "environment", remember: false },
    })
  return (
    <>
      <Button
        size="sm"
        variant="outline"
        disabled={decide.isPending || !has("reject_once")}
        onClick={() => send("deny")}
      >
        {busy("deny") && <Spinner />}
        Deny
      </Button>
      <Button
        size="sm"
        disabled={decide.isPending || !has("allow_once")}
        onClick={() => send("allow")}
      >
        {busy("allow") && <Spinner />}
        Allow
      </Button>
    </>
  )
}

/**
 * Denies or allows a network request as its card starts (initialDecision): the button's
 * tooltip says for whom and how long; the card on Home has the other choices.
 */
function NetworkRowActions({
  approval,
  envName,
  gaveUp,
}: {
  approval: Approval
  envName?: string
  gaveUp: boolean
}) {
  const decide = useDecideApproval()
  const d = initialDecision(approval)
  const rule = ruleSummary({
    lifetime: d.lifetime,
    scope: d.scope,
    reach: d.reach,
    browser: isBrowserRequest(approval),
    sandboxName: approval.network?.sandboxName,
    owner: approval.teammate?.name,
    envName,
  })
  const busy = (action: string) =>
    decide.isPending && decide.variables.body.action === action
  const send = (action: "allow" | "deny") =>
    decide.mutate({
      params: { path: { env: approval.environmentId, id: approval.id } },
      body: initialDecisionBody(approval, action),
    })
  return (
    <>
      <Button
        size="sm"
        variant="outline"
        title={rule}
        disabled={decide.isPending}
        onClick={() => send("deny")}
      >
        {busy("deny") && <Spinner />}
        Deny
      </Button>
      <Button
        size="sm"
        title={rule}
        disabled={decide.isPending}
        onClick={() => send("allow")}
      >
        {busy("allow") && <Spinner />}
        {gaveUp ? "Allow next time" : "Allow"}
      </Button>
    </>
  )
}

/** A link to the page that shows an approval's card: Home, or the teammate's browser. */
function CardPageLink({
  approval,
  ...props
}: { approval: Approval } & Omit<ComponentProps<"a">, "href">) {
  const page = approvalKind(approval).page
  return (
    <Link
      to={page}
      params={{
        env: approval.environmentId,
        id: approvalTeammateId(approval) ?? "",
      }}
      search={page === "/e/$env/teammates/$id" ? { tab: "browser" } : {}}
      onClick={() => setListOpen(false)}
      {...props}
    />
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
        setListOpen(false)
        void openReview(a)
      }
    },
  }
}

/**
 * Approval cards, up to max, then a link to the page that lists the rest. The stack shows them
 * floating; Home lists its environment's in its column. For half a second after the cards
 * change they ignore clicks, so that one meant for a card that just left, or moved, doesn't
 * answer the one that took its place. settleKey is what counts as a change, by default the
 * approvals these cards show; a page that lists its cards apart gives all of theirs.
 */
export function ApprovalCardList({
  cards,
  max,
  envName,
  onReview,
  onSplit,
  settleKey,
}: {
  cards: ApprovalCard[]
  max: number
  envName: (a: Approval) => string | undefined
  onReview: (a: Approval) => void
  onSplit: (ids: string[]) => void
  settleKey?: string
}) {
  const settled = useSettled(settleKey ?? approvalIds(cards))
  return (
    <>
      {cards.slice(0, max).map((card) => (
        <StackCard
          key={cardApproval(card).id}
          card={card}
          envName={envName}
          onReview={onReview}
          onSplit={onSplit}
          inert={!settled}
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

/** The IDs of every approval the cards show, in order, as one key. */
export function approvalIds(cards: ApprovalCard[]) {
  return cards
    .flatMap((c) => (c.kind === "single" ? [c.approval] : c.approvals))
    .map((a) => a.id)
    .join(" ")
}

/** One card of the stack: a single approval, or the network requests of one sandbox together. */
function StackCard({
  card,
  envName,
  onReview,
  onSplit,
  inert,
}: {
  card: ApprovalCard
  envName: (a: Approval) => string | undefined
  onReview: (a: Approval) => void
  onSplit: (ids: string[]) => void
  inert: boolean
}) {
  // A card that just arrived slides in with a ring that fades (lib/approval-arrivals.ts).
  const arriving = useJustArrived(cardApproval(card).id)
  const className = cn(arriving && "motion-safe:animate-card-arrival")
  if (card.kind === "single") {
    return (
      <ApprovalCard
        approval={card.approval}
        envName={envName(card.approval)}
        onReview={() => onReview(card.approval)}
        inert={inert}
        className={className}
      />
    )
  }
  return (
    <NetworkGroupCard
      approvals={card.approvals}
      envName={envName(card.approvals[0])}
      onReview={() => onSplit(card.approvals.map((a) => a.id))}
      inert={inert}
      className={className}
    />
  )
}

/** Links to the environment page that lists the next hidden approval's kind. */
function MoreLink({ approval, count }: { approval: Approval; count: number }) {
  return (
    <CardPageLink
      approval={approval}
      className="px-1 text-xs text-muted-foreground underline-offset-4 hover:text-foreground hover:underline"
    >
      +{count} more
    </CardPageLink>
  )
}

function ApprovalCard({
  approval,
  envName,
  onReview,
  inert,
  className,
}: {
  approval: Approval
  envName?: string
  onReview: () => void
  inert: boolean
  className?: string
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
      className={cn("gap-2 border-l-[3px]", ruleClass(kind.area), className)}
      data-testid="approval-card"
      inert={inert}
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
        ) : approval.kind === "lead.plan" && approval.leadPlan ? (
          <LeadPlanSummary approval={approval} />
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
    <div className="grid min-w-0 gap-2 text-xs">
      <PermissionInput
        input={p.input}
        clipped={p.inputClipped}
        kind={p.toolKind}
      />
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
        void refreshDecisions(queryClient)
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
