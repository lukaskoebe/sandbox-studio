import { useEffect, useRef, useState, type ReactNode } from "react"
import { useQueryClient } from "@tanstack/react-query"
import {
  ArrowSquareOutIcon,
  BroadcastIcon,
  BroomIcon,
  BrowserIcon,
  ChatCircleDotsIcon,
  GitDiffIcon,
  PlugsConnectedIcon,
  PlusIcon,
  WarningIcon,
  XIcon,
} from "@phosphor-icons/react"
import { AgentSessions } from "@/components/agent-sessions"
import { ChatThread, requestLabel } from "@/components/chat/chat-thread"
import { SandboxChats } from "@/components/chat/sandbox-chats"
import { WorkspaceTabs, type PanelTab } from "@/components/chat/workspace-panel"
import { ConnectionsPanel } from "@/components/connections-panel"
import { SandboxBrowserPanel } from "@/components/sandbox-browser-panel"
import { StartButton } from "@/components/sandbox-lifecycle"
import { DeleteSandboxButton } from "@/components/sandbox-options-menu"
import { useSandboxPage } from "@/components/sandbox-page"
import { SandboxProgress } from "@/components/sandbox-progress"
import { SidePanel } from "@/components/side-panel"
import { phase, phaseDot } from "@/components/status-badge"
import { StatusDot } from "@/components/status-dot"
import { WaitingCount } from "@/components/waiting-count"
import { Button } from "@/components/ui/button"
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuGroup,
  DropdownMenuItem,
  DropdownMenuLabel,
  DropdownMenuSeparator,
  DropdownMenuSub,
  DropdownMenuSubContent,
  DropdownMenuSubTrigger,
  DropdownMenuTrigger,
} from "@/components/ui/dropdown-menu"
import {
  Empty,
  EmptyContent,
  EmptyDescription,
  EmptyHeader,
  EmptyMedia,
  EmptyTitle,
} from "@/components/ui/empty"
import { Spinner } from "@/components/ui/spinner"
import { Terminal } from "@/components/terminal"
import { usePendingApprovals } from "@/hooks/use-pending-approvals"
import { useAgentView } from "@/lib/agent-view"
import { $api, isReady, type Sandbox } from "@/lib/api/client"
import { chatTitle, useChat, type ChatTurn } from "@/lib/chats"
import { shortTemplate } from "@/lib/lineage"
import {
  freshPreviewHint,
  onStudioMachine,
  openPreviewTab,
} from "@/lib/previews"
import { cn } from "@/lib/utils"

/** The workspace's side panels: one is open at a time, named in the URL. */
export const workspacePanels = ["browser", "connections", "files"] as const
export type WorkspacePanelId = (typeof workspacePanels)[number]

/** A tab in the workspace: a terminal session of the guest, or a chat of the sandbox. */
type Tab = { kind: "terminal"; name: string } | { kind: "chat"; id: string }
const keyOf = (t: Tab) => `${t.kind}:${t.kind === "terminal" ? t.name : t.id}`

/**
 * The working surface of a sandbox: its terminals and chats as tabs, the one picked shown
 * in full, and the side panels beside it. The URL can name a terminal, a chat and the open
 * panel, so links from elsewhere land on the right one.
 */
