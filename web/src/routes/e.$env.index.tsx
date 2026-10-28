import { useState, type ReactNode } from "react"
import { Link, createFileRoute } from "@tanstack/react-router"
import {
  BellIcon,
  BroadcastIcon,
  HardDriveIcon,
  PlusIcon,
  StackIcon,
  UserPlusIcon,
  XIcon,
} from "@phosphor-icons/react"
import { Button } from "@/components/ui/button"
import { Notice } from "@/components/notice"
import { lowDiskText } from "@/components/low-disk"
import {
  ApprovalCardList,
  useApprovalReview,
} from "@/components/approval-stack"
import { CreateSandboxDialog } from "@/components/create-sandbox-dialog"
import { Ident } from "@/components/ident"
import { SetupCard } from "@/components/setup-card"
import { ColumnPage } from "@/components/column-page"
import { TeammateAvatar } from "@/components/teammate-avatar"
import { TeammateDialog } from "@/components/teammate-dialog"
import { usePendingApprovals } from "@/hooks/use-pending-approvals"
import { $api, type Approval, type Sandbox } from "@/lib/api/client"
import type { Chat } from "@/lib/chats"
import { useClaimApprovals } from "@/lib/approval-claims"
import { useNotificationPermission } from "@/lib/notifications"
import { usePreviewSetting } from "@/lib/preview-setting"
import { approvalCards } from "@/lib/approval-groups"
import {
  approvalDecision,
  approvalKind,
  approvalSource,
} from "@/lib/approval-kinds"
import { cn, formatAge } from "@/lib/utils"

export const Route = createFileRoute("/e/$env/")({ component: Home })

/** Entries of the earlier list. */
const maxEarlier = 8

/**
 * What needs you, who is working, and what happened. Home answers its environment's approvals
 * in place, so the floating stack leaves them out here. Sandboxes, teammates and their chats are
 * in the sidebar, so Home doesn't list them again.
 */
function Home() {
  const { env } = Route.useParams()
  const sandboxes = $api.useQuery(
    "get",
    "/api/environments/{env}/sandboxes",
    { params: { path: { env } } },
    { refetchInterval: 3000 }
  )
  const teammates = $api.useQuery("get", "/api/environments/{env}/teammates", {
    params: { path: { env } },
  })
  const chats = $api.useQuery(
    "get",
    "/api/environments/{env}/chats",
    { params: { path: { env } } },
    { refetchInterval: 5000 }
  )
  const approvals = usePendingApprovals()

  const waiting = (approvals.data ?? []).filter((a) => a.environmentId === env)
  const running = (chats.data ?? []).filter((c) => c.state === "running")
  const empty = teammates.data?.length === 0 && sandboxes.data?.length === 0

  return (
    <ColumnPage
      title="Home"
      intro={summary(waiting.length, running.length)}
      narrow
    >
      <SetupCard env={env} />
      <NotificationsBanner />
      <PreviewsNotice env={env} />
      <BaseNotice env={env} sandboxes={sandboxes.data ?? []} />
      <DiskNotice env={env} sandboxes={sandboxes.data ?? []} />
      {empty ? (
        <GetStarted env={env} />
      ) : (
        <>
          <NeedsYou env={env} approvals={waiting} />
          <WorkingNow
            env={env}
            chats={running}
            waiting={new Set(waiting.map((a) => a.sandboxId))}
          />
        </>
      )}
      <Earlier env={env} />
    </ColumnPage>
  )
}

const bannerKey = "studio.notifications-banner"

/**
 * Asks once whether Studio may notify. It goes away when the browser has the answer, or for
 * good when it is dismissed; Settings → General still has the button.
 */
