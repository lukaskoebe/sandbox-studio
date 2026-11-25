import { useCallback, useState } from "react"
import {
  Link,
  useMatchRoute,
  useNavigate,
  useSearch,
} from "@tanstack/react-router"
import { useQueryClient } from "@tanstack/react-query"
import { toast } from "sonner"
import { BaseMark } from "@/components/base-dialog"
import {
  CaretUpDownIcon,
  CheckIcon,
  DesktopTowerIcon,
  GearIcon,
  MagnifyingGlassIcon,
  PlusIcon,
  TerminalIcon,
  TrayIcon,
  UsersThreeIcon,
  WarningIcon,
} from "@phosphor-icons/react"
import { ChatContextMenu } from "@/components/chat/chat-actions"
import { CommandPalette } from "@/components/command-palette"
import { EnvironmentIcon } from "@/components/environment-icon"
import {
  Sidebar,
  SidebarContent,
  SidebarFooter,
  SidebarGroup,
  SidebarGroupAction,
  SidebarGroupContent,
  SidebarGroupLabel,
  SidebarHeader,
  SidebarMenu,
  SidebarMenuButton,
  SidebarMenuItem,
  SidebarMenuSkeleton,
  SidebarMenuSub,
  SidebarMenuSubButton,
  SidebarMenuSubItem,
  useSidebar,
} from "@/components/ui/sidebar"
import {
  SettingsLink,
  environmentPages,
  settingsPaths,
} from "@/components/settings/link"
import { ResizeHandle } from "@/components/resize-handle"
import { isNetworkApproval } from "@/lib/approval-kinds"
import { openPalette } from "@/lib/palette"
import { modKey } from "@/lib/keys"
import { Kbd } from "@/components/ui/kbd"
import { appWindow } from "@/lib/desktop"
import { cn, formatAge } from "@/lib/utils"
import { ThemeToggle } from "@/components/theme-toggle"
import { StudioMenu, StudioPower } from "@/components/studio-menu"
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuGroup,
  DropdownMenuItem,
  DropdownMenuLabel,
  DropdownMenuSeparator,
  DropdownMenuTrigger,
} from "@/components/ui/dropdown-menu"
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog"
import { Button } from "@/components/ui/button"
import { Input } from "@/components/ui/input"
import { Field, FieldLabel } from "@/components/ui/field"
import { CreateSandboxDialog } from "@/components/create-sandbox-dialog"
import { ImportSandboxDialog } from "@/components/import-sandbox-dialog"
import { LeadSidebarRow } from "@/components/lead/sidebar-row"
import { NewChatDialog } from "@/components/new-chat-dialog"
import { TeammateAvatar } from "@/components/teammate-avatar"
import { TeammateDialog } from "@/components/teammate-dialog"
import { teammateApprovals } from "@/components/teammate/home"
import { ThreadDot } from "@/components/thread-status"
import { phase, phaseDot } from "@/components/status-badge"
import { StatusDot } from "@/components/status-dot"
import { WaitingCount } from "@/components/waiting-count"
import { usePendingApprovals } from "@/hooks/use-pending-approvals"
import { useActivity } from "@/hooks/use-teammate-presence"
import {
  $api,
  errorMessage,
  type Approval,
  type Sandbox,
  type Teammate,
} from "@/lib/api/client"
import { chatTitle, type Chat } from "@/lib/chats"
import { lastComputer, screenComputer } from "@/lib/computer"
import { newThreadWith } from "@/lib/new-thread"
import { programName } from "@/lib/terminals"
import {
  agentTerminals,
  chatApprovals,
  chatStatus,
  sidebarChats,
  statusLabels,
  type AgentTerminal,
  type ThreadStatus,
} from "@/lib/threads"

/** The section labels, in the style's label type; the size replaces SidebarGroupLabel's text-xs. */
const groupLabel = "h-6 section-label text-(length:--label-size)"

/** The sidebar's width, as it starts and as double-clicking its edge puts it back. */
export const sidebarWidth = { default: 256, min: 180, max: 360, folded: 48 }

/**
 * The environment's places (docs/design/teammate-computer.md): Search, which opens the palette;
 * Home, with what waits for you; the teammates, each with one word for what it is doing or what
 * waits and under it its live threads and the last few; the user's own computers; and the
 * environment's pages, Computers first, which lists every computer. A teammate's computers are
 * on its page, not in here. Its edge drags between sidebarWidth's ends, and dragged past the
 * minimum it folds to its icons, as ⌘B does.
 */