export function SandboxWorkspace({
  search,
  onPanel,
}: {
  search: { terminal?: string; chat?: string; panel?: WorkspacePanelId }
  onPanel: (panel: WorkspacePanelId | undefined) => void
}) {
  const { env, sandbox: sb } = useSandboxPage()
  const queryClient = useQueryClient()
  const view = useAgentView().view
  const p = phase(sb)
  const building = sb.status === "creating" || sb.status === "failed"
  const suspended = p === "suspended"
  const live = p === "ready" || suspended

  // Terminal tabs are the guest's tmux sessions: they outlive the browser, and sessions an
  // agent starts appear here too. Open ones stay mounted while hidden, so switching keeps
  // scrollback and the connection.
  const [terminals, setTerminals] = useState<string[]>([])
  const [closing, setClosing] = useState<ReadonlySet<string>>(new Set())
  const [adopted, setAdopted] = useState(false)
  const [chatIds, setChatIds] = useState<string[]>([])
  const [picked, setPicked] = useState<Tab | null>(null)
  const [panelTab, setPanelTab] = useState<PanelTab>("changes")
  const [turnId, setTurnId] = useState<string>()

  const sessions = $api.useQuery(
    "get",
    "/api/environments/{env}/sandboxes/{id}/terminals",
    { params: { path: { env, id: sb.id } } },
    { refetchInterval: 5000, enabled: live && !suspended }
  )
  const chats = $api.useQuery(
    "get",
    "/api/environments/{env}/sandboxes/{id}/chats",
    { params: { path: { env, id: sb.id } } },
    { enabled: !!sb.teammateId }
  )
  const close = $api.useMutation(
    "delete",
    "/api/environments/{env}/sandboxes/{id}/terminals/{name}",
    {
      onSettled: () =>
        queryClient.invalidateQueries({
          queryKey: ["get", "/api/environments/{env}/sandboxes/{id}/terminals"],
        }),
    }
  )

  // Adopt sessions that exist in the guest; the first visit opens "main".
  useEffect(() => {
    if (!sessions.data) return
    const names = [...sessions.data]
      .sort((a, b) => a.created - b.created)
      .map((s) => s.name)
    const fresh = names.filter((n) => !closing.has(n))
    setTerminals((prev) => {
      const next = [...prev, ...fresh.filter((n) => !prev.includes(n))]
      return next.length || adopted ? next : ["main"]
    })
    setAdopted(true)
    // A closed session leaves the closing set once the guest no longer lists it.
    setClosing((prev) => {
      const still = new Set([...prev].filter((n) => names.includes(n)))
      return still.size === prev.size ? prev : still
    })
  }, [sessions.data, closing, adopted])

  // A link that names a terminal or a chat opens it.
  useEffect(() => {
    if (!search.terminal) return
    const name = search.terminal
    setTerminals((prev) => (prev.includes(name) ? prev : [...prev, name]))
    setPicked({ kind: "terminal", name })
  }, [search.terminal])
  useEffect(() => {
    if (!search.chat) return
    const id = search.chat
    setChatIds((prev) => (prev.includes(id) ? prev : [...prev, id]))
    setPicked({ kind: "chat", id })
  }, [search.chat])

  // Work mode: in chat mode a sandbox with chats opens its latest one, unless the user or
  // a link already picked a tab.
  const seeded = useRef(false)
  useEffect(() => {
    if (seeded.current || view === undefined || !chats.data) return
    seeded.current = true
    const latest = [...chats.data]
      .sort((a, b) => Date.parse(b.updatedAt) - Date.parse(a.updatedAt))
      .at(0)
    if (view !== "chat" || !latest || picked) return
    setChatIds((prev) =>
      prev.includes(latest.id) ? prev : [...prev, latest.id]
    )
    setPicked({ kind: "chat", id: latest.id })
  }, [view, chats.data, picked])

  const tabs: Tab[] = [
    ...terminals.map((name): Tab => ({ kind: "terminal", name })),
    ...chatIds.map((id): Tab => ({ kind: "chat", id })),
  ]
  // The picked tab, or the last one open when the picked one closed.
  const current =
    tabs.find((t) => keyOf(t) === (picked && keyOf(picked))) ??
    tabs.at(-1) ??
    null
  const chatActive = current?.kind === "chat"
  const chatLog = useChat(
    env,
    current?.kind === "chat" ? current.id : undefined
  )

  const openTerminal = (name: string) => {
    setTerminals((prev) => (prev.includes(name) ? prev : [...prev, name]))
    setPicked({ kind: "terminal", name })
  }
  const openChat = (id: string) => {
    setChatIds((prev) => (prev.includes(id) ? prev : [...prev, id]))
    setPicked({ kind: "chat", id })
  }
  const newTerminal = () => {
    let n = 2
    while (terminals.includes(String(n)) || closing.has(String(n))) n++
    openTerminal(
      terminals.includes("main") || closing.has("main") ? String(n) : "main"
    )
  }
  // A terminal that exits leaves the tabs and the guest's list; closing it ends its processes.
  const drop = (name: string) => {
    setClosing((prev) => new Set(prev).add(name))
    setTerminals((prev) => prev.filter((n) => n !== name))
  }
  const closeTab = (t: Tab) => {
    if (t.kind === "terminal") {
      drop(t.name)
      close.mutate({ params: { path: { env, id: sb.id, name: t.name } } })
    } else {
      setChatIds((prev) => prev.filter((id) => id !== t.id))
    }
    if (current && keyOf(current) === keyOf(t)) setPicked(null)
  }
  const chatLabel = (id: string) => {
    const chat = chats.data?.find((c) => c.id === id)
    return chat ? chatTitle(chat) : "Chat"
  }

  // Changes of the chat in view open on the Files and changes panel.
  const showChanges = (id: string) => {
    setTurnId(id)
    setPanelTab("changes")
    onPanel("files")
  }

  // Each panel's icon takes its area's hue (docs/design/ui-style.md).
  const toggles: {
    id: WorkspacePanelId
    label: string
    icon: ReactNode
    className: string
  }[] = [
    ...(sb.teammateId
      ? [
          {
            id: "browser" as const,
            label: "Browser",
            icon: <BrowserIcon />,
            className: "[&_svg]:text-(--acc-browser)",
          },
        ]
      : []),
    {
      id: "connections",
      label: "Connections",
      icon: <PlugsConnectedIcon />,
      className: "[&_svg]:text-(--acc-network)",
    },
    {
      id: "files",
      label: "Files",
      icon: <GitDiffIcon />,
      className: "[&_svg]:text-(--acc-git)",
    },
  ]

  let main: ReactNode
  if (sb.status === "creating") {
    main = <SandboxProgress sandbox={sb} />
  } else if (sb.status === "failed") {
    main = (
      <SandboxProgress
        sandbox={sb}
        action={<DeleteSandboxButton env={env} sandbox={sb} />}
      />
    )
  } else {
    main = (
      <>
        <div
          className={cn("flex min-h-0 flex-1 flex-col", chatActive && "hidden")}
        >
          <TerminalPane
            env={env}
            sandbox={sb}
            phase={p}
            terminals={terminals}
            active={current?.kind === "terminal" ? current.name : undefined}
            suspended={suspended}
            sessionsLoaded={!!sessions.data}
            sessionsPending={sessions.isPending}
            onNew={newTerminal}
            onExit={drop}
          />
        </div>
        {chatActive &&
          (chatLog.chat ? (
            <ChatThread
              env={env}
              chat={chatLog.chat}
              log={chatLog}
              onShowChanges={showChanges}
            />
          ) : (
            <Empty className="m-auto">
              <EmptyHeader>
                {chatLog.gone ? (
                  <EmptyTitle>This chat was deleted</EmptyTitle>
                ) : chatLog.error ? (
                  <>
                    <EmptyTitle>Can&apos;t load this chat</EmptyTitle>
                    <EmptyDescription>{chatLog.error}</EmptyDescription>
                  </>
                ) : (
                  <Spinner />
                )}
              </EmptyHeader>
            </Empty>
          ))}
      </>
    )
  }

  return (
    <>
      <div
        role="group"
        aria-label="Workspace tabs and tools"
        className="flex h-9 min-w-0 shrink-0 items-center overflow-x-auto border-b bg-muted/40 px-1"
      >
        {/* Tabs on the left, tools on the right; the row scrolls when both don't fit. */}
        <div className="flex w-max min-w-full shrink-0 items-center gap-1">
          <div className="flex items-center gap-0.5">
            {tabs.map((t) => {
              const on = current !== null && keyOf(t) === keyOf(current)
              const label = t.kind === "terminal" ? t.name : chatLabel(t.id)
              return (
                <div
                  key={keyOf(t)}
                  className={cn(
                    "group flex h-7 shrink-0 items-center rounded-md text-xs",
                    on
                      ? "bg-background shadow-xs"
                      : "text-muted-foreground hover:bg-muted"
                  )}
                >
                  <button
                    type="button"
                    aria-pressed={on}
                    className={cn(
                      "flex h-full max-w-48 items-center gap-1.5 pr-1 pl-2.5",
                      t.kind === "terminal" && "font-mono"
                    )}
                    onClick={() => setPicked(t)}
                  >
                    {t.kind === "chat" && (
                      <ChatCircleDotsIcon className="size-3.5 shrink-0" />
                    )}
                    <span className="truncate">{label}</span>
                  </button>
                  <button
                    type="button"
                    aria-label={`Close ${label}`}
                    title={
                      t.kind === "terminal"
                        ? `Close ${label} (ends its processes)`
                        : `Close ${label} (the chat stays)`
                    }
                    className="mr-1 rounded p-0.5 opacity-50 group-hover:opacity-100 hover:bg-muted-foreground/20"
                    onClick={() => closeTab(t)}
                  >
                    <XIcon className="size-3" />
                  </button>
                </div>
              )
            })}
            <Button
              variant="ghost"
              size="icon-xs"
              title="New terminal"
              aria-label="New terminal"
              onClick={newTerminal}
            >
              <PlusIcon />
            </Button>
          </div>
          <div className="ml-auto flex shrink-0 items-center gap-1 pl-2">
            {!building && sb.teammateId && (
              <SandboxChats env={env} id={sb.id} onOpen={openChat} />
            )}
            {sb.teammateId && isReady(sb) && (
              <AgentSessions
                env={env}
                id={sb.id}
                teammateId={sb.teammateId}
                onAttach={openTerminal}
              />
            )}
            {isReady(sb) && <Previews env={env} sandbox={sb} />}
            <span className="mx-0.5 h-4 w-px shrink-0 bg-border" />
            {toggles.map((t) => {
              const on = search.panel === t.id
              return (
                <Button
                  key={t.id}
                  variant={on ? "secondary" : "ghost"}
                  size="sm"
                  className={t.className}
                  aria-label={t.label}
                  aria-pressed={on}
                  title={`${on ? "Close" : "Open"} the ${t.label.toLowerCase()} panel`}
                  onClick={() => onPanel(on ? undefined : t.id)}
                >
                  {t.icon}
                  <span className="hidden xl:inline">{t.label}</span>
                </Button>
              )
            })}
          </div>
        </div>
      </div>
      <div className="relative flex min-h-0 flex-1">
        <div className="flex min-h-0 min-w-0 flex-1 flex-col">{main}</div>
        {search.panel === "browser" && sb.teammateId && (
          <SandboxBrowserPanel
            key={sb.teammateId}
            env={env}
            teammate={sb.teammateId}
            onClose={() => onPanel(undefined)}
          />
        )}
        {search.panel === "connections" && (
          <SidePanel
            label="Connections"
            title="Connections"
            storageKey="studio.panel.sandbox"
            onClose={() => onPanel(undefined)}
          >
            <ConnectionsPanel env={env} sandbox={sb} />
          </SidePanel>
        )}
        {search.panel === "files" && (
          <SidePanel
            label="Files and changes"
            title="Files and changes"
            storageKey="studio.panel.sandbox"
            onClose={() => onPanel(undefined)}
          >
            <WorkspaceTabs
              env={env}
              sandboxId={sb.id}
              chat={chatLog.chat}
              turns={chatLog.turns}
              label={(t: ChatTurn) => requestLabel(chatLog.items, t)}
              panel={{
                tab: panelTab,
                setTab: setPanelTab,
                turnId,
                setTurn: setTurnId,
              }}
            />
          </SidePanel>
        )}
      </div>
      <StatusBar env={env} sandbox={sb} />
    </>
  )
}

