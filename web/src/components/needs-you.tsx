import {
  useRef,
  useState,
  useSyncExternalStore,
  type MouseEvent,
  type ReactNode,
} from "react"
import { Link } from "@tanstack/react-router"
import { useQueryClient } from "@tanstack/react-query"
import { toast } from "sonner"
import { CaretDownIcon } from "@phosphor-icons/react"
import {
  ApprovalDecision,
  decideEach,
  initialDecision,
  initialDecisionBody,
  isBrowserRequest,
  refreshDecisions,
  ruleSummary,
  useDecideApproval,
  type BrowserReach,
  type DecisionScope,
  type Lifetime,
} from "@/components/approval-decision"
import { NetworkGroupBody } from "@/components/approval-group"
import {
  BrowserActionDecision,
  BrowserControlDecision,
  BrowserCredentialDecision,
  BrowserShowDecision,
} from "@/components/browser-approval"
import {
  PermissionDecision,
  PermissionInput,
} from "@/components/chat/permission-card"
import { CredentialIssueDecision } from "@/components/credential-issue"
import { ApprovalTip } from "@/components/feature-tip"
import { GitCredentialDecision } from "@/components/git-credential"
import {
  GitApprovalSummary,
  isGitApproval,
  useOpenGitReview,
} from "@/components/git-review"
import { LeadPlanSummary } from "@/components/lead/plan-approval"
import {
  ConflictSheet,
  MemoryConflictDecision,
} from "@/components/memory-conflict"
import { MemoryShareDecision } from "@/components/memory-sessions"
import { SecretRejectedDecision } from "@/components/secret-rejected"
import { TeammateAvatar } from "@/components/teammate-avatar"
import { Button } from "@/components/ui/button"
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuGroup,
  DropdownMenuItem,
  DropdownMenuLabel,
  DropdownMenuSeparator,
  DropdownMenuTrigger,
} from "@/components/ui/dropdown-menu"
import { Spinner } from "@/components/ui/spinner"
import { approvalHost } from "@/components/waiting-requests"
import { $api, type Approval } from "@/lib/api/client"
import { useJustArrived } from "@/lib/approval-arrivals"
import {
  approvalCards,
  approvalIds,
  cardApproval,
  groupSite,
  type ApprovalCard,
} from "@/lib/approval-groups"
import {
  approvalKind,
  approvalSource,
  approvalTeammateId,
  isNetworkApproval,
  type ApprovalKind,
} from "@/lib/approval-kinds"
import { heldFor, isConnection, useHoldClock } from "@/lib/connection-holds"
import { useSettled } from "@/lib/settled"
import { cn, formatAge } from "@/lib/utils"
import { left as leftTime } from "@/lib/time"

// Whether the top bar's list is open: reviewing an approval from it closes it.
let listOpen = false
const listListeners = new Set<() => void>()
const subscribeList = (l: () => void) => {
  listListeners.add(l)
  return () => void listListeners.delete(l)
}
export const isListOpen = () => listOpen
export function setListOpen(open: boolean) {
  listOpen = open
  listListeners.forEach((l) => l())
}
export const useListOpen = () =>
  useSyncExternalStore(subscribeList, () => listOpen)

/**
 * Reviewing an approval: a git push opens in its computer's Changes tab, a memory conflict in a
 * sheet, which is rendered wherever its caller lists the requests.
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
 * What waits for you, one row per request in one box (docs/design/ui-style.md): the top bar's
 * list, a teammate's home, Home and the Network page show the same rows. A row says its kind,
 * where it comes from and what it asks, and has the answer that needs no more choices; choosing
 * it anywhere else opens it in place, with every choice its card has. Requests from one computer
 * that came together share a row until Each splits them. For half a second after the rows change
 * they ignore clicks, so that one meant for a row that just left, or moved, doesn't answer the one
 * that took its place.
 */
