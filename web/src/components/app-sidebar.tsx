import { useState } from "react"
import { Link, useMatchRoute, useNavigate } from "@tanstack/react-router"
import { useQueryClient } from "@tanstack/react-query"
import { toast } from "sonner"
import {
  BrainIcon,
  CaretUpDownIcon,
  CheckIcon,
  CubeIcon,
  GearIcon,
  GitBranchIcon,
  GlobeIcon,
  HammerIcon,
  KeyIcon,
  PlugIcon,
  PlusIcon,
  RobotIcon,
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
import { PersonaAvatar } from "@/components/persona-avatar"
import { StatusDot } from "@/components/status-badge"
import { $api, errorMessage } from "@/lib/api/client"

export function AppSidebar({ env }: { env: string }) {
  const [creating, setCreating] = useState(false)
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
  const personas = $api.useQuery("get", "/api/environments/{env}/personas", {
    params: { path: { env } },
  })
  const owners = new Map(personas.data?.map((p) => [p.id, p.name]))
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
                tooltip="Overview"
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
                <span>Overview</span>
              </SidebarMenuButton>
            </SidebarMenuItem>
            <SidebarMenuItem>
              <SidebarMenuButton
                tooltip="Builds"
                isActive={
                  !!matchRoute({ to: "/e/$env/builds", params: { env } })
                }
                render={<Link to="/e/$env/builds" params={{ env }} />}
              >
                <HammerIcon />
                <span>Builds</span>
              </SidebarMenuButton>
            </SidebarMenuItem>
            <SidebarMenuItem>
              <SidebarMenuButton
                tooltip="Network"
                isActive={
                  !!matchRoute({ to: "/e/$env/network", params: { env } })
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
                tooltip="Git"
                isActive={!!matchRoute({ to: "/e/$env/git", params: { env } })}
                render={<Link to="/e/$env/git" params={{ env }} />}
              >
                <GitBranchIcon />
                <span>Git</span>
              </SidebarMenuButton>
            </SidebarMenuItem>
            <SidebarMenuItem>
              <SidebarMenuButton
                tooltip="Personas"
                isActive={
                  !!matchRoute({ to: "/e/$env/personas", params: { env } })
                }
                render={<Link to="/e/$env/personas" params={{ env }} />}
              >
                <RobotIcon />
                <span>Personas</span>
              </SidebarMenuButton>
            </SidebarMenuItem>
            <SidebarMenuItem>
              <SidebarMenuButton
                tooltip="Providers"
                isActive={
                  !!matchRoute({ to: "/e/$env/providers", params: { env } })
                }
                render={<Link to="/e/$env/providers" params={{ env }} />}
              >
                <PlugIcon />
                <span>Providers</span>
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
          </SidebarMenu>
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
                    {sb.personaId && owners.has(sb.personaId) ? (
                      <PersonaAvatar
                        name={owners.get(sb.personaId) ?? ""}
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
        <SidebarMenu>
          <SidebarMenuItem>
            <SidebarMenuButton
              tooltip="Settings"
              isActive={
                !!matchRoute({ to: "/e/$env/settings", params: { env } })
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