/**
 * The workspace's status line: the sandbox's state, its ID and template, its listening ports,
 * and the approvals waiting on it.
 */
function StatusBar({ env, sandbox: sb }: { env: string; sandbox: Sandbox }) {
  const approvals = usePendingApprovals()
  const ports = $api.useQuery(
    "get",
    "/api/environments/{env}/sandboxes/{id}/ports",
    { params: { path: { env, id: sb.id } } },
    { refetchInterval: 3000, enabled: isReady(sb) }
  )
  const waiting = (approvals.data ?? []).filter(
    (a) => a.sandboxId === sb.id
  ).length
  const seg =
    "flex h-full shrink-0 items-center gap-1.5 border-r px-2.5 last:border-r-0"
  const list = ports.data ?? []
  return (
    <footer className="flex h-(--wb-statusbar) shrink-0 items-stretch overflow-x-auto border-t bg-sidebar font-mono text-[0.6875rem] whitespace-nowrap text-muted-foreground">
      <span className={cn(seg, "font-sans text-foreground")}>
        <StatusDot {...phaseDot(phase(sb))} />
      </span>
      <span className={cn(seg, "max-sm:hidden")}>{sb.id}</span>
      <span
        className={cn(seg, "max-md:hidden")}
        title={sb.templateId || undefined}
      >
        {shortTemplate(sb.templateId, sb.templateName)}
      </span>
      <span className={cn(seg, "max-md:hidden")}>
        ports {list.length ? list.map((p) => `:${p.port}`).join(" ") : "none"}
      </span>
      <span className={cn(seg, "font-sans")}>
        {waiting ? (
          <>
            <WaitingCount count={waiting} />
            waiting for you
          </>
        ) : (
          "Nothing waiting"
        )}
      </span>
      <span className="ml-auto flex items-center gap-3 px-2.5 max-md:hidden">
        gen {sb.generation}
      </span>
    </footer>
  )
}