export function NeedsYouList({
  approvals,
  who = false,
  showEnv = false,
  tips = false,
  framed = true,
  onReview,
  label,
}: {
  approvals: Approval[]
  /** Name the teammate a request is for, in a list of every teammate's. */
  who?: boolean
  /** Name the environment, in the top bar's list of every environment. */
  showEnv?: boolean
  /** Show the tips of the first network request and the first push beside their rows. */
  tips?: boolean
  /** A box of its own; without, the rows only, as in the top bar's popover. */
  framed?: boolean
  /**
   * Reviews a git ask or a memory conflict, for a caller that keeps the conflict's sheet
   * (useApprovalReview) outside a popover; without, the list reviews them itself.
   */
  onReview?: (a: Approval) => void
  /** The list's accessible name. */
  label?: string
}) {
  const [split, setSplit] = useState<ReadonlySet<string>>(() => new Set())
  const [open, setOpen] = useState<ReadonlySet<string>>(() => new Set())
  const own = useApprovalReview()
  const review = onReview ?? own.review
  const envs = $api.useQuery("get", "/api/environments")
  const cards = approvalCards(approvals, split)
  const settled = useSettled(approvalIds(cards))
  const envName = (a: Approval) =>
    envs.data?.find((e) => e.id === a.environmentId)?.name
  const toggle = (id: string) =>
    setOpen((prev) => {
      const next = new Set(prev)
      if (!next.delete(id)) next.add(id)
      return next
    })
  return (
    <>
      <ul
        aria-label={label}
        className={cn(
          "grid min-w-0 divide-y",
          framed && "overflow-hidden rounded-(--radius) border bg-card"
        )}
      >
        {cards.map((card) => {
          const id = cardApproval(card).id
          return (
            <NeedsYouRow
              key={id}
              card={card}
              open={open.has(id)}
              onToggle={() => toggle(id)}
              onReview={review}
              onSplit={(ids) => setSplit((prev) => new Set([...prev, ...ids]))}
              inert={!settled}
              who={who}
              envName={envName}
              // The name tells environments apart, so one alone goes unnamed.
              showEnv={showEnv && (envs.data?.length ?? 0) > 1}
              tips={tips}
            />
          )
        })}
      </ul>
      {!onReview && own.sheet}
    </>
  )
}

/** Clicks on these answer or lead somewhere; anywhere else on a row opens it. */
const interactive = "button, a, input, textarea, label, [role=menuitem]"

