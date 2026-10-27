import { useEffect, useState } from "react"
import {
  ArrowLineRightIcon,
  MonitorIcon,
  PlusIcon,
} from "@phosphor-icons/react"
import { ComputerScreen, type ScreenChange } from "@/components/computer/screen"
import { CreateSandboxDialog } from "@/components/create-sandbox-dialog"
import { useWideScreen } from "@/components/side-panel"
import { TeammateAvatar } from "@/components/teammate-avatar"
import { TeammateHome, teammateApprovals } from "@/components/teammate/home"
import { ProfileCard, ProfileSheet } from "@/components/teammate/profile"
import { TeammateThread } from "@/components/teammate/thread"
import { Button } from "@/components/ui/button"
import { usePendingApprovals } from "@/hooks/use-pending-approvals"
import { $api, type Teammate } from "@/lib/api/client"
import { useChat, type Chat } from "@/lib/chats"
import type { ProfileView, TeammateSearch } from "@/lib/computer"
import { cn } from "@/lib/utils"

export type SearchChange = Partial<Record<keyof TeammateSearch, unknown>>

/**
 * A teammate and its computer side by side (docs/design/teammate-computer.md): the conversation
 * on the left, its threads or the one open, and the screen of the computer it works on to the
 * right. The screen can fold, or take the page with the conversation a strip beside it. The
 * profile opens over the conversation. Esc closes what is on top, then the thread.
 */