export function AppSidebar({
  env,
  width,
  onWidth,
}: {
  env: string
  width: number
  onWidth: (px: number) => void
}) {
  const { state, setOpen } = useSidebar()
  const [creating, setCreating] = useState(false)
  const [importing, setImporting] = useState(false)
  const [addingTeammate, setAddingTeammate] = useState(false)
  const openTeammateDialog = useCallback(() => setAddingTeammate(true), [])
  const openComputerDialog = useCallback(() => setCreating(true), [])
  const sandboxes = $api.useQuery(
    "get",
    "/api/environments/{env}/sandboxes",
    { params: { path: { env } } },
    { refetchInterval: 3000 }
  )
  const health = $api.useQuery("get", "/api/health")
  const updates = $api.useQuery("get", "/api/system/updates", undefined, {
    refetchInterval: 60 * 60 * 1000,
  })
  const teammates = $api.useQuery("get", "/api/environments/{env}/teammates", {
    params: { path: { env } },
  })
  const providers = $api.useQuery("get", "/api/environments/{env}/providers", {
    params: { path: { env } },
  })
  const chats = $api.useQuery(
    "get",
    "/api/environments/{env}/chats",
    { params: { path: { env } } },
    { refetchInterval: 5000 }
  )
  const terminals = $api.useQuery("get", "/api/environments/{env}/terminals", {
    params: { path: { env } },
  })
  const approvals = usePendingApprovals()
  const pending = (approvals.data ?? []).filter((a) => a.environmentId === env)
  const matchRoute = useMatchRoute()
  const settingsOpen = !!matchRoute({
    to: "/e/$env/settings",
    params: { env },
    fuzzy: true,
  })
  const all = sandboxes.data ?? []
  // A sandbox whose teammate is gone counts as without one.
  const ownerOf = (sb: Sandbox) =>
    teammates.data?.find((p) => p.id === sb.teammateId)
  const networkWaiting = pending.filter(isNetworkApproval).length
  const yours = all.filter((sb) => !ownerOf(sb))

  return (
    <>
      <Sidebar collapsible="icon">
        <SidebarHeader
          className={cn(
            "h-(--wb-header) justify-center border-b border-sidebar-border px-2 py-0",
            // The window's buttons sit above it on macOS.
            appWindow === "inset" && "mt-7"
          )}
        >
          <EnvironmentSwitcher env={env} />
        </SidebarHeader>
        <SidebarContent className="gap-0">
          <SidebarGroup className="py-2">
            <SidebarMenu>
              <SidebarMenuItem>
                <SidebarMenuButton
                  tooltip={`Search (${modKey}K)`}
                  className="mb-1 border border-border bg-card text-muted-foreground group-data-[collapsible=icon]:border-transparent group-data-[collapsible=icon]:bg-transparent hover:text-foreground"
                  onClick={openPalette}
                >
                  <MagnifyingGlassIcon />
                  <span className="min-w-0 flex-1 truncate">Search</span>
                  <Kbd className="group-data-[collapsible=icon]:hidden">
                    {modKey}K
                  </Kbd>
                </SidebarMenuButton>
              </SidebarMenuItem>
              <SidebarMenuItem>
                <SidebarMenuButton
                  tooltip="Home"
                  isActive={!!matchRoute({ to: "/e/$env", params: { env } })}
                  render={
                    <Link
                      to="/e/$env"
                      params={{ env }}
                      activeOptions={{ exact: true }}
                    />
                  }
                >
                  <TrayIcon />
                  <span className="min-w-0 flex-1 truncate">Home</span>
                  <WaitingCount count={pending.length} />
                </SidebarMenuButton>
              </SidebarMenuItem>
            </SidebarMenu>
          </SidebarGroup>
          <SidebarGroup className="py-1">
            <SidebarGroupLabel className={groupLabel}>
              Teammates
            </SidebarGroupLabel>
            <SidebarGroupAction
              className="top-1.5"
              title="New teammate"
              onClick={() => setAddingTeammate(true)}
            >
              <PlusIcon />
            </SidebarGroupAction>
            <SidebarGroupContent>
              <SidebarMenu>
                {/* Only the first try shows the skeleton: pages that load teammates retry a
                  failed list, and the sidebar says it failed meanwhile. */}
                {teammates.isPending &&
                  teammates.failureCount === 0 &&
                  Array.from({ length: 2 }, (_, i) => (
                    <SidebarMenuItem key={i}>
                      <SidebarMenuSkeleton showIcon />
                    </SidebarMenuItem>
                  ))}
                {!teammates.data && teammates.failureCount > 0 && (
                  <SidebarMenuItem>
                    <SidebarMenuButton
                      size="sm"
                      tooltip={errorMessage(
                        teammates.error ?? teammates.failureReason
                      )}
                      title={errorMessage(
                        teammates.error ?? teammates.failureReason
                      )}
                      disabled={teammates.isFetching}
                      onClick={() => void teammates.refetch()}
                    >
                      <WarningIcon className="text-destructive" />
                      <span className="min-w-0 flex-1 truncate">
                        Teammates didn't load
                      </span>
                      <span className="text-caption text-muted-foreground">
                        {teammates.isFetching ? "retrying" : "retry"}
                      </span>
                    </SidebarMenuButton>
                  </SidebarMenuItem>
                )}
                <LeadSidebarRow env={env} />
                {teammates.data?.map((p) => {
                  const computers = all.filter((sb) => sb.teammateId === p.id)
                  return (
                    <TeammateRow
                      key={p.id}
                      env={env}
                      teammate={p}
                      computers={computers}
                      chats={chats.data ?? []}
                      pending={pending}
                      terminals={agentTerminals(
                        terminals.data,
                        new Set(computers.map((sb) => sb.id))
                      )}
                    />
                  )
                })}
                {/* The group ends with the list of them all, the way "Another computer" ends
                    a teammate's computers. */}
                <SidebarMenuItem>
                  <SidebarMenuButton
                    size="sm"
                    tooltip="All teammates"
                    className="text-muted-foreground"
                    isActive={
                      !!matchRoute({ to: "/e/$env/teammates", params: { env } })
                    }
                    render={
                      <Link
                        to="/e/$env/teammates"
                        params={{ env }}
                        activeOptions={{ exact: true }}
                      />
                    }
                  >
                    <UsersThreeIcon />
                    <span className="min-w-0 flex-1 truncate">
                      All teammates
                    </span>
                  </SidebarMenuButton>
                </SidebarMenuItem>
              </SidebarMenu>
            </SidebarGroupContent>
          </SidebarGroup>
          <SidebarGroup className="py-1">
            <SidebarGroupLabel className={groupLabel}>
              Your computers
            </SidebarGroupLabel>
            <DropdownMenu>
              <DropdownMenuTrigger
                render={
                  <SidebarGroupAction
                    className="top-1.5 data-popup-open:bg-sidebar-accent"
                    title="New computer"
                  />
                }
              >
                <PlusIcon />
              </DropdownMenuTrigger>
              <DropdownMenuContent align="start" className="min-w-60">
                <DropdownMenuItem onClick={() => setCreating(true)}>
                  <MenuText
                    title="New computer"
                    hint="A computer of your own, without a teammate"
                  />
                </DropdownMenuItem>
                <DropdownMenuItem onClick={() => setImporting(true)}>
                  <MenuText
                    title="Import a computer"
                    hint="From an export file"
                  />
                </DropdownMenuItem>
              </DropdownMenuContent>
            </DropdownMenu>
            <SidebarGroupContent>
              <SidebarMenu>
                {yours.map((sb) => (
                  <ComputerRow
                    key={sb.id}
                    env={env}
                    sandbox={sb}
                    pending={pending}
                  />
                ))}
              </SidebarMenu>
            </SidebarGroupContent>
          </SidebarGroup>
          <SidebarGroup className="py-1">
            <SidebarGroupLabel className={groupLabel}>
              Environment
            </SidebarGroupLabel>
            <SidebarGroupContent>
              <SidebarMenu>
                <SidebarMenuItem>
                  <SidebarMenuButton
                    tooltip="Computers"
                    isActive={
                      !!matchRoute({ to: "/e/$env/computers", params: { env } })
                    }
                    render={<Link to="/e/$env/computers" params={{ env }} />}
                  >
                    <DesktopTowerIcon />
                    <span className="min-w-0 flex-1 truncate">Computers</span>
                  </SidebarMenuButton>
                </SidebarMenuItem>
                {environmentPages.map((page) => {
                  const waiting = page.id === "network" ? networkWaiting : 0
                  return (
                    <SidebarMenuItem key={page.id}>
                      <SidebarMenuButton
                        tooltip={page.label}
                        isActive={
                          !!matchRoute({
                            to: settingsPaths[page.id],
                            params: { env },
                            fuzzy: true,
                          })
                        }
                        render={<SettingsLink section={page.id} />}
                      >
                        <EnvironmentIcon page={page.id} />
                        <span className="min-w-0 flex-1 truncate">
                          {page.label}
                        </span>
                        <WaitingCount count={waiting} />
                      </SidebarMenuButton>
                    </SidebarMenuItem>
                  )
                })}
              </SidebarMenu>
            </SidebarGroupContent>
          </SidebarGroup>
        </SidebarContent>
        <SidebarFooter className="gap-1 border-t border-sidebar-border p-2">
          <SidebarMenu>
            <SidebarMenuItem>
              <SidebarMenuButton
                size="sm"
                tooltip="Settings"
                isActive={settingsOpen}
                render={<SettingsLink section="general" />}
              >
                <GearIcon />
                <span>Settings</span>
                {updates.data?.available && (
                  <span className="ml-auto text-meta text-primary">
                    Update available
                  </span>
                )}
              </SidebarMenuButton>
            </SidebarMenuItem>
          </SidebarMenu>
          <div className="@container flex items-center justify-between gap-2 group-data-[collapsible=icon]:hidden">
            <StudioMenu version={health.data?.version} />
            <ThemeToggle />
          </div>
        </SidebarFooter>
        <ResizeHandle
          label="the sidebar"
          side="left"
          size={width}
          min={sidebarWidth.min}
          max={sidebarWidth.max}
          defaultSize={sidebarWidth.default}
          onResize={onWidth}
          folded={state === "collapsed"}
          foldedSize={sidebarWidth.folded}
          onFold={(folded) => setOpen(!folded)}
          className="absolute inset-y-0 -right-1 hidden md:block"
        />
      </Sidebar>
      {/* Outside the sidebar, which on phones is a drawer that is not there while it is shut. */}
      <CreateSandboxDialog
        env={env}
        open={creating}
        onOpenChange={setCreating}
      />
      <ImportSandboxDialog
        env={env}
        open={importing}
        onOpenChange={setImporting}
      />
      <TeammateDialog
        env={env}
        providers={providers.data ?? []}
        open={addingTeammate}
        onOpenChange={setAddingTeammate}
      />
      <NewChatDialog env={env} onNewTeammate={openTeammateDialog} />
      <StudioPower />
      <CommandPalette
        env={env}
        teammates={teammates.data ?? []}
        sandboxes={all}
        chats={chats.data ?? []}
        pending={pending}
        onNewTeammate={openTeammateDialog}
        onNewComputer={openComputerDialog}
      />
    </>
  )
}

