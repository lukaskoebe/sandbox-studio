import { useEffect, useRef } from "react"
import { useQueryClient } from "@tanstack/react-query"
import { toast } from "sonner"
import { Link, useParams } from "@tanstack/react-router"
import { CaretDownIcon } from "@phosphor-icons/react"
import {
  decideEach,
  decisionBody,
  refreshDecisions,
} from "@/components/approval-decision"
import {
  isListOpen,
  NeedsYouList,
  setListOpen,
  useApprovalReview,
  useListOpen,
} from "@/components/needs-you"
import { usePendingApprovals } from "@/hooks/use-pending-approvals"
import {
  shouldNotify,
  shouldNotifyConnections,
  useNotificationPermission,
} from "@/lib/notifications"
import { WaitingCount } from "@/components/waiting-count"
import { approvalHost } from "@/components/waiting-requests"
import { Button } from "@/components/ui/button"
import {
  Popover,
  PopoverContent,
  PopoverTrigger,
} from "@/components/ui/popover"
import { ScrollArea } from "@/components/ui/scroll-area"
import { type Approval } from "@/lib/api/client"
import { useClaimedApprovals } from "@/lib/approval-claims"
import { approvalKind, approvalSource } from "@/lib/approval-kinds"
import { cn } from "@/lib/utils"
import {
  waitingConnections,
  heldFor,
  isConnection,
} from "@/lib/connection-holds"
import { appAllowAllEvent, appNotifies, notifyApp } from "@/lib/desktop"
import { useArrivals, useRecordArrivals } from "@/lib/approval-arrivals"

/** The top bar's list, which its button opens and names. */
const listId = "approvals-list"

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
 * teammate's home do, it is only their count. A new one flashes the count once. Its rows are
 * the Needs you rows of every page (components/needs-you.tsx), which open in place.
 */
export function ApprovalsButton({ className }: { className?: string }) {
  const { env } = useParams({ strict: false })
  const { all, items } = usePendingItems()
  const open = useListOpen()
  const arrivals = useArrivals()
  const { permission, request } = useNotificationPermission()
  const { sheet, review } = useApprovalReview()
  // The list closes once nothing is left in it, and with the button, so it doesn't open
  // again by itself where the top bar mounts one anew.
  useEffect(() => {
    if (items.length === 0 && isListOpen()) setListOpen(false)
  }, [items.length])
  useEffect(() => () => setListOpen(false), [])
  if (all.length === 0) return sheet

  const n = items.length > 0 ? items.length : all.length
  const count = (
    <>
      <WaitingCount
        key={arrivals}
        count={n}
        className={cn(arrivals > 0 && "motion-safe:animate-count-arrival")}
      />{" "}
      {/* A phone's top bar keeps the room for where you are. */}
      <span className="max-sm:sr-only">
        {n === 1 ? "needs you" : "need you"}
      </span>
    </>
  )
  if (items.length === 0)
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
              <NeedsYouList
                approvals={items}
                who
                showEnv
                framed={false}
                onReview={review}
              />
            </ScrollArea>
            <footer className="flex flex-wrap items-center gap-x-3 gap-y-1 border-t px-3 py-2 text-caption text-muted-foreground">
              <span className="min-w-0 flex-1">
                Choose a request to see all its choices.
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
  const title = `${approvalSource(held[0]) ?? "A computer"} is waiting on a connection`
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