function NotificationsBanner() {
  const { permission, request } = useNotificationPermission()
  const [dismissed, setDismissed] = useState(() => {
    try {
      return localStorage.getItem(bannerKey) === "dismissed"
    } catch {
      return false
    }
  })
  if (permission !== "default" || dismissed) return null
  const dismiss = () => {
    setDismissed(true)
    try {
      localStorage.setItem(bannerKey, "dismissed")
    } catch {
      // The banner just shows again next time.
    }
  }
  return (
    <Notice
      aria-label="Notifications"
      icon={<BellIcon />}
      actions={
        <>
          <Button size="sm" variant="outline" onClick={request}>
            Enable notifications
          </Button>
          <Button
            size="icon-sm"
            variant="ghost"
            aria-label="Dismiss"
            title="Dismiss"
            onClick={dismiss}
          >
            <XIcon />
          </Button>
        </>
      }
    >
      Get a notification when an approval waits while Studio isn&apos;t in
      front.
    </Notice>
  )
}

/**
 * Says once that previews are off by default, and where to turn them on. Either
 * button records a choice, which ends the notice for every browser.
 */
function PreviewsNotice({ env }: { env: string }) {
  const previews = usePreviewSetting()
  if (!previews.notice) return null
  return (
    <Notice
      aria-label="Previews"
      icon={<BroadcastIcon />}
      actions={
        <>
          <Button
            size="sm"
            variant="outline"
            disabled={previews.pending}
            onClick={() => previews.setEnabled(true)}
          >
            Turn on previews
          </Button>
          <Button
            size="icon-sm"
            variant="ghost"
            aria-label="Keep previews off"
            title="Keep previews off"
            disabled={previews.pending}
            onClick={() => previews.setEnabled(false)}
          >
            <XIcon />
          </Button>
        </>
      }
    >
      Previews of sandbox ports are off by default since v0.6. Turn them on here
      or in{" "}
      <Link
        to="/e/$env/settings/general"
        params={{ env }}
        className="underline underline-offset-4"
      >
        Settings › General
      </Link>
      .
    </Notice>
  )
}

const baseNoticeKey = "studio.base-notice"

/**
 * Says, after an update that brought a new base image, which sandboxes still run the old one
 * (docs/sandboxes.md#base-images). Each name opens its computer, whose menu has Rebase.
 * Dismissing it hides it until the next release.
 */
function BaseNotice({ env, sandboxes }: { env: string; sandboxes: Sandbox[] }) {
  const updates = $api.useQuery("get", "/api/system/updates")
  const current = updates.data?.current ?? ""
  const [dismissed, setDismissed] = useState(() => {
    try {
      return localStorage.getItem(baseNoticeKey)
    } catch {
      return null
    }
  })
  const outdated = sandboxes.filter((sb) => sb.baseOutdated)
  if (outdated.length === 0 || !updates.data || dismissed === current)
    return null
  const dismiss = () => {
    setDismissed(current)
    try {
      localStorage.setItem(baseNoticeKey, current)
    } catch {
      // The notice just shows again next time.
    }
  }
  const release = /^v\d+\.\d+\.\d+(-[0-9A-Za-z.]+)?$/.test(current)
  return (
    <Notice
      aria-label="Base image"
      tone="waiting"
      icon={<StackIcon />}
      actions={
        <>
          {release && (
            <a
              href={`https://github.com/lukaskoebe/sandbox-studio/releases/tag/${current}`}
              target="_blank"
              rel="noreferrer"
              className="underline underline-offset-4"
            >
              What changed
            </a>
          )}
          <Button
            size="icon-sm"
            variant="ghost"
            aria-label="Dismiss"
            title="Dismiss until the next release"
            onClick={dismiss}
          >
            <XIcon />
          </Button>
        </>
      }
    >
      {release ? `${current} brings` : "Studio has"} a new base image.{" "}
      <span className="font-medium">
        {outdated.length} {outdated.length === 1 ? "sandbox" : "sandboxes"}
      </span>{" "}
      still {outdated.length === 1 ? "uses" : "use"} the old one:{" "}
      {outdated.map((sb, i) => (
        <span key={sb.id}>
          {i > 0 && ", "}
          <Link
            to="/e/$env/sandboxes/$id"
            params={{ env, id: sb.id }}
            className="font-mono underline underline-offset-4"
          >
            {sb.name}
          </Link>
        </span>
      ))}
      . Rebase them from their computer&apos;s menu.
    </Notice>
  )
}