function MenuText({ title, hint }: { title: string; hint: string }) {
  return (
    <span className="flex flex-col gap-0.5">
      <span>{title}</span>
      <span className="text-xs text-muted-foreground">{hint}</span>
    </span>
  )
}

/**
 * A teammate: its avatar and name, and on the right what waits for you, else one word for what
 * it is doing or why it can't (working, no computer, stopped); nothing while it is idle. On
 * hover, >_ opens the terminal of the computer last used for it and + asks what to start.
 * Under it, its threads: every one that runs or waits, its agents in terminals, and the latest
 * others of the last day; the rest are on its home.
 */
function TeammateRow({
  env,
  teammate,
  computers,
  chats,
  pending,
  terminals,
}: {
  env: string
  teammate: Teammate
  computers: Sandbox[]
  chats: Chat[]
  pending: Approval[]
  /** Its agents in terminals of its computers, with no chat behind them. */
  terminals: AgentTerminal[]
}) {
  const matchRoute = useMatchRoute()
  const navigate = useNavigate()
  const search: {
    thread?: string
    computer?: string
    tab?: string
    term?: string
  } = useSearch({ strict: false })
  const onPage = !!matchRoute({
    to: "/e/$env/teammates/$id",
    params: { env, id: teammate.id },
    fuzzy: true,
  })
  const waiting = teammateApprovals(
    pending,
    env,
    teammate.id,
    new Set(computers.map((sb) => sb.id))
  ).length
  // A harness printing in one of its terminals is work too; another program there isn't.
  const terminal = useActivity(env)(teammate.id).terminal
  const mine = chats.filter((c) => c.teammateId === teammate.id)
  const working =
    mine.some((c) => c.state === "running") || terminal?.state === "working"
  const busy = !working && terminal?.state === "busy"
  const up = computers.find((sb) => phase(sb) === "ready")
  const status = working
    ? "working"
    : computers.length === 0
      ? "no computer"
      : up
        ? busy
          ? programName(terminal.program)
          : undefined
        : phaseDot(phase(computers[0])).label.toLowerCase()
  const asks = (c: Chat) => chatApprovals(c, pending).length
  const listed = sidebarChats(mine, asks, onPage ? search.thread : undefined)
  const nameOf = (id?: string) => computers.find((sb) => sb.id === id)?.name
  const openTerminal = (t: AgentTerminal) =>
    onPage &&
    !search.thread &&
    search.tab === "terminal" &&
    search.computer === t.sandboxId &&
    search.term === t.name
  const home = onPage && !search.thread && !terminals.some(openTerminal)
  const desk = computers.length
    ? screenComputer(
        computers,
        mine,
        undefined,
        undefined,
        lastComputer(env, teammate.id)
      )
    : undefined
  const action =
    "flex size-5 items-center justify-center rounded-[calc(var(--radius-sm)-2px)] text-sidebar-foreground outline-hidden hover:bg-sidebar-accent focus-visible:ring-2 focus-visible:ring-sidebar-ring [&>svg]:size-3.5"
  return (
    <>
      <SidebarMenuItem>
        <SidebarMenuButton
          tooltip={teammate.name}
          // The avatar is wider than an icon; collapsed, the padding keeps it in the middle.
          className="group-data-[collapsible=icon]:p-1.5!"
          isActive={home}
          render={
            <Link
              to="/e/$env/teammates/$id"
              params={{ env, id: teammate.id }}
            />
          }
        >
          <TeammateAvatar
            name={teammate.name}
            avatar={teammate.avatar}
            className="size-5 text-[0.5625rem]"
          />
          <span className="min-w-0 flex-1 truncate">{teammate.name}</span>
          <span className="flex min-w-0 items-center md:group-focus-within/menu-item:opacity-0 md:group-hover/menu-item:opacity-0">
            {waiting > 0 ? (
              <WaitingCount count={waiting} />
            ) : (
              status && (
                <span
                  title={busy ? `${terminal.program} running` : undefined}
                  className={
                    working || busy
                      ? "flex max-w-24 items-center gap-1.5 text-caption text-muted-foreground"
                      : "text-caption text-muted-foreground"
                  }
                >
                  {working && (
                    <span className="size-1.5 animate-pulse rounded-full bg-(--st-running)" />
                  )}
                  {busy && (
                    <span className="size-1.5 shrink-0 rounded-full ring-1 ring-current ring-inset" />
                  )}
                  <span className="truncate">{status}</span>
                </span>
              )
            )}
          </span>
        </SidebarMenuButton>
        <span className="absolute top-1.5 right-1 flex items-center gap-0.5 group-data-[collapsible=icon]:hidden max-md:hidden md:opacity-0 md:group-focus-within/menu-item:opacity-100 md:group-hover/menu-item:opacity-100">
          {desk && (
            <Link
              to="/e/$env/teammates/$id"
              params={{ env, id: teammate.id }}
              search={{ computer: desk.id, tab: "terminal" }}
              aria-label={`A terminal on ${desk.name}`}
              title={`A terminal on ${desk.name}`}
              className={action}
            >
              <TerminalIcon />
            </Link>
          )}
          <Link
            to="/e/$env/teammates/$id"
            params={{ env, id: teammate.id }}
            aria-label={`New thread with ${teammate.name}`}
            title={`New thread with ${teammate.name}`}
            className={action}
            onClick={() => newThreadWith(teammate.id)}
          >
            <PlusIcon />
          </Link>
        </span>
      </SidebarMenuItem>
      {(listed.length > 0 || terminals.length > 0) && (
        <li className="list-none">
          <SidebarMenuSub
            aria-label={`${teammate.name}'s threads`}
            className="mr-0 gap-px pr-0"
          >
            {terminals.map((t) => (
              <ThreadRow
                key={`${t.sandboxId}-${t.name}`}
                status="terminal"
                title={t.title || `${teammate.name} in a terminal`}
                meta={[
                  t.state === "working" ? "working" : "in the terminal",
                  computers.length > 1 && nameOf(t.sandboxId),
                ]}
                active={openTerminal(t)}
                link={
                  <Link
                    to="/e/$env/teammates/$id"
                    params={{ env, id: teammate.id }}
                    search={{
                      computer: t.sandboxId,
                      tab: "terminal",
                      term: t.name,
                    }}
                  />
                }
              />
            ))}
            {listed.map((c) => {
              const n = asks(c)
              const st = chatStatus(c, n)
              return (
                <ChatContextMenu
                  key={c.id}
                  env={env}
                  chat={c}
                  onTerminal={(term) =>
                    void navigate({
                      to: "/e/$env/teammates/$id",
                      params: { env, id: teammate.id },
                      search: {
                        computer: c.sandboxId,
                        tab: "terminal",
                        term,
                      },
                    })
                  }
                  onDeleted={() => {
                    if (onPage && search.thread === c.id)
                      void navigate({
                        to: "/e/$env/teammates/$id",
                        params: { env, id: teammate.id },
                      })
                  }}
                >
                  <ThreadRow
                    status={st}
                    title={chatTitle(c)}
                    meta={[
                      st === "done" || st === "ended"
                        ? `${statusLabels[st]} ${formatAge(c.updatedAt)}`
                        : statusLabels[st],
                      computers.length > 1 && nameOf(c.sandboxId),
                    ]}
                    waiting={n}
                    active={onPage && search.thread === c.id}
                    link={
                      <Link
                        to="/e/$env/teammates/$id"
                        params={{ env, id: teammate.id }}
                        search={{ thread: c.id }}
                      />
                    }
                  />
                </ChatContextMenu>
              )
            })}
            {mine.length > listed.length && (
              <SidebarMenuSubItem>
                <SidebarMenuSubButton
                  size="sm"
                  className="pl-7 text-muted-foreground"
                  render={
                    <Link
                      to="/e/$env/teammates/$id"
                      params={{ env, id: teammate.id }}
                    />
                  }
                >
                  All {mine.length} threads
                </SidebarMenuSubButton>
              </SidebarMenuSubItem>
            )}
          </SidebarMenuSub>
        </li>
      )}
    </>
  )
}