function NeedsYouRow({
  card,
  open,
  onToggle,
  onReview,
  onSplit,
  inert,
  who,
  envName,
  showEnv,
  tips,
}: {
  card: ApprovalCard
  open: boolean
  onToggle: () => void
  onReview: (a: Approval) => void
  onSplit: (ids: string[]) => void
  inert: boolean
  who: boolean
  envName: (a: Approval) => string | undefined
  showEnv: boolean
  tips: boolean
}) {
  const a = cardApproval(card)
  const kind = approvalKind(a)
  const group = card.kind === "group" ? card.approvals : undefined
  const row = useRef<HTMLLIElement>(null)
  // A row that just arrived slides in with a ring that fades (lib/approval-arrivals.ts).
  const arriving = useJustArrived(a.id)
  const owner = useApprovalOwner(a, who)
  // A connection the gateway holds says how long it waits; once it gave up, an answer is for
  // the next attempt.
  const connection = !group && (isConnection(a) || !!a.network?.write)
  const now = useHoldClock(connection ? [a] : [])
  const left = connection ? heldFor(a, now) : undefined
  const site = group && groupSite(group)
  const permission = a.chatPermission
  const source =
    permission && !who
      ? `in “${permission.chatTitle || "New chat"}”`
      : approvalSource(a)
  const from = [
    site,
    source !== owner?.name ? source : undefined,
    showEnv ? envName(a) : undefined,
    formatAge(a.createdAt),
    a.attempts > 1 ? `${a.attempts} attempts` : undefined,
  ]
    .filter(Boolean)
    .join(" · ")
  // Opened, a group's hosts are in its body, and its title says whose they are.
  const title = !group
    ? kind.summary(a)
    : open
      ? `${site ?? approvalSource(a) ?? "A computer"} wants ${group.length} hosts`
      : group.map(approvalHost).join(", ")
  const mono = group ? !open : !!kind.mono
  const command = permission?.input?.split("\n")[0]
  const tip = !tips
    ? undefined
    : isNetworkApproval(a)
      ? "network"
      : a.kind === "git.push" || a.kind === "git.pr"
        ? "push"
        : undefined
  const review = () => onReview(a)
  return (
    <li
      ref={row}
      data-testid={group ? "approval-group" : "approval-card"}
      data-open={open || undefined}
      inert={inert}
      onClick={(e: MouseEvent) => {
        if (open || (e.target as Element).closest(interactive)) return
        if (window.getSelection()?.toString()) return
        onToggle()
      }}
      className={cn(
        "grid min-w-0 grid-cols-[minmax(0,1fr)_auto] items-center gap-x-3 gap-y-1 px-3 py-2.5 max-sm:grid-cols-1",
        open ? "bg-muted/40" : "cursor-pointer hover:bg-muted/30",
        arriving && "motion-safe:animate-card-arrival"
      )}
    >
      {tip && <ApprovalTip env={a.environmentId} feature={tip} anchor={row} />}
      <div className="col-span-full flex min-w-0 items-center gap-1.5 text-caption text-muted-foreground">
        <KindLabel kind={kind} count={group?.length} />
        {owner && (
          <span className="inline-flex shrink-0 items-center gap-1 text-foreground">
            <TeammateAvatar
              name={owner.name}
              avatar={owner.avatar}
              className="size-4"
            />
            {owner.name}
          </span>
        )}
        <span className="min-w-0 truncate">{from}</span>
        {/* Opened, the card's own hold status says more. */}
        {connection && !open && (
          <span
            data-testid="hold-status"
            className="ml-auto shrink-0 tabular-nums"
          >
            {left === undefined ? "No longer held" : `Held, ${leftTime(left)}`}
          </span>
        )}
      </div>
      <button
        type="button"
        data-slot="approval-open"
        aria-expanded={open}
        onClick={onToggle}
        className={cn(
          "grid min-w-0 justify-items-start gap-1 rounded-sm text-left text-xs font-medium outline-none focus-visible:ring-2 focus-visible:ring-ring",
          open && "col-span-full"
        )}
      >
        <span
          className={cn(
            "line-clamp-2 max-w-full [overflow-wrap:anywhere]",
            mono && "font-mono"
          )}
        >
          {title}
        </span>
        {command && command !== title && !open && (
          <span className="max-w-full truncate font-mono font-normal text-muted-foreground">
            {command}
          </span>
        )}
      </button>
      {!open && (
        <div className="flex shrink-0 items-center gap-1.5 max-sm:justify-self-end">
          <RowActions
            card={card}
            envName={envName(a)}
            held={left !== undefined}
            onReview={review}
            onSplit={onSplit}
            onOpen={onToggle}
          />
        </div>
      )}
      {open && (
        <div className="col-span-full grid gap-2 pt-1">
          {group ? (
            <NetworkGroupBody
              approvals={group}
              envName={envName(a)}
              onReview={() => onSplit(group.map((g) => g.id))}
            />
          ) : (
            <ApprovalBody approval={a} onReview={review} />
          )}
          <button
            type="button"
            onClick={onToggle}
            className="justify-self-end px-1 text-xs text-muted-foreground underline-offset-4 hover:text-foreground hover:underline"
          >
            Show less
          </button>
        </div>
      )}
    </li>
  )
}

/**
 * A kind's name in the label type, with its icon beside it in the same grey and the size of the
 * letters: "NETWORK", or "NETWORK · 3" for a computer's requests together.
 */
export function KindLabel({
  kind,
  count,
}: {
  kind: ApprovalKind
  count?: number
}) {
  return (
    <span className="inline-flex shrink-0 items-center gap-1 section-label text-muted-foreground">
      <kind.icon weight="bold" className="size-3 shrink-0" aria-hidden />
      {count ? `${kind.title} · ${count}` : kind.title}
    </span>
  )
}