/**
 * Names the sandboxes whose disks are nearly full: agents stop when the home disk fills.
 * It goes once they have room again.
 */
function DiskNotice({ env, sandboxes }: { env: string; sandboxes: Sandbox[] }) {
  const low = sandboxes.filter((sb) => sb.lowDisks?.length)
  if (low.length === 0) return null
  return (
    <Notice aria-label="Disk space" tone="waiting" icon={<HardDriveIcon />}>
      {low.map((sb) => (
        <span key={sb.id} className="block">
          <Link
            to="/e/$env/sandboxes/$id"
            params={{ env, id: sb.id }}
            className="font-mono underline underline-offset-4"
          >
            {sb.name}
          </Link>
          : {lowDiskText(sb.lowDisks ?? [])}.
        </span>
      ))}
      <span className="block">
        Agents stop when a home disk fills. Free some space in the
        sandbox&apos;s terminal, such as with{" "}
        <code className="font-mono">rm -rf ~/.cache ~/.npm</code>.
      </span>
    </Notice>
  )
}

function summary(waiting: number, working: number) {
  const needs =
    waiting === 0
      ? "Nothing needs you."
      : waiting === 1
        ? "1 thing needs you."
        : `${waiting} things need you.`
  const busy =
    working === 0
      ? "No one is working right now."
      : working === 1
        ? "1 chat is running."
        : `${working} chats are running.`
  return `${needs} ${busy}`
}

/** A section's label for the page's three parts, in the style's label type. */
function Label({ id, children }: { id: string; children: ReactNode }) {
  return (
    <h2 id={id} className="section-label text-muted-foreground">
      {children}
    </h2>
  )
}

/** A new environment: a teammate is the way in, a bare sandbox the other one. */
function GetStarted({ env }: { env: string }) {
  const [adding, setAdding] = useState(false)
  const [creating, setCreating] = useState(false)
  const providers = $api.useQuery("get", "/api/environments/{env}/providers", {
    params: { path: { env } },
  })
  return (
    <section
      aria-label="Get started"
      className="grid gap-3 rounded-(--radius) border bg-card p-5"
    >
      <h2 className="text-sm font-medium">Start with a teammate</h2>
      <p className="text-xs text-muted-foreground">
        A teammate is an agent with its own computer: a sandbox, a Linux VM with
        Docker and a persistent workspace. Chat with it, and it asks you here
        before it reaches the network or pushes code.
      </p>
      <div className="flex flex-wrap gap-2">
        <Button onClick={() => setAdding(true)}>
          <UserPlusIcon />
          Add a teammate
        </Button>
        <Button variant="outline" onClick={() => setCreating(true)}>
          <PlusIcon />
          New sandbox
        </Button>
      </div>
      <TeammateDialog
        env={env}
        providers={providers.data ?? []}
        open={adding}
        onOpenChange={setAdding}
      />
      <CreateSandboxDialog
        env={env}
        open={creating}
        onOpenChange={setCreating}
      />
    </section>
  )
}

/**
 * The environment's approvals, as the same cards the stack shows, answered here. Requests from
 * one sandbox that came together share a card, and "Review individually" splits them.
 */
function NeedsYou({ env, approvals }: { env: string; approvals: Approval[] }) {
  const [split, setSplit] = useState<ReadonlySet<string>>(() => new Set())
  const envs = $api.useQuery("get", "/api/environments")
  useClaimApprovals(approvals.map((a) => a.id))
  const { sheet, review } = useApprovalReview(approvals)
  const cards = approvalCards(approvals, split)
  const envName = () => envs.data?.find((e) => e.id === env)?.name
  return (
    <section
      aria-label="Approvals"
      className="grid min-w-0 content-start gap-2.5"
    >
      <Label id="needs-you">Needs you</Label>
      {cards.length === 0 ? (
        <p className="rounded-(--radius) border border-dashed p-5 text-muted-foreground">
          Nothing waits for you. Teammates keep working; this list fills when
          one of them asks.
        </p>
      ) : (
        <ApprovalCardList
          cards={cards}
          max={cards.length}
          envName={envName}
          onReview={review}
          onSplit={(ids) => setSplit((prev) => new Set([...prev, ...ids]))}
        />
      )}
      {sheet}
    </section>
  )
}

