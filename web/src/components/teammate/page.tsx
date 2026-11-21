import { useEffect, useState } from "react"
import { useRouter } from "@tanstack/react-router"
import { MonitorIcon, PlusIcon } from "@phosphor-icons/react"
import { ComputerScreen, type ScreenChange } from "@/components/computer/screen"
import { CreateSandboxDialog } from "@/components/create-sandbox-dialog"
import { opensEnlarged } from "@/components/git-review"
import {
  ResizeHandle,
  useElementWidth,
  useStoredSize,
} from "@/components/resize-handle"
import { useWideScreen } from "@/components/side-panel"
import { InTopBar } from "@/components/top-bar-slot"
import { TeammateHome, teammateApprovals } from "@/components/teammate/home"
import {
  LayoutSwitch,
  type PageLayout,
} from "@/components/teammate/layout-switch"
import {
  ProfileSettingsPage,
  ProfileSheet,
} from "@/components/teammate/profile"
import { TeammateThread } from "@/components/teammate/thread"
import { Button } from "@/components/ui/button"
import { usePendingApprovals } from "@/hooks/use-pending-approvals"
import { $api, type Teammate } from "@/lib/api/client"
import { ClaimsHidden } from "@/lib/approval-claims"
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
 * home's place while the computer side stays as it was. Either side folds to a rail, and the
 * line between them drags all the way: to the screen's rail, to any split, to the screen taking
 * the page. On phones the home has no rail: its computer cards open the screen. Its settings
 * are a page of their own, with its memory and tasks as sheets over them. Esc closes what is
 * on top, then the thread.
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
  // The file a chip in the chat asked Changes to show, with when it asked, so asking again
  // shows it again.
  const [changeFile, setChangeFile] = useState<{ path: string; at: number }>()
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
    // Enlarging along with a place to go, as a waiting push's Review does, is one step.
    const along = enlarge !== undefined && Object.keys(rest).length > 0
    // Folding and enlarging undo each other. Opening the screen from the home opens a tab.
    if (!along && (fold !== undefined || enlarge !== undefined)) {
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
        ...(wide && along ? { full: enlarge || undefined } : {}),
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
  // The desk: the home narrowed to a panel beside the open computer, no thread open.
  const desk = wide && !search.thread && !folded && !full
  // The top bar's layout switch: the conversation alone, both, or the computer alone.
  const layout: PageLayout = folded
    ? "conversation"
    : full || !wide
      ? "computer"
      : "both"
  const setLayout = (l: PageLayout) =>
    screen(
      l === "conversation"
        ? { fold: true }
        : l === "both"
          ? { fold: false, full: false }
          : wide
            ? { full: true }
            : { fold: false }
    )
  // Memory or tasks opened from a settings row lead back to that row.
  const [settingsBack, setSettingsBack] = useState<string>()
  const backable = search.profile === "memory" || search.profile === "tasks"
  if (settingsBack !== undefined && !backable && search.profile !== "settings")
    setSettingsBack(undefined)
  // Opening the settings is a step in the browser's history, so Back, Alt+← and a phone's back
  // gesture leave them; moving between their rows, or to memory or tasks and back, is not.
  const profile = (view: ProfileView | undefined, section?: string) =>
    onSearch(
      {
        profile: view,
        open: view === "settings" || view === "memory" ? section : undefined,
        from: view === "settings" ? search.from : undefined,
      },
      view === "settings" && !search.profile && "quiet"
    )
  // Whether the settings were opened on this page, by a step in its history (every way in
  // pushes one): leaving them goes back that step rather than adding another. Opened from a
  // link or a reload, there is no step to go back to.
  const router = useRouter()
  const [lastProfile, setLastProfile] = useState(search.profile)
  const [settingsPushed, setSettingsPushed] = useState(false)
  if (lastProfile !== search.profile) {
    setLastProfile(search.profile)
    if (!lastProfile && search.profile === "settings") setSettingsPushed(true)
    else if (!search.profile) setSettingsPushed(false)
  }
  const leaveSettings = () => {
    if (settingsPushed) router.history.back()
    else profile(undefined)
  }

  // Esc steps back: a dialog or menu closes itself, then the profile, then the thread.
  useEffect(() => {
    const onKey = (e: KeyboardEvent) => {
      if (e.key !== "Escape" || e.defaultPrevented) return
      const t = e.target as HTMLElement | null
      if (
        t?.closest(
          "input, textarea, select, [contenteditable], .xterm, [role=dialog], [role=menu]:not([data-closed]), [role=listbox]"
        ) &&
        !t.closest('[aria-label$="\'s settings"]')
      )
        return
      // A menu fading out after a pick stays in the page, with the focus, for its animation;
      // it is closed already.
      if (
        document.querySelector(
          "[data-open][role=dialog], [role=menu]:not([data-closed])"
        )
      )
        return
      if (search.profile === "settings") leaveSettings()
      else if (search.profile) profile(undefined)
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
  // its minimum, the screen takes the page, and past its maximum, the screen folds to its rail.
  const [box, boxWidth] = useElementWidth<HTMLDivElement>()
  const [threadShare, setThreadShare] = useStoredSize(
    "studio.conversation-share",
    conversationSplit.share
  )
  // The desk's home is a narrower panel, with a share of its own: the home's, dragged out from
  // the rail, is the desk's.
  const [deskShare, setDeskShare] = useStoredSize(
    "studio.desk-share",
    deskSplit.share
  )
  const sizes = search.thread ? conversationSplit : deskSplit
  const share = search.thread ? threadShare : deskShare
  const setShare = search.thread ? setThreadShare : setDeskShare
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
          onBack={leaveSettings}
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
      {computer && (
        <InTopBar>
          <LayoutSwitch
            value={layout}
            onChange={setLayout}
            conversation={search.thread ? "Chat" : "Home"}
            narrow={!wide}
          />
        </InTopBar>
      )}
      {/* Kept while the screen takes the page, so a draft and the scroll stay; meanwhile its
          approvals are in the top bar's list, as nothing here shows them. */}
      <ClaimsHidden value={full || (!wide && !folded && !!computer)}>
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
              onBack={leaveThread}
              onShowChanges={(turn, file) => {
                setTurnId(turn)
                setChangeFile(file ? { path: file, at: Date.now() } : undefined)
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
                screen({
                  tab: "changes",
                  review: a.id,
                  computer: a.sandboxId,
                  ...(opensEnlarged(a) ? { full: true } : {}),
                })
              }
              onOpenComputer={(c, tab) => screen({ computer: c.id, tab })}
              onExpand={() => screen({ fold: true })}
            />
          )}
        </div>
      </ClaimsHidden>
      {wide && computer && boxWidth > 0 && (
        <ResizeHandle
          label={search.thread ? "the conversation" : "the home"}
          side="left"
          size={conversationWidth}
          min={sizes.min}
          max={maxConversation}
          defaultSize={Math.round(sizes.share * boxWidth)}
          onResize={(px) => setShare(px / boxWidth)}
          folded={full}
          foldedSize={0}
          onFold={(fold) => screen({ full: fold })}
          expanded={folded}
          expandedSize={boxWidth - railWidth}
          onExpand={(fold) => screen({ fold })}
          className="-mx-1"
        />
      )}
      {computer && !wide && folded && !search.thread ? null : computer ? (
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
            onChange={screen}
            chatComputer={offScreen}
            thread={
              thread && log.chat && thread.sandboxId === computer.id
                ? {
                    chat: log.chat,
                    turns: log.turns,
                    items: log.items,
                    turnId,
                    file: changeFile,
                    onTurn: (t) => {
                      setTurnId(t)
                      setChangeFile(undefined)
                    },
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

/** The folded screen's rail, which a drag starts from. */
const railWidth = 56

/** The conversation beside the screen: its default share of the page and the ends it drags to. */
const conversationSplit = { share: 0.444, min: 320, screenMin: 480 }

/** The desk's home beside the computer: a narrow panel by default. */
const deskSplit = { share: 0.3, min: 300, screenMin: 480 }

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
