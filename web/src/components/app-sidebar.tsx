import { useId, useState } from "react"
import {
  Link,
  useMatchRoute,
  useNavigate,
  useSearch,
} from "@tanstack/react-router"
import { useQueryClient } from "@tanstack/react-query"
import { toast } from "sonner"
import {
  BrainIcon,
  CaretDownIcon,
  CaretRightIcon,
  CaretUpDownIcon,
  ChatCircleIcon,
  ChatsCircleIcon,
  CheckIcon,
  GearIcon,
  GitBranchIcon,
  GlobeIcon,
  KeyIcon,
  PlusIcon,
  RobotIcon,
  TrayIcon,
  TerminalWindowIcon,
} from "@phosphor-icons/react"
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
  SidebarRail,
} from "@/components/ui/sidebar"
import { SettingsLink } from "@/components/settings/link"
import { ThemeToggle } from "@/components/theme-toggle"
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
import { TeammateAvatar } from "@/components/teammate-avatar"
import { TeammateDialog } from "@/components/teammate-dialog"
import { phase, phaseDot } from "@/components/status-badge"
import { StatusDot } from "@/components/status-dot"
import { WaitingCount } from "@/components/waiting-count"
import { isGitApproval } from "@/components/git-review"
import { usePendingApprovals } from "@/hooks/use-pending-approvals"
import { presenceLabels, usePresence } from "@/hooks/use-teammate-presence"
import {
  $api,
  errorMessage,
  type Approval,
  type Sandbox,
} from "@/lib/api/client"
import { hasNews, useChatsSeen } from "@/lib/chat-seen"
import type { Chat } from "@/lib/chats"
import { harnessLabels } from "@/lib/teammates"
import { cn } from "@/lib/utils"

/** The section labels: small, mono and uppercase, as the Workbench's lists title theirs. */
const groupLabel = "h-6 font-mono text-[0.625rem] tracking-wider uppercase"

/** Chats a sandbox lists in the sidebar; the rest are on the teammate's page. */
const sidebarChats = 5

type Teammate = { id: string; name: string; avatar?: string }

/** What the sidebar knows about the environment's sessions, shared by its rows. */
type Sessions = {
  env: string
  chats: Chat[]
  reviews: Approval[]
  waiting: Map<string, number>
}

/**
 * The environment's navigation as one tree: environment → teammate → sandbox → session. A
 * teammate with one sandbox lists its sessions (chats, terminals, reviews) right under it; one
 * with more lists its sandboxes first. Sandboxes without a teammate come last. Home, with what
 * waits for you, and New chat are on top; Settings at the bottom.
 */