/** The terminal tabs' area: the guest's shells, or why there are none yet. */
function TerminalPane({
  env,
  sandbox: sb,
  phase: p,
  terminals,
  active,
  suspended,
  sessionsLoaded,
  sessionsPending,
  onNew,
  onExit,
}: {
  env: string
  sandbox: Sandbox
  phase: ReturnType<typeof phase>
  terminals: string[]
  active?: string
  suspended: boolean
  sessionsLoaded: boolean
  sessionsPending: boolean
  onNew: () => void
  onExit: (name: string) => void
}) {
  if (p === "booting")
    return (
      <Empty>
        <EmptyHeader>
          <EmptyMedia variant="icon">
            <Spinner />
          </EmptyMedia>
          <EmptyTitle>Booting…</EmptyTitle>
          <EmptyDescription>
            Terminals open once the sandbox is up.
          </EmptyDescription>
        </EmptyHeader>
      </Empty>
    )
  if (p === "failed")
    return (
      <Empty>
        <EmptyHeader>
          <EmptyMedia variant="icon">
            <WarningIcon />
          </EmptyMedia>
          <EmptyTitle>The sandbox stopped unexpectedly</EmptyTitle>
          <EmptyDescription>
            Your workspace is kept. Start it again to continue.
          </EmptyDescription>
        </EmptyHeader>
        <EmptyContent>
          <StartButton env={env} sandbox={sb} />
        </EmptyContent>
      </Empty>
    )
  if (p !== "ready" && p !== "suspended")
    return (
      <Empty>
        <EmptyHeader>
          <EmptyTitle>Stopped</EmptyTitle>
          <EmptyDescription>
            Your workspace, home directory and Docker images are kept.
          </EmptyDescription>
        </EmptyHeader>
        <EmptyContent>
          <StartButton env={env} sandbox={sb} />
        </EmptyContent>
      </Empty>
    )
  if (suspended && !sessionsLoaded)
    return (
      <Empty>
        <EmptyHeader>
          <EmptyTitle>Sandbox suspended</EmptyTitle>
          <EmptyDescription>
            Resume it to continue where it left off.
          </EmptyDescription>
        </EmptyHeader>
      </Empty>
    )
  if (sessionsPending) return <Spinner className="m-auto" />
  if (terminals.length === 0)
    return (
      <Empty className="h-full">
        <EmptyHeader>
          <EmptyTitle>No terminals open</EmptyTitle>
        </EmptyHeader>
        <EmptyContent>
          <Button onClick={onNew}>
            <PlusIcon />
            New terminal
          </Button>
        </EmptyContent>
      </Empty>
    )
  return (
    <div className="flex min-h-0 flex-1 flex-col">
      {terminals.map((name) => (
        <Terminal
          key={name}
          url={`/api/environments/${env}/sandboxes/${sb.id}/terminals/${name}/attach`}
          active={name === active}
          suspended={suspended}
          onExit={() => onExit(name)}
        />
      ))}
    </div>
  )
}