/** The teammate a request is for: the one owning its computer, or whose browser asks. */
function useApprovalOwner(a: Approval, enabled: boolean) {
  const env = a.environmentId
  const teammates = $api.useQuery(
    "get",
    "/api/environments/{env}/teammates",
    { params: { path: { env } } },
    { enabled }
  )
  const known = a.teammate?.id ?? approvalTeammateId(a)
  const sandboxes = $api.useQuery(
    "get",
    "/api/environments/{env}/sandboxes",
    { params: { path: { env } } },
    { enabled: enabled && !known && !!a.sandboxId }
  )
  if (!enabled) return undefined
  const id =
    known ?? sandboxes.data?.find((s) => s.id === a.sandboxId)?.teammateId
  return id ? teammates.data?.find((t) => t.id === id) : undefined
}

/** The answer of a row that needs no more choices, by kind. */
function RowActions({
  card,
  envName,
  held,
  onReview,
  onSplit,
  onOpen,
}: {
  card: ApprovalCard
  envName?: string
  held: boolean
  onReview: () => void
  onSplit: (ids: string[]) => void
  onOpen: () => void
}) {
  if (card.kind === "group")
    return (
      <GroupActions
        approvals={card.approvals}
        envName={envName}
        onSplit={() => onSplit(card.approvals.map((a) => a.id))}
      />
    )
  const a = card.approval
  if (a.kind === "chat.permission" && a.chatPermission)
    return <PermissionActions approval={a} />
  if (isNetworkApproval(a))
    return (
      <NetworkActions
        approval={a}
        envName={envName}
        held={held}
        onMore={onOpen}
      />
    )
  if (isGitApproval(a) || a.kind === "memory.conflict")
    return (
      <Button size="sm" variant="outline" onClick={onReview}>
        {a.kind === "memory.conflict" ? "Resolve" : "Review"}
      </Button>
    )
  if (a.kind === "memory.share")
    return (
      <TwoWords
        approval={a}
        allow={a.memoryShare?.page ? "Write page" : "Share"}
        scope="environment"
      />
    )
  if (a.kind === "browser.credential")
    return <TwoWords approval={a} allow="Fill" scope="teammate" />
  if (a.kind === "browser.action")
    return (
      <TwoWords
        approval={a}
        allow="Allow"
        scope="teammate"
        titles={["Deny this action once", "Allow this action once"]}
      />
    )
  return (
    <Button size="sm" variant="outline" onClick={onOpen}>
      Open
    </Button>
  )
}

/** Deny and a kind's own word for allowing, each once, as its card's buttons do untouched. */
function TwoWords({
  approval,
  allow,
  scope,
  titles,
}: {
  approval: Approval
  allow: string
  scope: DecisionScope
  titles?: [string, string]
}) {
  const decide = useDecideApproval()
  const busy = (action: string) =>
    decide.isPending && decide.variables.body.action === action
  const send = (action: "allow" | "deny") =>
    decide.mutate({
      params: { path: { env: approval.environmentId, id: approval.id } },
      body: { action, scope, remember: false },
    })
  return (
    <>
      <Button
        size="sm"
        variant="outline"
        title={titles?.[0]}
        disabled={decide.isPending}
        onClick={() => send("deny")}
      >
        {busy("deny") && <Spinner />}
        Deny
      </Button>
      <Button
        size="sm"
        title={titles?.[1]}
        disabled={decide.isPending}
        onClick={() => send("allow")}
      >
        {busy("allow") && <Spinner />}
        {allow}
      </Button>
    </>
  )
}