/**
 * A thread under its teammate: its state, its title, and what it does where. The rest of its
 * props go to its item, such as a context menu's trigger.
 */
function ThreadRow({
  status,
  title,
  meta,
  waiting = 0,
  active,
  link,
  ...props
}: {
  status: ThreadStatus
  title: string
  meta: (string | false | undefined)[]
  waiting?: number
  active: boolean
  link: React.ReactElement
} & React.ComponentProps<"li">) {
  return (
    <SidebarMenuSubItem {...props}>
      <SidebarMenuSubButton
        isActive={active}
        render={link}
        className="h-auto items-start gap-2 py-1"
      >
        <span className="flex h-4 w-3 shrink-0 items-center justify-center">
          <ThreadDot status={status} />
        </span>
        <span className="grid min-w-0 flex-1 leading-tight">
          <span className="truncate">{title}</span>
          <span className="truncate text-caption text-muted-foreground">
            {meta.filter(Boolean).join(" · ")}
          </span>
        </span>
        <WaitingCount count={waiting} className="mt-px" />
      </SidebarMenuSubButton>
    </SidebarMenuSubItem>
  )
}

/** One of the user's own computers: its status as a dot, its name, and what waits on it. */
function ComputerRow({
  env,
  sandbox: sb,
  pending,
}: {
  env: string
  sandbox: Sandbox
  pending: Approval[]
}) {
  const matchRoute = useMatchRoute()
  const p = phase(sb)
  const waiting = pending.filter((a) => a.sandboxId === sb.id).length
  return (
    <SidebarMenuItem>
      <SidebarMenuButton
        tooltip={`${sb.name}: ${phaseDot(p).label}`}
        isActive={
          !!matchRoute({
            to: "/e/$env/sandboxes/$id",
            params: { env, id: sb.id },
            fuzzy: true,
          })
        }
        className="font-mono"
        render={<Link to="/e/$env/sandboxes/$id" params={{ env, id: sb.id }} />}
      >
        {/* The row's name starts with the computer's; its state is said after it. */}
        <span
          aria-hidden
          title={phaseDot(p).label}
          className="flex size-4 shrink-0 items-center justify-center"
        >
          <StatusDot {...phaseDot(p)} compact />
        </span>
        <span className="min-w-0 flex-1 truncate">
          {sb.name}
          <span className="sr-only">, {phaseDot(p).label}</span>
        </span>
        {sb.forkDefaults && <BaseMark />}
        <WaitingCount count={waiting} />
      </SidebarMenuButton>
    </SidebarMenuItem>
  )
}

