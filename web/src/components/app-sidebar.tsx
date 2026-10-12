import { useState } from "react"
import { Link, useMatchRoute, useNavigate } from "@tanstack/react-router"
import { useQueryClient } from "@tanstack/react-query"
import { toast } from "sonner"
import {
  BrainIcon,
  CaretUpDownIcon,
  ChatsCircleIcon,
  CheckIcon,
  CubeIcon,
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
import { StatusDot } from "@/components/status-badge"
import { usePresence } from "@/hooks/use-teammate-presence"
import { $api, errorMessage } from "@/lib/api/client"

/**
 * The environment's navigation: Home, Chats, Network, Secrets, Memory and Settings, then the
 * teammates (each with its presence) and the sandboxes.
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
  const presence = usePresence(env)
  const owners = new Map(teammates.data?.map((p) => [p.id, p.name]))
  const canAddTeammate = (providers.data?.length ?? 0) > 0
  const matchRoute = useMatchRoute()

  return (
    <Sidebar collapsible="icon">
      <SidebarHeader>
        <EnvironmentSwitcher env={env} />
      </SidebarHeader>
      <SidebarContent>
        <SidebarGroup>
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
                <SquaresFourIcon />
                <span>Home</span>
              </SidebarMenuButton>
            </SidebarMenuItem>
            <SidebarMenuItem>
              <SidebarMenuButton
                tooltip="Chats"
                isActive={
                  !!matchRoute({
                    to: "/e/$env/chats",
                    params: { env },
                    fuzzy: true,
                  })
                }
                render={<Link to="/e/$env/chats" params={{ env }} />}
              >
                <ChatsCircleIcon />
                <span>Chats</span>
              </SidebarMenuButton>
            </SidebarMenuItem>
            <SidebarMenuItem>
              <SidebarMenuButton
                tooltip="Network"
                isActive={
                  !!matchRoute({
                    to: "/e/$env/network",
                    params: { env },
                    fuzzy: true,
                  })
                }
                render={<Link to="/e/$env/network" params={{ env }} />}
              >
                <GlobeIcon />
                <span>Network</span>
              </SidebarMenuButton>
            </SidebarMenuItem>
            <SidebarMenuItem>
              <SidebarMenuButton
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
                tooltip="Memory"
                isActive={
                  !!matchRoute({ to: "/e/$env/memory", params: { env } })
                }
                render={<Link to="/e/$env/memory" params={{ env }} />}
              >
                <BrainIcon />
                <span>Memory</span>
              </SidebarMenuButton>
            </SidebarMenuItem>
            <SidebarMenuItem>
              <SidebarMenuButton
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
        <SidebarGroup>
          <SidebarGroupLabel
            render={<Link to="/e/$env/teammates" params={{ env }} />}
          >
            Teammates
          </SidebarGroupLabel>
          <SidebarGroupAction
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
        <SidebarGroup>
          <SidebarGroupLabel>Sandboxes</SidebarGroupLabel>
          <SidebarGroupAction
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
                    tooltip={sb.name}
                    isActive={
                      !!matchRoute({
                        to: "/e/$env/sandboxes/$id",
                        params: { env, id: sb.id },
                      })
                    }
                    render={
                      <Link
                        to="/e/$env/sandboxes/$id"
                        params={{ env, id: sb.id }}
                      />
                    }
                  >
                    {sb.teammateId && owners.has(sb.teammateId) ? (
                      <TeammateAvatar
                        name={owners.get(sb.teammateId) ?? ""}
                        className="size-4 text-[0.5rem]"
                      />
                    ) : (
                      <CubeIcon />
                    )}
                    <span className="truncate">{sb.name}</span>
                    <StatusDot sandbox={sb} className="ml-auto" />
                  </SidebarMenuButton>
                </SidebarMenuItem>
              ))}
            </SidebarMenu>
          </SidebarGroupContent>
        </SidebarGroup>
      </SidebarContent>
      <SidebarFooter>
        <p className="px-2 text-[0.625rem] text-muted-foreground group-data-[collapsible=icon]:hidden">
          Sandbox Studio {health.data?.version}
        </p>
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

  return (
    <SidebarMenu>
      <SidebarMenuItem>
        <DropdownMenu>
          <DropdownMenuTrigger
            render={
              <SidebarMenuButton
                size="lg"
                className="data-popup-open:bg-sidebar-accent"
              />
            }
          >
            <div className="flex aspect-square size-8 items-center justify-center rounded-md bg-primary text-sm font-semibold text-primary-foreground">
              {current?.name.slice(0, 1).toUpperCase() ?? "·"}
            </div>
            <div className="grid flex-1 text-left leading-tight">
              <span className="truncate font-medium">
                {current?.name ?? "…"}
              </span>
              <span className="truncate text-[0.625rem] text-muted-foreground">
                Environment
              </span>
            </div>
            <CaretUpDownIcon className="ml-auto" />
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