/** Denies or allows an agent's permission request once, as its card's buttons do untouched. */
function PermissionActions({ approval }: { approval: Approval }) {
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
        title="Deny this request once"
        disabled={decide.isPending || !has("reject_once")}
        onClick={() => send("deny")}
      >
        {busy("deny") && <Spinner />}
        Deny
      </Button>
      <Button
        size="sm"
        title="Allow this request once"
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
 * Denies or allows a network request as its card starts (initialDecision), whose tooltip says
 * for whom and how long, and the caret beside Allow picks another length or scope, which answers
 * at once. More opens the row with the card's other choices: the path, the port, a note.
 */
function NetworkActions({
  approval,
  envName,
  held,
  onMore,
}: {
  approval: Approval
  envName?: string
  held: boolean
  onMore: () => void
}) {
  const decide = useDecideApproval()
  const d = initialDecision(approval)
  const browser = isBrowserRequest(approval)
  const owner = approval.teammate?.name
  const sandboxName = approval.network?.sandboxName
  const facts = { browser, sandboxName, owner, envName }
  const rule = ruleSummary({ ...facts, ...d })
  const busy = (action: string) =>
    decide.isPending && decide.variables.body.action === action
  const send = (
    action: "allow" | "deny",
    over?: { lifetime?: Lifetime; scope?: DecisionScope; reach?: BrowserReach }
  ) =>
    decide.mutate({
      params: { path: { env: approval.environmentId, id: approval.id } },
      body: initialDecisionBody(approval, action, over),
    })
  const scopes = browser
    ? (["browser", "browsers", "everywhere"] as const)
        .filter((r) => r !== d.reach)
        .map((reach) => ({
          key: reach,
          label:
            reach === "browser"
              ? `Only ${owner ? `${owner}'s browser` : "this browser"}`
              : reach === "browsers"
                ? `Every browser in ${envName ?? "this environment"}`
                : `Browsers and computers in ${envName ?? "this environment"}`,
          over: { reach },
        }))
    : (["sandbox", "teammate", "environment"] as const)
        .filter(
          (s) =>
            s !== d.scope &&
            (s !== "teammate" || !!owner) &&
            (s !== "sandbox" || !!sandboxName)
        )
        .map((scope) => ({
          key: scope,
          label:
            scope === "sandbox"
              ? `Only ${sandboxName}`
              : scope === "teammate"
                ? `${owner}'s computers`
                : `Every computer in ${envName ?? "this environment"}`,
          over: { scope },
        }))
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
      <span className="inline-flex">
        <Button
          size="sm"
          title={rule}
          disabled={decide.isPending}
          onClick={() => send("allow")}
          className="rounded-r-none"
        >
          {busy("allow") && <Spinner />}
          Allow
        </Button>
        <DropdownMenu>
          <DropdownMenuTrigger
            render={
              <Button
                size="sm"
                aria-label="Other ways to allow"
                disabled={decide.isPending}
                className="rounded-l-none border-l border-l-primary-foreground/25 px-1.5"
              />
            }
          >
            <CaretDownIcon aria-hidden className="size-3" />
          </DropdownMenuTrigger>
          <DropdownMenuContent align="end" className="w-72">
            <DropdownMenuGroup>
              <DropdownMenuLabel className="truncate">
                Allow{" "}
                <span className="font-mono">{approvalHost(approval)}</span>
              </DropdownMenuLabel>
              <DropdownMenuItem
                onClick={() => send("allow", { lifetime: "hour" })}
              >
                For 1 hour
              </DropdownMenuItem>
              {browser && (
                <DropdownMenuItem
                  onClick={() => send("allow", { lifetime: "session" })}
                >
                  For this browser session
                </DropdownMenuItem>
              )}
              {held && (
                <DropdownMenuItem
                  onClick={() => send("allow", { lifetime: "once" })}
                >
                  Once, with no rule
                </DropdownMenuItem>
              )}
            </DropdownMenuGroup>
            {scopes.length > 0 && (
              <>
                <DropdownMenuSeparator />
                <DropdownMenuGroup>
                  {scopes.map((s) => (
                    <DropdownMenuItem
                      key={s.key}
                      onClick={() => send("allow", s.over)}
                    >
                      {s.label}
                    </DropdownMenuItem>
                  ))}
                </DropdownMenuGroup>
              </>
            )}
            <DropdownMenuSeparator />
            <DropdownMenuItem onClick={onMore}>
              More: the path, a note…
            </DropdownMenuItem>
          </DropdownMenuContent>
        </DropdownMenu>
      </span>
    </>
  )
}

