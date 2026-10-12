import { useState } from "react"
import { Link, useMatchRoute, useNavigate } from "@tanstack/react-router"
import { useQueryClient } from "@tanstack/react-query"
import { toast } from "sonner"
import {
  BrainIcon,
  CaretUpDownIcon,
  ChatsCircleIcon,
  CheckIcon,
  GearIcon,
  GlobeIcon,
  KeyIcon,
  PlusIcon,
  SquaresFourIcon,
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
import { PresenceDot, TeammateAvatar } from "@/components/teammate-avatar"
import { TeammateDialog } from "@/components/teammate-dialog"
import { phase, phaseDot } from "@/components/status-badge"
import { StatusDot } from "@/components/status-dot"
import { WaitingCount } from "@/components/waiting-count"
import { usePendingApprovals } from "@/hooks/use-pending-approvals"
import { usePresence } from "@/hooks/use-teammate-presence"
import { $api, errorMessage } from "@/lib/api/client"

/** The section labels: small, mono and uppercase, as the Workbench's lists title theirs. */
const groupLabel = "h-6 font-mono text-[0.625rem] tracking-wider uppercase"

/**
 * The environment's navigation: Home, Chats, Network, Secrets, Memory and Settings, then the
 * teammates (each with its presence) and the sandboxes (each with its status and the approvals
 * waiting on it).
 */
export function AppSidebar({ env }: { env: string }) {
  const [creating, setCreating] = useState(false)
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
  const approvals = usePendingApprovals()
  const presence = usePresence(env)
  const waiting = new Map<string, number>()
  for (const a of approvals.data ?? []) {
    if (a.sandboxId)
      waiting.set(a.sandboxId, (waiting.get(a.sandboxId) ?? 0) + 1)
  }
  const canAddTeammate = (providers.data?.length ?? 0) > 0
  const matchRoute = useMatchRoute()

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
                <SquaresFourIcon />
                <span>Home</span>
              </SidebarMenuButton>
            </SidebarMenuItem>
            <SidebarMenuItem>
              <SidebarMenuButton
                size="sm"
                tooltip="Chats"
                isActive={
                  !!matchRoute({
                    to: "/e/$env/chats",
                    params: { env },
                    fuzzy: true,
                  })
                }
                className="[&_svg]:text-(--acc-chat)"
                render={<Link to="/e/$env/chats" params={{ env }} />}
              >
                <ChatsCircleIcon />
                <span>Chats</span>
              </SidebarMenuButton>
            </SidebarMenuItem>
            <SidebarMenuItem>
              <SidebarMenuButton
                size="sm"
                tooltip="Network"
                isActive={
                  !!matchRoute({
                    to: "/e/$env/network",
                    params: { env },
                    fuzzy: true,
                  })
                }
                className="[&_svg]:text-(--acc-network)"
                render={<Link to="/e/$env/network" params={{ env }} />}
              >
                <GlobeIcon />
                <span>Network</span>
              </SidebarMenuButton>
            </SidebarMenuItem>
            <SidebarMenuItem>
              <SidebarMenuButton
                size="sm"
                tooltip="Secrets"
                isActive={
                  !!matchRoute({ to: "/e/$env/secrets", params: { env } })
                }
                render={<Link to="/e/$env/secrets" params={{ env }} />}
              >
                <KeyIcon />
                <span>Secrets</span>
              </SidebarMenuButton>
            </SidebarMenuItem>
            <SidebarMenuItem>
              <SidebarMenuButton
                size="sm"
                tooltip="Memory"
                isActive={
                  !!matchRoute({ to: "/e/$env/memory", params: { env } })
                }
                className="[&_svg]:text-(--acc-memory)"
                render={<Link to="/e/$env/memory" params={{ env }} />}
              >
                <BrainIcon />
                <span>Memory</span>
              </SidebarMenuButton>
            </SidebarMenuItem>
            <SidebarMenuItem>
              <SidebarMenuButton
                size="sm"
                tooltip="Settings"
                isActive={
                  !!matchRoute({
                    to: "/e/$env/settings",
                    params: { env },
                    fuzzy: true,
                  })
                }
                render={<Link to="/e/$env/settings" params={{ env }} />}
              >
                <GearIcon />
                <span>Settings</span>
                {updates.data?.available && (
                  <span className="ml-auto text-[0.625rem] text-primary">
                    Update available
                  </span>
                )}
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
            title={canAddTeammate ? "New teammate" : "Add a provider first"}
            disabled={!canAddTeammate}
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
                <SidebarMenuItem key={p.id}>
                  <SidebarMenuButton
                    size="sm"
                    tooltip={p.name}
                    isActive={
                      !!matchRoute({
                        to: "/e/$env/teammates/$id",
                        params: { env, id: p.id },
                        fuzzy: true,
                      })
                    }
                    render={
                      <Link
                        to="/e/$env/teammates/$id"
                        params={{ env, id: p.id }}
                      />
                    }
                  >
                    <TeammateAvatar
                      name={p.name}
                      className="size-4 text-[0.5rem]"
                    />
                    <span className="truncate">{p.name}</span>
                    <PresenceDot
                      presence={presence(p.id)}
                      className="ml-auto"
                    />
                  </SidebarMenuButton>
                </SidebarMenuItem>
              ))}
            </SidebarMenu>
          </SidebarGroupContent>
        </SidebarGroup>
        <SidebarGroup className="py-1">
          <SidebarGroupLabel className={groupLabel}>
            Sandboxes
          </SidebarGroupLabel>
          <SidebarGroupAction
            className="top-1.5"
            title="New sandbox"
            onClick={() => setCreating(true)}
          >
            <PlusIcon />
          </SidebarGroupAction>
          <SidebarGroupContent>
            <SidebarMenu>
              {sandboxes.isPending &&
                Array.from({ length: 2 }, (_, i) => (
                  <SidebarMenuItem key={i}>
                    <SidebarMenuSkeleton showIcon />
                  </SidebarMenuItem>
                ))}
              {sandboxes.data?.map((sb) => (
                <SidebarMenuItem key={sb.id}>
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
                    render={
                      <Link
                        to="/e/$env/sandboxes/$id"
                        params={{ env, id: sb.id }}
                      />
                    }
                  >
                    <StatusDot
                      {...phaseDot(phase(sb))}
                      compact
                      className="shrink-0"
                    />
                    <span className="min-w-0 flex-1 truncate">{sb.name}</span>
                    <WaitingCount count={waiting.get(sb.id) ?? 0} />
                  </SidebarMenuButton>
                </SidebarMenuItem>
              ))}
            </SidebarMenu>
          </SidebarGroupContent>
        </SidebarGroup>
      </SidebarContent>
      <SidebarFooter className="border-t border-sidebar-border p-2">
        <div className="flex items-center justify-between gap-2 px-1 group-data-[collapsible=icon]:hidden">
          <p className="text-[0.625rem] text-muted-foreground">
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
      <TeammateDialog
        env={env}
        providers={providers.data ?? []}
        open={addingTeammate}
        onOpenChange={setAddingTeammate}
      />
    </Sidebar>
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