export function TeammatePage({
  env,
  teammate,
  search,
  onSearch,
}: {
  env: string
  teammate: Teammate
  search: TeammateSearch
  /** Changes the URL's search; a thread opened or left is a new history entry. */
  /** With push the change is a step in the browser's history; "quiet" leaves out the transition. */
  onSearch: (change: SearchChange, push?: boolean | "quiet") => void
}) {
  const id = teammate.id
  const sandboxes = $api.useQuery(
    "get",
    "/api/environments/{env}/sandboxes",
    { params: { path: { env } } },
    { refetchInterval: 5000 }
  )
  const chats = $api.useQuery(
    "get",
    "/api/environments/{env}/chats",
    { params: { path: { env } } },
    { refetchInterval: 10000 }
  )
  const owned = (sandboxes.data ?? []).filter((s) => s.teammateId === id)
  const mine = (chats.data ?? []).filter((c) => c.teammateId === id)
  const log = useChat(env, search.thread)
  const thread = search.thread
    ? (log.chat ?? mine.find((c) => c.id === search.thread))
    : undefined
  const latest = [...mine].sort((a, b) =>
    b.updatedAt.localeCompare(a.updatedAt)
  )
  // The screen shows the computer asked for, else the thread's, else where the work happens.
  const computer =
    owned.find((s) => s.id === search.computer) ??
    owned.find((s) => s.id === thread?.sandboxId) ??
    owned.find(
      (s) => s.id === latest.find((c) => c.state === "running")?.sandboxId
    ) ??
    owned.find((s) => s.id === latest.at(0)?.sandboxId) ??
    owned.at(0)

  const pending = usePendingApprovals()
  const waiting = teammateApprovals(
    pending.data ?? [],
    env,
    id,
    new Set(owned.map((s) => s.id))
  )

  const [turnId, setTurnId] = useState<string>()
  const wide = useWideScreen()
  // On narrow screens the screen is a strip that opens over the conversation. A link that
  // names a tab, such as a thread's Go to the terminal, opens it.
  const [overlay, setOverlay] = useState(!!search.tab)
  const [shownTab, setShownTab] = useState(search.tab)
  if (shownTab !== search.tab) {
    setShownTab(search.tab)
    if (!wide && search.tab) setOverlay(true)
  }
  const folded = wide ? !!search.fold : !overlay
  // On narrow screens the open screen covers the conversation anyway.
  const full = wide && !folded && !!search.full

  const screen = (change: ScreenChange) => {
    const { fold, full: enlarge, push, ...rest } = change
    // Folding and enlarging undo each other.
    if (fold !== undefined || enlarge !== undefined) {
      if (wide)
        onSearch({ fold: fold || undefined, full: enlarge || undefined })
      else if (fold !== undefined) setOverlay(!fold)
    }
    if (Object.keys(rest).length === 0) return
    if (!wide && rest.tab) setOverlay(true)
    onSearch(
      {
        // Another computer starts on its own terminal, review and place in Files, unless the
        // change names them.
        ...(rest.computer && rest.computer !== computer?.id
          ? {
              term: undefined,
              review: undefined,
              files: undefined,
              file: undefined,
            }
          : {}),
        ...rest,
        ...(wide && rest.tab ? { fold: undefined } : {}),
      },
      push && "quiet"
    )
  }
  const openThread = (chat: Chat) =>
    onSearch(
      {
        thread: chat.id,
        computer: chat.sandboxId === computer?.id ? search.computer : undefined,
      },
      true
    )
  const leaveThread = () => onSearch({ thread: undefined }, true)
  const profile = (view: ProfileView | undefined, section?: string) =>
    onSearch({
      profile: view,
      open: view === "settings" ? section : undefined,
      from: view === "settings" ? search.from : undefined,
    })

  // Esc steps back: a dialog or menu closes itself, then the profile, then the thread.
  useEffect(() => {
    const onKey = (e: KeyboardEvent) => {
      if (e.key !== "Escape" || e.defaultPrevented) return
      const t = e.target as HTMLElement | null
      if (
        t?.closest(
          "input, textarea, select, [contenteditable], .xterm, [role=dialog], [role=menu], [role=listbox]"
        ) &&
        !t.closest('[aria-label$="\'s profile"]')
      )
        return
      if (document.querySelector("[data-open][role=dialog], [role=menu]"))
        return
      if (search.profile) profile(undefined)
      else if (!wide && overlay) setOverlay(false)
      else if (full) screen({ full: false })
      else if (search.thread) leaveThread()
      else return
      e.preventDefault()
    }
    window.addEventListener("keydown", onKey)
    return () => window.removeEventListener("keydown", onKey)
  })

  return (
    <div className="relative flex min-h-0 flex-1">
      {full && (
        <ConversationStrip
          teammate={teammate}
          waiting={waiting.length}
          onShow={() => screen({ full: false })}
        />
      )}
      {/* Kept while the screen takes the page, so a draft and the scroll stay. */}
      <div
        className={cn(
          "relative flex min-h-0 min-w-0 flex-col",
          !computer
            ? "flex-[1.25]"
            : folded
              ? "flex-1"
              : "flex-1 lg:min-w-[25rem]",
          full && "hidden"
        )}
      >
        {search.thread ? (
          <TeammateThread
            key={search.thread}
            env={env}
            teammate={teammate}
            log={log}
            computer={
              owned.length > 1
                ? owned.find((s) => s.id === thread?.sandboxId)?.name
                : undefined
            }
            onBack={leaveThread}
            onShowChanges={(turn) => {
              setTurnId(turn)
              screen({ tab: "changes", computer: thread?.sandboxId })
            }}
            onTerminal={(term) =>
              screen({ tab: "terminal", term, computer: thread?.sandboxId })
            }
            onOpenFile={(file) =>
              screen({
                tab: "changes",
                file,
                files: undefined,
                computer: thread?.sandboxId,
                push: true,
              })
            }
          />
        ) : (
          <TeammateHome
            env={env}
            teammate={teammate}
            chats={mine}
            computers={owned}
            computer={computer}
            waiting={waiting}
            watching={!folded && search.tab === "browser"}
            onThread={openThread}
            onProfile={(view) => profile(view)}
            onWatch={() => screen({ tab: "browser" })}
            onReview={(a) =>
              screen({ tab: "changes", review: a.id, computer: a.sandboxId })
            }
          />
        )}
        {search.profile === "card" && (
          <ProfileCard
            env={env}
            teammate={teammate}
            computers={owned}
            onOpen={profile}
            onComputer={(c) => {
              profile(undefined)
              screen({ computer: c })
            }}
          />
        )}
      </div>
      {computer ? (
        <div
          className={cn(
            "flex min-h-0",
            folded ? "shrink-0" : full ? "flex-1" : "flex-[1.25]",
            !wide && !folded && "absolute inset-0 z-20 bg-background shadow-lg"
          )}
        >
          <ComputerScreen
            key={computer.id}
            env={env}
            sandbox={computer}
            computers={owned}
            owner={teammate}
            search={{ ...search, fold: folded, full }}
            enlargeable={wide}
            onChange={screen}
            thread={
              thread && log.chat && thread.sandboxId === computer.id
                ? {
                    chat: log.chat,
                    turns: log.turns,
                    items: log.items,
                    turnId,
                    onTurn: setTurnId,
                  }
                : undefined
            }
          />
        </div>
      ) : (
        sandboxes.data && (
          <NoComputer env={env} teammate={teammate} hidden={!wide} />
        )
      )}
      <ProfileSheet
        env={env}
        teammate={teammate}
        view={search.profile}
        section={search.open}
        fromSetup={search.from === "setup"}
        onOpen={profile}
      />
    </div>
  )
}