/**
 * A computer's requests together: Each splits them into a row per host; Allow all gives each
 * host the rule its own row's Allow would, and the caret picks another length, or denies them all.
 */
function GroupActions({
  approvals,
  envName,
  onSplit,
}: {
  approvals: Approval[]
  envName?: string
  onSplit: () => void
}) {
  const queryClient = useQueryClient()
  const [working, setWorking] = useState<"allow" | "deny">()
  const first = approvals[0]
  const rule = ruleSummary({
    ...initialDecision(first),
    browser: isBrowserRequest(first),
    sandboxName: first.network?.sandboxName,
    owner: first.teammate?.name,
    envName,
    many: true,
  })
  async function decideAll(
    action: "allow" | "deny",
    over?: { lifetime?: Lifetime }
  ) {
    setWorking(action)
    const failure = await decideEach(approvals, (a) =>
      initialDecisionBody(a, action, over)
    )
    if (failure)
      toast.error(`Could not ${action} every host`, { description: failure })
    await refreshDecisions(queryClient)
    setWorking(undefined)
  }
  return (
    <>
      <Button size="sm" variant="ghost" onClick={onSplit}>
        Each
      </Button>
      <span className="inline-flex">
        <Button
          size="sm"
          title={rule}
          disabled={working !== undefined}
          onClick={() => void decideAll("allow")}
          className="rounded-r-none"
        >
          {working === "allow" && <Spinner />}
          Allow all
        </Button>
        <DropdownMenu>
          <DropdownMenuTrigger
            render={
              <Button
                size="sm"
                aria-label="Other ways to answer them all"
                disabled={working !== undefined}
                className="rounded-l-none border-l border-l-primary-foreground/25 px-1.5"
              />
            }
          >
            <CaretDownIcon aria-hidden className="size-3" />
          </DropdownMenuTrigger>
          <DropdownMenuContent align="end" className="w-52">
            <DropdownMenuItem
              onClick={() => void decideAll("allow", { lifetime: "hour" })}
            >
              Allow all for 1 hour
            </DropdownMenuItem>
            <DropdownMenuSeparator />
            <DropdownMenuItem onClick={() => void decideAll("deny")}>
              {working === "deny" && <Spinner />}
              Deny all
            </DropdownMenuItem>
          </DropdownMenuContent>
        </DropdownMenu>
      </span>
    </>
  )
}

/** Everything an approval's card has below its title: what it asks in full, and every choice. */
export function ApprovalBody({
  approval,
  onReview,
}: {
  approval: Approval
  onReview: () => void
}) {
  if (isGitApproval(approval))
    return <GitApprovalSummary approval={approval} onReview={onReview} />
  switch (approval.kind) {
    case "memory.share":
      return <MemoryShareDecision approval={approval} />
    case "memory.conflict":
      return <MemoryConflictDecision approval={approval} onResolve={onReview} />
    case "browser.action":
      return <BrowserActionDecision approval={approval} />
    case "browser.credential":
      return <BrowserCredentialDecision approval={approval} />
    case "browser.control":
      return <BrowserControlDecision approval={approval} />
    case "browser.show":
      return <BrowserShowDecision approval={approval} />
    case "git.credential":
      return <GitCredentialDecision approval={approval} />
    case "secret.rejected":
      return <SecretRejectedDecision approval={approval} />
    case "credential.issue":
      return <CredentialIssueDecision approval={approval} />
  }
  if (approval.kind === "chat.permission" && approval.chatPermission)
    return <ChatPermissionSummary approval={approval} />
  if (approval.kind === "lead.plan" && approval.leadPlan)
    return <LeadPlanSummary approval={approval} />
  if (isNetworkApproval(approval))
    return <ApprovalDecision approval={approval} compact />
  return (
    <p className="text-xs text-muted-foreground" role="status">
      This approval type is not supported by this version of Studio.
    </p>
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
        onClick={() => setListOpen(false)}
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
