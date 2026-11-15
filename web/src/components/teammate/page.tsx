import { useEffect, useState } from "react"
import {
  ArrowLineRightIcon,
  MonitorIcon,
  PlusIcon,
} from "@phosphor-icons/react"
import { ComputerScreen, type ScreenChange } from "@/components/computer/screen"
import { CreateSandboxDialog } from "@/components/create-sandbox-dialog"
import {
  ResizeHandle,
  useElementWidth,
  useStoredSize,
} from "@/components/resize-handle"
import { useWideScreen } from "@/components/side-panel"
import { TeammateAvatar } from "@/components/teammate-avatar"
import { TeammateHome, teammateApprovals } from "@/components/teammate/home"
import {
  ProfileCard,
  ProfileSettingsPage,
  ProfileSheet,
} from "@/components/teammate/profile"
import { TeammateThread } from "@/components/teammate/thread"
import { phase } from "@/components/status-badge"
import { openStartWork } from "@/lib/start-work"
import { Button } from "@/components/ui/button"
import { usePendingApprovals } from "@/hooks/use-pending-approvals"
import { $api, type Teammate } from "@/lib/api/client"
import { useChat, type Chat } from "@/lib/chats"
import {
  lastComputer,
  screenComputer,
  rememberComputer,
  type ProfileView,
  type TeammateSearch,
} from "@/lib/computer"
import { cn } from "@/lib/utils"

export type SearchChange = Partial<Record<keyof TeammateSearch, unknown>>

