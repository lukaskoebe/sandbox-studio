import { useState } from "react"
import { Link, useMatchRoute, useNavigate } from "@tanstack/react-router"
import { useQueryClient } from "@tanstack/react-query"
import { toast } from "sonner"
import {
  CaretUpDownIcon,
  CheckIcon,
  CubeIcon,
  GearIcon,
  PlusIcon,
  TrayIcon,
  WarningIcon,
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
import { teammateApprovals } from "@/components/teammate/home"
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
import type { Chat } from "@/lib/chats"
import { programName } from "@/lib/terminals"

/** The section labels, in the style's label type; the size replaces SidebarGroupLabel's text-xs. */
const groupLabel = "h-6 section-label text-(length:--label-size)"

/**
 * The environment's places (docs/design/teammate-computer.md): Home, with what waits for you;
 * the teammates, each with one word for what it is doing or what waits; and your computers, the
 * sandboxes without a teammate. Threads and terminals are on the pages, not in here.
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
  const pending = (approvals.data ?? []).filter((a) => a.environmentId === env)
  const matchRoute = useMatchRoute()
  const settingsOpen = !!matchRoute({
    to: "/e/$env/settings",
    params: { env },
    fuzzy: true,
  })
  const all = sandboxes.data ?? []
  // A sandbox whose teammate is gone counts as without one; until the teammates load, only
  // a sandbox that never had one does.
  const unowned = all.filter(
    (sb) =>
      !sb.teammateId ||
      (teammates.data && !teammates.data.some((p) => p.id === sb.teammateId))
  )

  return (
    <Sidebar collapsible="icon">
      <SidebarHeader className="h-(--wb-header) justify-center border-b border-sidebar-border px-2 py-0">
        <EnvironmentSwitcher env={env} />
      </SidebarHeader>
      <SidebarContent className="gap-0">
        <SidebarGroup className="py-2">
          <SidebarMenu>
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
              {teammates.data?.map((p) => (
                <TeammateRow
                  key={p.id}
                  env={env}
                  teammate={p}
                  computers={all.filter((sb) => sb.teammateId === p.id)}
                  chats={chats.data ?? []}
                  pending={pending}
                />
              ))}
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
                  hint="A sandbox of your own, without a teammate"
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
              {unowned.map((sb) => (
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
        <div className="flex items-center justify-between gap-2 px-1 group-data-[collapsible=icon]:hidden">
          <p className="min-w-0 truncate text-meta text-muted-foreground">
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
 * it is doing or why it can't (working, no computer, stopped). Nothing while it is idle.
 */
function TeammateRow({
  env,
  teammate,
  computers,
  chats,
  pending,
}: {
  env: string
  teammate: Teammate
  computers: Sandbox[]
  chats: Chat[]
  pending: Approval[]
}) {
  const matchRoute = useMatchRoute()
  const waiting = teammateApprovals(
    pending,
    env,
    teammate.id,
    new Set(computers.map((sb) => sb.id))
  ).length
  // A harness printing in one of its terminals is work too; another program there isn't.
  const terminal = useActivity(env)(teammate.id).terminal
  const working =
    chats.some((c) => c.teammateId === teammate.id && c.state === "running") ||
    terminal?.state === "working"
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
  return (
    <SidebarMenuItem>
      <SidebarMenuButton
        tooltip={teammate.name}
        isActive={
          !!matchRoute({
            to: "/e/$env/teammates/$id",
            params: { env, id: teammate.id },
            fuzzy: true,
          })
        }
        render={
          <Link to="/e/$env/teammates/$id" params={{ env, id: teammate.id }} />
        }
      >
        <TeammateAvatar
          name={teammate.name}
          avatar={teammate.avatar}
          className="size-5 text-[0.5625rem]"
        />
        <span className="min-w-0 flex-1 truncate">{teammate.name}</span>
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
      </SidebarMenuButton>
    </SidebarMenuItem>
  )
}

/** A computer without a teammate: its name, what waits on it, and its status as a dot. */
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
        <CubeIcon />
        <span className="min-w-0 flex-1 truncate">{sb.name}</span>
        {waiting > 0 ? (
          <WaitingCount count={waiting} />
        ) : (
          <span title={phaseDot(p).label} className="flex px-1">
            <StatusDot {...phaseDot(p)} compact />
          </span>
        )}
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