export function AppSidebar({ env }: { env: string }) {
  const [creating, setCreating] = useState(false)
  const [importing, setImporting] = useState(false)
  const [addingTeammate, setAddingTeammate] = useState(false)
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
  const approvals = usePendingApprovals()
  const waiting = new Map<string, number>()
  const reviews: Approval[] = []
  let envWaiting = 0
  for (const a of approvals.data ?? []) {
    if (a.environmentId !== env) continue
    envWaiting++
    if (a.sandboxId)
      waiting.set(a.sandboxId, (waiting.get(a.sandboxId) ?? 0) + 1)
    if (isGitApproval(a)) reviews.push(a)
  }
  const sessions: Sessions = {
    env,
    chats: chats.data ?? [],
    reviews,
    waiting,
  }
  const matchRoute = useMatchRoute()
  const settingsOpen = useSearch({ strict: false }).settings !== undefined
  const all = sandboxes.data ?? []
  const unowned = all.filter(
    (sb) =>
      !sb.teammateId || !teammates.data?.some((p) => p.id === sb.teammateId)
  )

  return (
    <Sidebar collapsible="icon">
      <SidebarHeader className="h-(--wb-header) justify-center border-b border-sidebar-border px-2 py-0">
        <EnvironmentSwitcher env={env} />
      </SidebarHeader>
      <SidebarContent className="gap-0">
        <SidebarGroup className="gap-1 py-2">
          <NewChatButton
            env={env}
            onAddTeammate={() => setAddingTeammate(true)}
            onCreateSandbox={() => setCreating(true)}
            onImport={() => setImporting(true)}
          />
          <SidebarMenu>
            <SidebarMenuItem>
              <SidebarMenuButton
                size="sm"
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
                <WaitingCount count={envWaiting} />
              </SidebarMenuButton>
            </SidebarMenuItem>
          </SidebarMenu>
        </SidebarGroup>
        <SidebarGroup className="py-1">
          <SidebarGroupLabel
            className={groupLabel}
            render={<Link to="/e/$env/teammates" params={{ env }} />}
          >
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
              {teammates.isPending &&
                Array.from({ length: 2 }, (_, i) => (
                  <SidebarMenuItem key={i}>
                    <SidebarMenuSkeleton showIcon />
                  </SidebarMenuItem>
                ))}
              {teammates.data?.map((p) => (
                <TeammateNode
                  key={p.id}
                  teammate={p}
                  sandboxes={all.filter((sb) => sb.teammateId === p.id)}
                  sessions={sessions}
                />
              ))}
            </SidebarMenu>
          </SidebarGroupContent>
        </SidebarGroup>
        {unowned.length > 0 && (
          <SidebarGroup className="py-1">
            <SidebarGroupLabel className={groupLabel}>
              No teammate
            </SidebarGroupLabel>
            <SidebarGroupAction
              className="top-1.5"
              title="New sandbox without a teammate"
              onClick={() => setCreating(true)}
            >
              <PlusIcon />
            </SidebarGroupAction>
            <SidebarGroupContent>
              <SidebarMenu>
                {unowned.map((sb) => (
                  <SidebarMenuItem key={sb.id}>
                    <SandboxRow sb={sb} sessions={sessions} />
                    <SessionList sb={sb} sessions={sessions} />
                  </SidebarMenuItem>
                ))}
              </SidebarMenu>
            </SidebarGroupContent>
          </SidebarGroup>
        )}
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
              {updates.data?.available ? (
                <span className="ml-auto text-[0.625rem] text-primary">
                  Update available
                </span>
              ) : (
                <span className="ml-auto truncate text-[0.625rem] text-muted-foreground">
                  network, git, keys
                </span>
              )}
            </SidebarMenuButton>
          </SidebarMenuItem>
        </SidebarMenu>
        <MorePages env={env} />
        <div className="flex items-center justify-between gap-2 px-1 group-data-[collapsible=icon]:hidden">
          <p className="min-w-0 truncate text-[0.625rem] text-muted-foreground">
            Sandbox Studio{" "}
            <span className="font-mono">{health.data?.version}</span>
          </p>
          <ThemeToggle />
        </div>
      </SidebarFooter>
      <SidebarRail />
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
    </Sidebar>
  )
}