/** The conversation while the screen takes the page: the teammate, and the way back. */
function ConversationStrip({
  teammate,
  waiting,
  onShow,
}: {
  teammate: Teammate
  waiting: number
  onShow: () => void
}) {
  return (
    <section
      aria-label={`${teammate.name}'s conversation, folded`}
      className="flex w-12 shrink-0 flex-col items-center gap-1 pt-4.5 pb-2.5"
    >
      <Button
        variant="ghost"
        size="icon-sm"
        aria-label="Show the conversation"
        title="Show the conversation"
        className="text-muted-foreground hover:text-foreground"
        onClick={onShow}
      >
        <ArrowLineRightIcon />
      </Button>
      <span className="my-1 h-px w-6 bg-border" />
      <span
        className="relative"
        title={
          waiting
            ? `${teammate.name}: ${waiting} waiting for you`
            : teammate.name
        }
      >
        <TeammateAvatar
          name={teammate.name}
          avatar={teammate.avatar}
          className="size-7 text-meta"
        />
        {waiting > 0 && (
          <span className="absolute -top-0.5 -right-0.5 size-2 rounded-full bg-(--st-waiting) ring-2 ring-background" />
        )}
      </span>
    </section>
  )
}

/** Where the screen goes while the teammate has no computer: an offer to give it one. */
function NoComputer({
  env,
  teammate,
  hidden,
}: {
  env: string
  teammate: Teammate
  hidden: boolean
}) {
  const [creating, setCreating] = useState(false)
  if (hidden) return null
  return (
    <section
      aria-label={`${teammate.name}'s computer`}
      className="flex min-h-0 flex-1 py-2.5 pr-3 pl-0.5"
    >
      <div className="m-0 grid flex-1 place-content-center justify-items-center gap-2 rounded-3xl border border-dashed px-6 text-center">
        <span className="mb-1 grid size-11 place-items-center rounded-xl border bg-card text-muted-foreground shadow-xs">
          <MonitorIcon className="size-5" />
        </span>
        <p className="text-sm font-medium">
          {teammate.name} has no computer yet
        </p>
        <p className="max-w-72 text-xs/relaxed text-muted-foreground">
          Give it one to run code, use its browser and show you previews.
        </p>
        <Button
          size="sm"
          variant="outline"
          className="mt-1"
          onClick={() => setCreating(true)}
        >
          <PlusIcon />
          Give {teammate.name} a computer
        </Button>
        <CreateSandboxDialog
          env={env}
          teammate={teammate.id}
          open={creating}
          onOpenChange={setCreating}
        />
      </div>
    </section>
  )
}