/** Previews the sandbox's listening ports in a browser tab. */
function Previews({ env, sandbox: sb }: { env: string; sandbox: Sandbox }) {
  const ports = $api.useQuery(
    "get",
    "/api/environments/{env}/sandboxes/{id}/ports",
    { params: { path: { env, id: sb.id } } },
    { refetchInterval: 3000 }
  )
  const list = ports.data ?? []

  return (
    <DropdownMenu>
      <DropdownMenuTrigger render={<Button variant="ghost" size="sm" />}>
        <BroadcastIcon />
        Previews
        {list.length > 0 && (
          <span className="rounded-full bg-primary px-1.5 text-[0.625rem] text-primary-foreground">
            {list.length}
          </span>
        )}
      </DropdownMenuTrigger>
      <DropdownMenuContent align="end" className="w-64">
        <DropdownMenuGroup>
          <DropdownMenuLabel>Listening ports</DropdownMenuLabel>
          {list.length === 0 && (
            <p className="px-2 py-1.5 text-xs text-muted-foreground">
              Start a server in a terminal, e.g. on port 3000, and it shows up
              here.
            </p>
          )}
          {list.map((p) => (
            <DropdownMenuItem
              key={p.port}
              render={<button type="button" />}
              onClick={() => void openPreviewTab(env, sb.id, p.port)}
            >
              :{p.port}
              <ArrowSquareOutIcon className="ml-auto" />
            </DropdownMenuItem>
          ))}
        </DropdownMenuGroup>
        {list.length > 0 && (
          <>
            <DropdownMenuSeparator />
            <DropdownMenuSub>
              <DropdownMenuSubTrigger>
                <BroomIcon />
                Open fresh
              </DropdownMenuSubTrigger>
              <DropdownMenuSubContent className="w-56">
                <p className="px-2 py-1.5 text-[0.625rem] text-muted-foreground">
                  {freshPreviewHint}: for an app whose service worker still
                  serves an old version.
                </p>
                {list.map((p) => (
                  <DropdownMenuItem
                    key={p.port}
                    render={<button type="button" />}
                    onClick={() =>
                      void openPreviewTab(env, sb.id, p.port, true)
                    }
                  >
                    :{p.port}
                    <ArrowSquareOutIcon className="ml-auto" />
                  </DropdownMenuItem>
                ))}
              </DropdownMenuSubContent>
            </DropdownMenuSub>
          </>
        )}
        {!onStudioMachine && list.length > 0 && (
          <p className="px-2 py-1.5 text-[0.625rem] text-muted-foreground">
            Previews open on the machine running Studio.
          </p>
        )}
      </DropdownMenuContent>
    </DropdownMenu>
  )
}