/** The running chats: who is on what, in which sandbox, and whether it waits on you. */
function WorkingNow({
  env,
  chats,
  waiting,
}: {
  env: string
  chats: Chat[]
  waiting: ReadonlySet<string | undefined>
}) {
  const teammates = $api.useQuery("get", "/api/environments/{env}/teammates", {
    params: { path: { env } },
  })
  const sandboxes = $api.useQuery("get", "/api/environments/{env}/sandboxes", {
    params: { path: { env } },
  })
  const people = new Map((teammates.data ?? []).map((t) => [t.id, t]))
  const names = new Map((sandboxes.data ?? []).map((s) => [s.id, s.name]))
  return (
    <section
      aria-labelledby="working-now"
      className="grid min-w-0 content-start gap-2.5"
    >
      <Label id="working-now">Working now</Label>
      {chats.length === 0 ? (
        <p className="text-xs text-muted-foreground">No chat is running.</p>
      ) : (
        <ul className="overflow-hidden rounded-(--radius) border bg-card">
          {chats.map((c) => {
            const who = people.get(c.teammateId)
            const blocked = waiting.has(c.sandboxId)
            return (
              <li key={c.id} className="border-b last:border-b-0">
                <Link
                  to="/e/$env/teammates/$id"
                  params={{ env, id: c.teammateId }}
                  search={{ thread: c.id }}
                  className="flex items-center gap-3 px-4 py-3 hover:bg-muted/40"
                >
                  <TeammateAvatar
                    name={who?.name ?? "?"}
                    avatar={who?.avatar}
                    className="size-6 rounded-full text-meta"
                  />
                  <span className="grid min-w-0 flex-1">
                    <span className="truncate text-title font-medium">
                      {c.title || "New chat"}
                    </span>
                    <span className="flex min-w-0 items-center gap-1 text-xs text-muted-foreground">
                      {who && <span className="truncate">{who.name}</span>}
                      {who && <span aria-hidden>·</span>}
                      <Ident>{names.get(c.sandboxId) ?? c.sandboxId}</Ident>
                      {blocked && <span aria-hidden>·</span>}
                      {blocked && <span>waiting on you</span>}
                    </span>
                  </span>
                  <span
                    className={cn(
                      "shrink-0 text-xs",
                      blocked
                        ? "text-(--st-waiting-fg)"
                        : "text-muted-foreground"
                    )}
                  >
                    {blocked ? "waiting" : formatAge(c.updatedAt)}
                  </span>
                </Link>
              </li>
            )
          })}
        </ul>
      )}
    </section>
  )
}

/**
 * A decided git review or memory conflict links to its own page; other approvals are plain
 * text.
 */
function ApprovalPageLink({
  approval: a,
  children,
}: {
  approval: Approval
  children: ReactNode
}) {
  const className = "underline-offset-4 hover:underline"
  if (a.kind === "git.push" || a.kind === "git.pr")
    return (
      <Link
        to="/e/$env/git/reviews/$id"
        params={{ env: a.environmentId, id: a.id }}
        className={className}
      >
        {children}
      </Link>
    )
  const conflict = a.memoryConflict?.conflictId
  if (conflict)
    return (
      <Link
        to="/e/$env/memory/conflicts/$id"
        params={{ env: a.environmentId, id: conflict }}
        className={className}
      >
        {children}
      </Link>
    )
  return <>{children}</>
}