/** New chat, with the other ways to start beside it: a teammate, a bare sandbox, an import. */
function NewChatButton({
  env,
  onAddTeammate,
  onCreateSandbox,
  onImport,
}: {
  env: string
  onAddTeammate: () => void
  onCreateSandbox: () => void
  onImport: () => void
}) {
  const navigate = useNavigate()
  return (
    <div className="flex rounded-md bg-primary text-primary-foreground group-data-[collapsible=icon]:hidden">
      <Link
        to="/e/$env/chats/new"
        params={{ env }}
        className="flex h-8 min-w-0 flex-1 items-center gap-2 rounded-l-md px-2.5 text-sm font-medium hover:bg-primary/90"
      >
        <PlusIcon className="size-4" />
        New chat
      </Link>
      <DropdownMenu>
        <DropdownMenuTrigger
          aria-label="More to create"
          className="flex w-8 items-center justify-center rounded-r-md border-l border-primary-foreground/30 hover:bg-primary/90"
        >
          <CaretDownIcon className="size-3.5" />
        </DropdownMenuTrigger>
        <DropdownMenuContent align="start" className="min-w-60">
          <DropdownMenuItem
            onClick={() =>
              navigate({ to: "/e/$env/chats/new", params: { env } })
            }
          >
            <MenuText
              title="New chat"
              hint="With a teammate, on its computer"
            />
          </DropdownMenuItem>
          <DropdownMenuItem onClick={onAddTeammate}>
            <MenuText
              title="Add a teammate"
              hint="Comes with its own sandbox"
            />
          </DropdownMenuItem>
          <DropdownMenuItem onClick={onCreateSandbox}>
            <MenuText
              title="Sandbox without a teammate"
              hint="A plain VM with terminals"
            />
          </DropdownMenuItem>
          <DropdownMenuItem onClick={onImport}>
            <MenuText title="Import a sandbox" hint="From an export file" />
          </DropdownMenuItem>
        </DropdownMenuContent>
      </DropdownMenu>
    </div>
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

/** Which teammates' rows are open, per browser; one never toggled opens while it needs you. */
const openKey = "studio.sidebar.open"

function loadOpen(): Record<string, boolean> {
  try {
    const parsed: unknown = JSON.parse(localStorage.getItem(openKey) ?? "{}")
    return parsed && typeof parsed === "object"
      ? (parsed as Record<string, boolean>)
      : {}
  } catch {
    return {}
  }
}

function useOpen(id: string, fallback: boolean) {
  const [open, setOpen] = useState<boolean | undefined>(() => loadOpen()[id])
  const toggle = () => {
    const next = !(open ?? fallback)
    setOpen(next)
    try {
      localStorage.setItem(
        openKey,
        JSON.stringify({ ...loadOpen(), [id]: next })
      )
    } catch {
      // Kept for this page.
    }
  }
  return [open ?? fallback, toggle] as const
}

/**
 * A teammate, with its one sandbox's sessions under it, or its sandboxes when it has more.
 * The line under its name says where it works and what it is doing.
 */
function TeammateNode({
  teammate,
  sandboxes,
  sessions,
}: {
  teammate: Teammate
  sandboxes: Sandbox[]
  sessions: Sessions
}) {
  const { env } = sessions
  const presence = usePresence(env)(teammate.id)
  const matchRoute = useMatchRoute()
  const ids = new Set(sandboxes.map((sb) => sb.id))
  const waiting = sandboxes.reduce(
    (n, sb) => n + (sessions.waiting.get(sb.id) ?? 0),
    0
  )
  const working = sessions.chats.some(
    (c) => ids.has(c.sandboxId) && c.state === "running"
  )
  const here = sandboxes.some(
    (sb) =>
      !!matchRoute({
        to: "/e/$env/sandboxes/$id",
        params: { env, id: sb.id },
        fuzzy: true,
      })
  )
  const active = !!matchRoute({
    to: "/e/$env/teammates/$id",
    params: { env, id: teammate.id },
    fuzzy: true,
  })
  const [open, toggle] = useOpen(teammate.id, waiting > 0 || working || here)
  const statusId = useId()
  const status =
    waiting > 0
      ? "needs you"
      : working
        ? "working"
        : sandboxes.length === 1 && phase(sandboxes[0]) !== "ready"
          ? phaseDot(phase(sandboxes[0])).label.toLowerCase()
          : presenceLabels[presence].toLowerCase()
  const where =
    sandboxes.length === 0
      ? "no sandbox"
      : sandboxes.length === 1
        ? sandboxes[0].name
        : `${sandboxes.length} sandboxes`

  return (
    <SidebarMenuItem>
      <div className="flex items-center">
        <button
          type="button"
          aria-label={`${open ? "Hide" : "Show"} ${teammate.name}'s sessions`}
          aria-expanded={open}
          onClick={toggle}
          className="flex size-5 shrink-0 items-center justify-center rounded-sm text-muted-foreground group-data-[collapsible=icon]:hidden hover:bg-sidebar-accent"
        >
          <CaretRightIcon
            className={cn("size-3 transition-transform", open && "rotate-90")}
          />
        </button>
        <SidebarMenuButton
          tooltip={teammate.name}
          isActive={active}
          className="h-auto min-h-9 py-1"
          render={
            <Link
              to="/e/$env/teammates/$id"
              params={{ env, id: teammate.id }}
              aria-label={teammate.name}
              aria-describedby={statusId}
            />
          }
        >
          <TeammateAvatar
            name={teammate.name}
            avatar={teammate.avatar}
            className="size-5 text-[0.5625rem]"
          />
          <span className="flex min-w-0 flex-1 flex-col leading-tight">
            <span className="truncate font-medium">{teammate.name}</span>
            <span
              id={statusId}
              className="truncate text-[0.6875rem] text-muted-foreground"
            >
              <span className={cn(sandboxes.length === 1 && "font-mono")}>
                {where}
              </span>
              {" · "}
              <span className={cn(waiting > 0 && "text-(--st-waiting-fg)")}>
                {status}
              </span>
            </span>
          </span>
          <WaitingCount count={waiting} />
        </SidebarMenuButton>
      </div>
      {open &&
        (sandboxes.length === 1 ? (
          <SessionList sb={sandboxes[0]} sessions={sessions} />
        ) : (
          sandboxes.length > 1 && (
            <SidebarMenu className="pl-5 group-data-[collapsible=icon]:hidden">
              {sandboxes.map((sb) => (
                <SidebarMenuItem key={sb.id}>
                  <SandboxRow sb={sb} sessions={sessions} />
                  <SessionList sb={sb} sessions={sessions} />
                </SidebarMenuItem>
              ))}
            </SidebarMenu>
          )
        ))}
    </SidebarMenuItem>
  )
}

/** A sandbox: its status, its name and what waits on it, opening its workspace. */
function SandboxRow({ sb, sessions }: { sb: Sandbox; sessions: Sessions }) {
  const { env } = sessions
  const matchRoute = useMatchRoute()
  const p = phase(sb)
  return (
    <SidebarMenuButton
      size="sm"
      tooltip={sb.name}
      isActive={
        !!matchRoute({
          to: "/e/$env/sandboxes/$id",
          params: { env, id: sb.id },
        })
      }
      className="font-mono"
      render={<Link to="/e/$env/sandboxes/$id" params={{ env, id: sb.id }} />}
    >
      <StatusDot {...phaseDot(p)} compact className="shrink-0" />
      <span className="min-w-0 flex-1 truncate">{sb.name}</span>
      {p !== "ready" && (
        <span className="font-sans text-[0.625rem] text-muted-foreground">
          {phaseDot(p).label.toLowerCase()}
        </span>
      )}
      <WaitingCount count={sessions.waiting.get(sb.id) ?? 0} />
    </SidebarMenuButton>
  )
}

/**
 * A sandbox's sessions, each opening in its workspace: its latest chats, its terminals (the
 * guest's tmux sessions, a shell or an agent's) and the pushes waiting for review.
 */
function SessionList({ sb, sessions }: { sb: Sandbox; sessions: Sessions }) {
  const { env } = sessions
  const running = phase(sb) === "ready"
  const terminals = $api.useQuery(
    "get",
    "/api/environments/{env}/sandboxes/{id}/terminals",
    { params: { path: { env, id: sb.id } } },
    { refetchInterval: 5000, enabled: running }
  )
  const seen = useChatsSeen()
  const matchRoute = useMatchRoute()
  const search = useSearch({ strict: false })
  const inWorkspace = !!matchRoute({
    to: "/e/$env/sandboxes/$id/work",
    params: { env, id: sb.id },
  })
  const chats = sessions.chats
    .filter(
      (c) =>
        c.sandboxId === sb.id && (c.state === "idle" || c.state === "running")
    )
    .slice(0, sidebarChats)
  const list = running
    ? [...(terminals.data ?? [])].sort(
        // tmux counts in seconds, so sessions made in the same one go main first, then by name
        (a, b) =>
          a.created - b.created ||
          Number(b.name === "main") - Number(a.name === "main") ||
          a.name.localeCompare(b.name, undefined, { numeric: true })
      )
    : []
  const reviews = sessions.reviews.filter((a) => a.sandboxId === sb.id)
  if (!chats.length && !list.length && !reviews.length) return null
  return (
    <SidebarMenuSub aria-label={`Sessions of ${sb.name}`}>
      {chats.map((c) => (
        <SidebarMenuSubItem key={c.id}>
          <SidebarMenuSubButton
            size="sm"
            isActive={inWorkspace && search.chat === c.id}
            title={`Chat: ${c.title || "Untitled"}`}
            render={
              <Link
                to="/e/$env/sandboxes/$id/work"
                params={{ env, id: sb.id }}
                search={{ chat: c.id }}
              />
            }
          >
            <ChatCircleIcon className="text-(--acc-chat)" />
            <span className="min-w-0 flex-1 truncate">
              {c.title || "Untitled"}
            </span>
            {c.state === "running" ? (
              <span className="text-[0.625rem] text-muted-foreground">
                working
              </span>
            ) : (
              hasNews(c, seen) && (
                <span
                  aria-label="new"
                  className="size-1.5 shrink-0 rounded-full bg-(--st-running)"
                />
              )
            )}
          </SidebarMenuSubButton>
        </SidebarMenuSubItem>
      ))}
      {list.map((t) => {
        const harness =
          t.harness && t.harness in harnessLabels
            ? harnessLabels[t.harness as keyof typeof harnessLabels]
            : t.harness
        return (
          <SidebarMenuSubItem key={t.name}>
            <SidebarMenuSubButton
              size="sm"
              isActive={inWorkspace && search.terminal === t.name}
              title={harness ? `${t.name}: ${harness}` : `Terminal ${t.name}`}
              render={
                <Link
                  to="/e/$env/sandboxes/$id/work"
                  params={{ env, id: sb.id }}
                  search={{ terminal: t.name }}
                />
              }
            >
              {t.harness ? <RobotIcon /> : <TerminalWindowIcon />}
              <span className="font-mono">{t.name}</span>
            </SidebarMenuSubButton>
          </SidebarMenuSubItem>
        )
      })}
      {reviews.map((a) => (
        <SidebarMenuSubItem key={a.id}>
          <SidebarMenuSubButton
            size="sm"
            isActive={
              !!matchRoute({
                to: "/e/$env/git/reviews/$id",
                params: { env, id: a.id },
              })
            }
            title={`Review: ${a.subject}`}
            render={
              <Link to="/e/$env/git/reviews/$id" params={{ env, id: a.id }} />
            }
          >
            <GitBranchIcon className="text-(--acc-git)" />
            <span className="min-w-0 flex-1 truncate font-mono">
              {a.git?.review?.branch ?? a.git?.pullRequest?.head ?? a.subject}
            </span>
            <span className="text-[0.625rem] text-(--st-waiting-fg)">
              review
            </span>
          </SidebarMenuSubButton>
        </SidebarMenuSubItem>
      ))}
    </SidebarMenuSub>
  )
}

/**
 * The pages that move into Settings with the redesign, until they do: all chats, git,
 * network, secrets and memory, as a row of icons.
 */
function MorePages({ env }: { env: string }) {
  const pages = [
    { to: "/e/$env/chats", label: "All chats", icon: ChatsCircleIcon },
    { to: "/e/$env/git", label: "Git", icon: GitBranchIcon },
    { to: "/e/$env/network", label: "Network", icon: GlobeIcon },
    { to: "/e/$env/secrets", label: "Secrets", icon: KeyIcon },
    { to: "/e/$env/memory", label: "Memory", icon: BrainIcon },
  ] as const
  return (
    <nav
      aria-label="More pages"
      className="flex items-center gap-0.5 px-1 group-data-[collapsible=icon]:hidden"
    >
      {pages.map(({ to, label, icon: Icon }) => (
        <Link
          key={to}
          to={to}
          params={{ env }}
          title={label}
          aria-label={label}
          className="flex size-7 items-center justify-center rounded-md text-muted-foreground hover:bg-sidebar-accent hover:text-sidebar-accent-foreground data-[status=active]:bg-sidebar-accent data-[status=active]:text-sidebar-accent-foreground"
        >
          <Icon className="size-4" />
        </Link>
      ))}
    </nav>
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
                className="font-mono data-popup-open:bg-sidebar-accent"
              />
            }
          >
            <span className="flex size-5 shrink-0 items-center justify-center rounded-[4px] bg-primary font-mono text-[0.625rem] font-bold text-primary-foreground">
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
                Environments don't share sandboxes, network rules, secrets or
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