/**
 * A teammate's page (docs/design/teammate-computer.md) is one layout in three states: its home,
 * with the computer folded to a rail at the right; its desk, the same home narrowed to a panel
 * beside the open computer, with no thread and no chat; and a thread, whose chat takes the
 * home's place while the computer side stays as it was. Either side folds to a rail. The
 * profile opens over the left side. Esc closes what is on top, then the thread.
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
  const computer = screenComputer(
    owned,
    mine,
    search.computer,
    thread?.sandboxId,
    lastComputer(env, id)
  )
  // The screen follows the open thread, unless the user pinned another computer beside it;
  // the thread's computer is then off the screen.
  const threadComputer = owned.find((s) => s.id === thread?.sandboxId)
  const offScreen =
    threadComputer && threadComputer.id !== computer?.id
      ? threadComputer
      : undefined

  // Only a pick counts: what the page chose by itself while the chats loaded would stick.
  const picked = owned.find((s) => s.id === search.computer)?.id
  useEffect(() => {
    if (picked) rememberComputer(env, id, picked)
  }, [env, id, picked])

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
  // Without a thread the screen opens only for a tab: the home has the page otherwise.
  const folded = wide
    ? !!search.fold || (!search.thread && !search.tab)
    : !overlay
  // On narrow screens the open screen covers the conversation anyway.
  const full = wide && !folded && !!search.full

  const screen = (change: ScreenChange) => {
    const { fold, full: enlarge, push, ...rest } = change
    // Folding and enlarging undo each other. Opening the screen from the home opens a tab.
    if (fold !== undefined || enlarge !== undefined) {
      if (wide)
        onSearch({
          fold: fold || undefined,
          full: enlarge || undefined,
          ...(folded && !fold && !rest.tab && !search.tab
            ? { tab: "terminal" }
            : {}),
        })
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
              port: undefined,
              review: undefined,
              change: undefined,
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
  // The computer side stays as it is: folded from the home, open from the desk.
  const openThread = (chat: Chat) =>
    onSearch(
      {
        thread: chat.id,
        computer: chat.sandboxId === computer?.id ? search.computer : undefined,
        fold: folded || undefined,
        full: undefined,
      },
      true
    )
  const leaveThread = () =>
    onSearch(
      {
        thread: undefined,
        ...(!folded && !search.tab ? { tab: "terminal" } : {}),
      },
      true
    )
  const startWork = (bring?: boolean) =>
    openStartWork({
      teammate: id,
      computer: computer?.id,
      ...(bring
        ? { mode: "chat", bring: { term: search.term ?? "main" } }
        : {}),
    })
  // The desk: the home narrowed to a panel beside the open computer, no thread open.
  const desk = wide && !search.thread && !folded && !full
  // Memory or tasks opened from a settings row lead back to that row.
  const [settingsBack, setSettingsBack] = useState<string>()
  const backable = search.profile === "memory" || search.profile === "tasks"
  if (settingsBack !== undefined && !backable && search.profile !== "settings")
    setSettingsBack(undefined)
  const profile = (view: ProfileView | undefined, section?: string) =>
    onSearch({
      profile: view,
      open: view === "settings" || view === "memory" ? section : undefined,
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
        !t.closest('[aria-label$="\'s profile"], [aria-label$="\'s settings"]')
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

  // The conversation's share of the page beside the screen, kept in this browser; dragged past
  // its minimum, the screen takes the page.
  const [box, boxWidth] = useElementWidth<HTMLDivElement>()
  const [threadShare, setThreadShare] = useStoredSize(
    "studio.conversation-share",
    conversationSplit.share
  )
  // The desk's home is a narrower panel, with a share of its own.
  const [deskShare, setDeskShare] = useStoredSize(
    "studio.desk-share",
    deskSplit.share
  )
  const sizes = desk ? deskSplit : conversationSplit
  const share = desk ? deskShare : threadShare
  const setShare = desk ? setDeskShare : setThreadShare
  const split = wide && !!computer && !folded && !full && boxWidth > 0
  const maxConversation = Math.max(sizes.min, boxWidth - sizes.screenMin)
  const conversationWidth = Math.round(
    Math.min(Math.max(share * boxWidth, sizes.min), maxConversation)
  )

  const sheet = (
    <ProfileSheet
      env={env}
      teammate={teammate}
      view={search.profile}
      section={search.open}
      back={settingsBack}
      onOpen={profile}
    />
  )
  // The settings are a page of their own, in place of the conversation and the screen.
  if (search.profile === "settings")
    return (
      <div className="relative flex min-h-0 flex-1">
        <ProfileSettingsPage
          env={env}
          teammate={teammate}
          section={search.open}
          fromSetup={search.from === "setup"}
          onOpen={profile}
          onView={(v) => {
            setSettingsBack(v)
            profile(v)
          }}
        />
        {sheet}
      </div>
    )

  return (
    <div ref={box} className="relative flex min-h-0 flex-1">
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
            ? search.thread
              ? "flex-[1.25]"
              : "flex-1"
            : folded
              ? "flex-1"
              : "flex-1 lg:min-w-[25rem]",
          full && "hidden",
          split && "flex-none"
        )}
        style={split ? { width: conversationWidth } : undefined}
      >
        {search.thread ? (
          <TeammateThread
            key={search.thread}
            env={env}
            teammate={teammate}
            log={log}
            // The crumb row names the thread's computer; this says only that it is off the screen.
            computer={offScreen?.name}
            onShowComputer={
              offScreen ? () => screen({ computer: offScreen.id }) : undefined
            }
            onBack={leaveThread}
            onShowChanges={(turn) => {
              setTurnId(turn)
              screen({
                tab: "changes",
                change: "thread",
                review: undefined,
                computer: thread?.sandboxId,
              })
            }}
            onTerminal={(term) =>
              screen({ tab: "terminal", term, computer: thread?.sandboxId })
            }
            onOpenFile={(file) =>
              screen({
                tab: "files",
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
            compact={desk}
            loaded={!!sandboxes.data}
            onThread={openThread}
            onProfile={(view) => profile(view)}
            onWatch={() => screen({ tab: "browser" })}
            onReview={(a) =>
              screen({ tab: "changes", review: a.id, computer: a.sandboxId })
            }
            onStartWork={() => startWork()}
            onBringIn={
              desk && computer && phase(computer) === "ready"
                ? () => startWork(true)
                : undefined
            }
            onOpenComputer={(c, tab) => screen({ computer: c.id, tab })}
            onExpand={() => screen({ fold: true })}
            onFold={() => screen({ full: true })}
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
      {split && (
        <ResizeHandle
          label={desk ? "the home" : "the conversation"}
          side="left"
          size={conversationWidth}
          min={sizes.min}
          max={maxConversation}
          defaultSize={Math.round(sizes.share * boxWidth)}
          onResize={(px) => setShare(px / boxWidth)}
          onFold={(fold) => fold && screen({ full: true })}
          className="-mx-1"
        />
      )}
      {computer ? (
        <div
          className={cn(
            "flex min-h-0",
            folded
              ? "shrink-0"
              : full || split
                ? "min-w-0 flex-1"
                : "flex-[1.25]",
            !wide && !folded && "absolute inset-0 z-20 bg-background shadow-lg"
          )}
        >
          <ComputerScreen
            key={computer.id}
            env={env}
            sandbox={computer}
            owner={teammate}
            search={{ ...search, fold: folded, full }}
            enlargeable={wide}
            onChange={screen}
            chatComputer={offScreen}
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
        // The home says so itself, with a card to give it one.
        sandboxes.data &&
        search.thread && (
          <NoComputer env={env} teammate={teammate} hidden={!wide} />
        )
      )}
      {sheet}
    </div>
  )
}

/** The conversation beside the screen: its default share of the page and the ends it drags to. */
const conversationSplit = { share: 0.444, min: 320, screenMin: 480 }

/** The desk's home beside the computer: a narrow panel by default. */
const deskSplit = { share: 0.3, min: 300, screenMin: 480 }

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