function EnvironmentSwitcher({ env }: { env: string }) {
  const navigate = useNavigate()
  const queryClient = useQueryClient()
  const [adding, setAdding] = useState(false)
  const [name, setName] = useState("")
  const envs = $api.useQuery("get", "/api/environments")
  const current = envs.data?.find((e) => e.id === env)
  const create = $api.useMutation("post", "/api/environments", {
    onSuccess: (e) => {
      // The environment's page checks the cached list, so it has to be in it before going there.
      queryClient.setQueryData<(typeof e)[]>(
        ["get", "/api/environments"],
        (old) => (old?.some((o) => o.id === e.id) ? old : [...(old ?? []), e])
      )
      queryClient.invalidateQueries({ queryKey: ["get", "/api/environments"] })
      setAdding(false)
      setName("")
      navigate({ to: "/e/$env", params: { env: e.id } })
    },
    onError: (err) =>
      toast.error("Could not create the environment", {
        description: errorMessage(err),
      }),
  })
  const initials =
    current?.name
      .split(/\s+/)
      .filter(Boolean)
      .slice(0, 2)
      .map((word) => word.slice(0, 1))
      .join("")
      .toUpperCase() || "·"

  return (
    <SidebarMenu>
      <SidebarMenuItem>
        <DropdownMenu>
          <DropdownMenuTrigger
            render={
              <SidebarMenuButton
                size="sm"
                className="font-mono group-data-[collapsible=icon]:p-1.5! data-popup-open:bg-sidebar-accent"
              />
            }
          >
            <span className="flex size-5 shrink-0 items-center justify-center rounded-sm bg-primary font-mono text-meta font-bold text-primary-foreground">
              {initials}
            </span>
            <span className="min-w-0 flex-1 truncate font-medium">
              {current?.name ?? "…"}
            </span>
            <CaretUpDownIcon className="size-3.5 text-muted-foreground" />
          </DropdownMenuTrigger>
          <DropdownMenuContent className="min-w-56" align="start">
            <DropdownMenuGroup>
              <DropdownMenuLabel>Environments</DropdownMenuLabel>
              {envs.data?.map((e) => (
                <DropdownMenuItem
                  key={e.id}
                  onClick={() =>
                    navigate({ to: "/e/$env", params: { env: e.id } })
                  }
                >
                  {e.name}
                  {e.id === env && <CheckIcon className="ml-auto" />}
                </DropdownMenuItem>
              ))}
            </DropdownMenuGroup>
            <DropdownMenuSeparator />
            <DropdownMenuItem onClick={() => setAdding(true)}>
              <PlusIcon />
              New environment
            </DropdownMenuItem>
          </DropdownMenuContent>
        </DropdownMenu>
      </SidebarMenuItem>
      <Dialog open={adding} onOpenChange={setAdding}>
        <DialogContent>
          <form
            className="contents"
            onSubmit={(e) => {
              e.preventDefault()
              if (name.trim()) create.mutate({ body: { name: name.trim() } })
            }}
          >
            <DialogHeader>
              <DialogTitle>New environment</DialogTitle>
              <DialogDescription>
                Environments don't share computers, network rules, secrets or
                memory.
              </DialogDescription>
            </DialogHeader>
            <Field>
              <FieldLabel htmlFor="env-name">Name</FieldLabel>
              <Input
                id="env-name"
                autoFocus
                autoComplete="off"
                placeholder="Client project"
                value={name}
                onChange={(e) => setName(e.target.value)}
              />
            </Field>
            <DialogFooter>
              <Button type="submit" disabled={!name.trim() || create.isPending}>
                Create
              </Button>
            </DialogFooter>
          </form>
        </DialogContent>
      </Dialog>
    </SidebarMenu>
  )
}
