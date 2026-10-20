import { useState, type ReactNode } from "react"
import { Link, createFileRoute } from "@tanstack/react-router"
import { ScrollArea } from "@/components/ui/scroll-area"
import { PlusIcon, UserPlusIcon } from "@phosphor-icons/react"
import { Button } from "@/components/ui/button"
import {
  ApprovalCardList,
  useApprovalReview,
} from "@/components/approval-stack"
import { CreateSandboxDialog } from "@/components/create-sandbox-dialog"
import { Ident } from "@/components/ident"
import { SetupCard } from "@/components/setup-card"
import { PageHeader } from "@/components/page-header"
import { TeammateAvatar } from "@/components/teammate-avatar"
import { TeammateDialog } from "@/components/teammate-dialog"
import { usePendingApprovals } from "@/hooks/use-pending-approvals"
import { $api, type Approval } from "@/lib/api/client"
import type { Chat } from "@/lib/chats"
import { useClaimApprovals } from "@/lib/approval-claims"
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
    <>
      <PageHeader>
        <h1 className="text-sm font-medium">Home</h1>
      </PageHeader>
      <ScrollArea className="min-h-0 flex-1">
        <div className="mx-auto grid max-w-3xl content-start gap-7 px-4 py-6 md:px-8 md:py-8">
          <p className="text-muted-foreground">
            {summary(waiting.length, running.length)}
          </p>
          <SetupCard env={env} />
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
        </div>
      </ScrollArea>
    </>
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

/** A section's label, in the quiet uppercase the page uses for its three parts. */
function Label({ id, children }: { id: string; children: ReactNode }) {
  return (
    <h2
      id={id}
      className="font-mono text-xs font-medium tracking-wider text-muted-foreground uppercase"
    >
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
      <h2 className="font-medium">Start with a teammate</h2>
      <p className="text-muted-foreground">
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
        <p className="text-muted-foreground">No chat is running.</p>
      ) : (
        <ul className="overflow-hidden rounded-(--radius) border bg-card">
          {chats.map((c) => {
            const who = people.get(c.teammateId)
            const blocked = waiting.has(c.sandboxId)
            return (
              <li key={c.id} className="border-b last:border-b-0">
                <Link
                  to="/e/$env/sandboxes/$id/work"
                  params={{ env, id: c.sandboxId }}
                  search={{ chat: c.id }}
                  className="flex items-center gap-3 px-4 py-3 hover:bg-muted/40"
                >
                  <TeammateAvatar
                    name={who?.name ?? "?"}
                    avatar={who?.avatar}
                    className="size-6 rounded-full text-[0.625rem]"
                  />
                  <span className="grid min-w-0 flex-1">
                    <span className="truncate font-medium">
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
        <>
          <span className="text-muted-foreground">
            {c.heartbeat
              ? "Heartbeat"
              : c.taskId
                ? "Scheduled run"
                : "Started chat"}
          </span>{" "}
          {c.title ? `“${c.title}”` : "New chat"}
        </>
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
          to="/e/$env/network/activity"
          params={{ env }}
          className="ml-auto text-xs text-primary underline-offset-4 hover:underline"
        >
          All activity
        </Link>
      </div>
      {entries.length === 0 ? (
        <p className="text-muted-foreground">
          {approvals.isPending ? "Loading…" : "Nothing has happened yet."}
        </p>
      ) : (
        <ul className="grid gap-2 text-[0.8125rem]">
          {entries.map((e) => (
            <li key={e.key} className="flex min-w-0 gap-3">
              <span
                className="w-16 shrink-0 font-mono text-xs leading-5 text-muted-foreground"
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