type Entry = { key: string; at: string; text: ReactNode; who?: string }

/** The time of day for today, the day and time before. */
function when(iso: string, now = new Date()) {
  const d = new Date(iso)
  const time = d.toLocaleTimeString(undefined, {
    hour: "2-digit",
    minute: "2-digit",
  })
  if (d.toDateString() === now.toDateString()) return time
  return `${d.toLocaleDateString(undefined, { weekday: "short" })} ${time}`
}

/**
 * What happened lately: decided approvals (allowed, denied, by whom or which rule) and chats
 * started, newest first. Git reviews and memory conflicts link to their own pages.
 */
function Earlier({ env }: { env: string }) {
  const approvals = $api.useQuery("get", "/api/environments/{env}/approvals", {
    params: { path: { env }, query: { status: "all" } },
  })
  const chats = $api.useQuery("get", "/api/environments/{env}/chats", {
    params: { path: { env } },
  })
  const teammates = $api.useQuery("get", "/api/environments/{env}/teammates", {
    params: { path: { env } },
  })
  const names = new Map((teammates.data ?? []).map((t) => [t.id, t.name]))
  const entries: Entry[] = [
    ...(approvals.data ?? [])
      .filter((a) => a.status !== "pending")
      .map((a): Entry => {
        const kind = approvalKind(a)
        const what = kind.summary(a)
        return {
          key: a.id,
          at: a.updatedAt,
          text: (
            <ApprovalPageLink approval={a}>
              <span className="text-muted-foreground">
                {approvalDecision(a)}
              </span>{" "}
              {kind.mono ? <Ident>{what}</Ident> : what}
            </ApprovalPageLink>
          ),
          who: approvalSource(a),
        }
      }),
    ...(chats.data ?? []).map((c): Entry => ({
      key: c.id,
      at: c.createdAt,
      text: (
        <Link
          to="/e/$env/teammates/$id"
          params={{ env, id: c.teammateId }}
          search={{ thread: c.id }}
          className="underline-offset-4 hover:underline"
        >
          <span className="text-muted-foreground">
            {c.heartbeat
              ? "Heartbeat"
              : c.taskId
                ? "Scheduled run"
                : "Started chat"}
          </span>{" "}
          {c.title ? `“${c.title}”` : "New chat"}
        </Link>
      ),
      who: c.teammateId ? names.get(c.teammateId) : undefined,
    })),
  ]
    .sort((a, b) => Date.parse(b.at) - Date.parse(a.at))
    .slice(0, maxEarlier)

  return (
    <section
      aria-labelledby="earlier"
      className="grid min-w-0 content-start gap-2.5"
    >
      <div className="flex items-baseline gap-2">
        <Label id="earlier">Earlier</Label>
        <Link
          to="/e/$env/chats"
          params={{ env }}
          className="ml-auto text-xs text-primary underline-offset-4 hover:underline"
        >
          All chats
        </Link>
        <Link
          to="/e/$env/settings/activity"
          params={{ env }}
          className="text-xs text-primary underline-offset-4 hover:underline"
        >
          All activity
        </Link>
      </div>
      {entries.length === 0 ? (
        <p className="text-xs text-muted-foreground">
          {approvals.isPending ? "Loading…" : "Nothing has happened yet."}
        </p>
      ) : (
        <ul className="grid gap-2 text-xs">
          {entries.map((e) => (
            <li key={e.key} className="flex min-w-0 gap-3">
              <span
                className="w-24 shrink-0 font-mono text-xs leading-5 whitespace-nowrap text-muted-foreground"
                title={new Date(e.at).toLocaleString()}
              >
                {when(e.at)}
              </span>
              <span className="min-w-0 flex-1 truncate">{e.text}</span>
              {e.who && (
                <span className="max-w-32 shrink-0 truncate text-xs leading-5 text-muted-foreground max-sm:hidden">
                  {e.who}
                </span>
              )}
            </li>
          ))}
        </ul>
      )}
    </section>
  )
}
