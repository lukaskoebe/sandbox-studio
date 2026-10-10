import { useEffect, useState } from "react"
import { createFileRoute, useNavigate } from "@tanstack/react-router"
import { useQueryClient } from "@tanstack/react-query"
import { toast } from "sonner"
import {
  ArrowSquareOutIcon,
  BroadcastIcon,
  PlayIcon,
  PlusIcon,
  StopIcon,
  TrashIcon,
  WarningIcon,
  XIcon,
} from "@phosphor-icons/react"
import { Button } from "@/components/ui/button"
import {
  AlertDialog,
  AlertDialogAction,
  AlertDialogCancel,
  AlertDialogContent,
  AlertDialogDescription,
  AlertDialogFooter,
  AlertDialogHeader,
  AlertDialogTitle,
  AlertDialogTrigger,
} from "@/components/ui/alert-dialog"
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuGroup,
  DropdownMenuItem,
  DropdownMenuLabel,
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
import { CheckpointsSheet } from "@/components/checkpoints-sheet"
import { ConnectionsSheet } from "@/components/connections-sheet"
import { ExportSandboxButton } from "@/components/export-sandbox-button"
import { ForkSandboxDialog } from "@/components/fork-sandbox-dialog"
import { PageHeader } from "@/components/page-header"
import { SandboxOwner } from "@/components/persona-avatar"
import { RebaseSandboxDialog } from "@/components/rebase-sandbox-dialog"
import { StatusBadge, phase } from "@/components/status-badge"
import { SuspendResumeButton } from "@/components/suspend-resume-button"
import { Terminal } from "@/components/terminal"
import {
  $api,
  errorMessage,
  errorStatus,
  fetchClient,
  isReady,
  type Sandbox,
} from "@/lib/api/client"
import { cn } from "@/lib/utils"

export const Route = createFileRoute("/e/$env/sandboxes/$id")({
  component: SandboxPage,
})

function SandboxPage() {
  const { env, id } = Route.useParams()
  const sandbox = $api.useQuery(
    "get",
    "/api/environments/{env}/sandboxes/{id}",
    { params: { path: { env, id } } },
    {
      refetchInterval: (q) =>
        q.state.data && isReady(q.state.data) ? 5000 : 1000,
    }
  )

  if (sandbox.isPending) {
    return <Spinner className="m-auto" />
  }
  if (
    sandbox.isError &&
    (!sandbox.data || errorStatus(sandbox.error) === 404)
  ) {
    return (
      <Empty>
        <EmptyHeader>
          <EmptyTitle>Sandbox unavailable</EmptyTitle>
          <EmptyDescription>{errorMessage(sandbox.error)}</EmptyDescription>
        </EmptyHeader>
      </Empty>
    )
  }

  const sb = sandbox.data
  return (
    <>
      <PageHeader>
        <h1 className="truncate text-sm font-medium">{sb.name}</h1>
        <StatusBadge sandbox={sb} />
        <SandboxOwner
          env={env}
          personaId={sb.personaId}
          className="text-xs text-muted-foreground"
        />
        <div className="ml-auto flex items-center gap-1">
          <ConnectionsSheet env={env} sandbox={sb} />
          <CheckpointsSheet env={env} sandbox={sb} />
          <ExportSandboxButton env={env} sandbox={sb} />
          {isReady(sb) && <Previews env={env} sandbox={sb} />}
          <ForkSandboxDialog env={env} sandbox={sb} />
          <RebaseSandboxDialog env={env} sandbox={sb} />
          <Lifecycle env={env} sandbox={sb} />
        </div>
      </PageHeader>
      <div className="flex min-h-0 flex-1 flex-col">
        {sandbox.isRefetchError && (
          <div
            role="status"
            className="flex shrink-0 items-center gap-2 border-b bg-amber-500/5 px-3 py-1 text-xs text-amber-700 dark:text-amber-400"
          >
            <WarningIcon className="size-3 shrink-0" />
            Sandbox status refresh failed. Retrying…
          </div>
        )}
        <Body env={env} sandbox={sb} />
      </div>
    </>
  )
}

function Body({ env, sandbox: sb }: { env: string; sandbox: Sandbox }) {
  switch (phase(sb)) {
    case "ready":
    case "suspended":
      return (
        <Terminals
          key={`${sb.id}:${sb.generation}`}
          env={env}
          id={sb.id}
          suspended={phase(sb) === "suspended"}
        />
      )
    case "booting":
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
    case "failed":
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
    default:
      return (
        <Empty>
          <EmptyHeader>
            <EmptyTitle>Stopped</EmptyTitle>
            <EmptyDescription>
              Your workspace and Docker images are kept.
            </EmptyDescription>
          </EmptyHeader>
          <EmptyContent>
            <StartButton env={env} sandbox={sb} />
          </EmptyContent>
        </Empty>
      )
  }
}

function useInvalidateSandboxes() {
  const queryClient = useQueryClient()
  return () => {
    queryClient.invalidateQueries({
      queryKey: ["get", "/api/environments/{env}/sandboxes"],
    })
    queryClient.invalidateQueries({
      queryKey: ["get", "/api/environments/{env}/sandboxes/{id}"],
    })
  }
}

function StartButton({ env, sandbox: sb }: { env: string; sandbox: Sandbox }) {
  const invalidate = useInvalidateSandboxes()
  const start = $api.useMutation(
    "post",
    "/api/environments/{env}/sandboxes/{id}/start",
    {
      onSettled: invalidate,
      onError: (err) =>
        toast.error("Could not start the sandbox", {
          description: errorMessage(err),
        }),
    }
  )
  return (
    <Button
      disabled={start.isPending}
      onClick={() => start.mutate({ params: { path: { env, id: sb.id } } })}
    >
      {start.isPending ? <Spinner /> : <PlayIcon />}
      Start
    </Button>
  )
}

function Lifecycle({ env, sandbox: sb }: { env: string; sandbox: Sandbox }) {
  const navigate = useNavigate()
  const invalidate = useInvalidateSandboxes()
  const path = { params: { path: { env, id: sb.id } } }
  const stop = $api.useMutation(
    "post",
    "/api/environments/{env}/sandboxes/{id}/stop",
    {
      onSettled: invalidate,
      onError: (err) =>
        toast.error("Could not stop the sandbox", {
          description: errorMessage(err),
        }),
    }
  )
  const remove = $api.useMutation(
    "delete",
    "/api/environments/{env}/sandboxes/{id}",
    {
      onSuccess: () => {
        invalidate()
        navigate({ to: "/e/$env", params: { env } })
      },
      onError: (err) =>
        toast.error("Could not delete the sandbox", {
          description: errorMessage(err),
        }),
    }
  )
  const p = phase(sb)

  return (
    <>
      <SuspendResumeButton env={env} sandbox={sb} />
      {(p === "ready" || p === "booting" || p === "suspended") && (
        <Button
          variant="ghost"
          size="sm"
          disabled={stop.isPending}
          onClick={() => stop.mutate(path)}
        >
          {stop.isPending ? <Spinner /> : <StopIcon />}
          Stop
        </Button>
      )}
      <AlertDialog>
        <AlertDialogTrigger
          render={
            <Button variant="ghost" size="icon-sm" title="Delete sandbox" />
          }
        >
          <TrashIcon />
        </AlertDialogTrigger>
        <AlertDialogContent>
          <AlertDialogHeader>
            <AlertDialogTitle>Delete {sb.name}?</AlertDialogTitle>
            <AlertDialogDescription>
              The VM, its workspace, Docker images, and checkpoints are deleted.
              This can't be undone.
            </AlertDialogDescription>
          </AlertDialogHeader>
          <AlertDialogFooter>
            <AlertDialogCancel>Cancel</AlertDialogCancel>
            <AlertDialogAction
              variant="destructive"
              disabled={remove.isPending}
              onClick={() => remove.mutate(path)}
            >
              {remove.isPending && <Spinner />}
              Delete
            </AlertDialogAction>
          </AlertDialogFooter>
        </AlertDialogContent>
      </AlertDialog>
    </>
  )
}

/** Preview URLs live on *.localhost, which only resolves on the machine running Studio. */
const onStudioMachine = ["localhost", "127.0.0.1", "[::1]"].includes(
  window.location.hostname
)

function Previews({ env, sandbox: sb }: { env: string; sandbox: Sandbox }) {
  const ports = $api.useQuery(
    "get",
    "/api/environments/{env}/sandboxes/{id}/ports",
    { params: { path: { env, id: sb.id } } },
    { refetchInterval: 3000 }
  )
  const list = ports.data ?? []

  async function openPreview(port: number) {
    let popup: Window | null = null
    try {
      popup = window.open("about:blank", "_blank")
    } catch {
      popup = null
    }
    if (!popup) {
      toast.error("Could not open preview", {
        description:
          "Allow pop-ups for Studio, then select this preview again.",
      })
      return
    }

    try {
      popup.opener = null
      const { data, error, response } = await fetchClient.POST(
        "/api/environments/{env}/sandboxes/{id}/previews/{port}/open",
        { params: { path: { env, id: sb.id, port } } }
      )
      if (response.status === 401) {
        try {
          popup.close()
        } catch {
          // The browser may already have closed the blank tab.
        }
        toast.error("Studio login expired", {
          description: "Reconnect, then open this preview again.",
        })
        return
      }
      if (error) {
        throw new Error(errorMessage(error))
      }
      if (!data.url) {
        throw new Error("Studio returned an invalid preview address.")
      }

      const target = new URL(data.url, window.location.origin)
      if (target.protocol !== "http:" && target.protocol !== "https:") {
        throw new Error("Studio returned an invalid preview address.")
      }
      popup.location.replace(target.href)
    } catch (error) {
      try {
        popup.close()
      } catch {
        // The browser may already have closed the blank tab.
      }
      toast.error("Could not open preview", {
        description:
          error instanceof TypeError
            ? "Could not reach Studio. Check the connection and try again."
            : errorMessage(error),
      })
    }
  }

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
              onClick={() => void openPreview(p.port)}
            >
              :{p.port}
              <ArrowSquareOutIcon className="ml-auto" />
            </DropdownMenuItem>
          ))}
        </DropdownMenuGroup>
        {!onStudioMachine && list.length > 0 && (
          <p className="px-2 py-1.5 text-[0.625rem] text-muted-foreground">
            Previews open on the machine running Studio.
          </p>
        )}
      </DropdownMenuContent>
    </DropdownMenu>
  )
}

/**
 * Terminal tabs are the guest's tmux sessions: they outlive the browser, and sessions an
 * agent starts appear here too. Open terminals stay mounted while hidden, so switching tabs
 * keeps scrollback and the connection.
 */
function Terminals({
  env,
  id,
  suspended,
}: {
  env: string
  id: string
  suspended: boolean
}) {
  const queryClient = useQueryClient()
  const sessions = $api.useQuery(
    "get",
    "/api/environments/{env}/sandboxes/{id}/terminals",
    { params: { path: { env, id } } },
    { refetchInterval: 5000, enabled: !suspended }
  )
  const [tabs, setTabs] = useState<string[]>([])
  const [closing, setClosing] = useState<ReadonlySet<string>>(new Set())
  const [active, setActive] = useState<string | null>(null)
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
  const [adopted, setAdopted] = useState(false)
  useEffect(() => {
    if (!sessions.data) return
    const names = [...sessions.data]
      .sort((a, b) => a.created - b.created)
      .map((s) => s.name)
    const live = names.filter((n) => !closing.has(n))
    setTabs((prev) => {
      const next = [...prev, ...live.filter((n) => !prev.includes(n))]
      return next.length || adopted ? next : ["main"]
    })
    setAdopted(true)
    // A closed session leaves the closing set once the guest no longer lists it.
    setClosing((prev) => {
      const still = new Set([...prev].filter((n) => names.includes(n)))
      return still.size === prev.size ? prev : still
    })
  }, [sessions.data, closing, adopted])

  useEffect(() => {
    if (!active || !tabs.includes(active)) setActive(tabs.at(-1) ?? null)
  }, [tabs, active])

  const drop = (name: string) => {
    setClosing((prev) => new Set(prev).add(name))
    setTabs((prev) => prev.filter((n) => n !== name))
  }
  const add = () => {
    let n = 2
    while (tabs.includes(String(n)) || closing.has(String(n))) n++
    const name =
      tabs.includes("main") || closing.has("main") ? String(n) : "main"
    setTabs((prev) => [...prev, name])
    setActive(name)
  }

  if (suspended && !sessions.data) {
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
  }
  if (sessions.isPending) return <Spinner className="m-auto" />

  return (
    <>
      <div className="flex h-9 shrink-0 items-center gap-0.5 border-b bg-muted/40 px-1">
        {tabs.map((name) => (
          <div
            key={name}
            className={cn(
              "group flex h-7 items-center rounded-md text-xs",
              name === active
                ? "bg-background shadow-xs"
                : "text-muted-foreground hover:bg-muted"
            )}
          >
            <button
              type="button"
              className="h-full pr-1 pl-2.5 font-mono"
              onClick={() => setActive(name)}
            >
              {name}
            </button>
            <button
              type="button"
              title={`Close ${name} (ends its processes)`}
              className="mr-1 rounded p-0.5 opacity-50 group-hover:opacity-100 hover:bg-muted-foreground/20"
              onClick={() => {
                drop(name)
                close.mutate({ params: { path: { env, id, name } } })
              }}
            >
              <XIcon className="size-3" />
            </button>
          </div>
        ))}
        <Button
          variant="ghost"
          size="icon-xs"
          title="New terminal"
          onClick={add}
        >
          <PlusIcon />
        </Button>
      </div>
      <div className="min-h-0 flex-1">
        {tabs.length === 0 && (
          <Empty className="h-full">
            <EmptyHeader>
              <EmptyTitle>No terminals open</EmptyTitle>
            </EmptyHeader>
            <EmptyContent>
              <Button onClick={add}>
                <PlusIcon />
                New terminal
              </Button>
            </EmptyContent>
          </Empty>
        )}
        {tabs.map((name) => (
          <Terminal
            key={name}
            url={`/api/environments/${env}/sandboxes/${id}/terminals/${name}/attach`}
            active={name === active}
            suspended={suspended}
            onExit={() => drop(name)}
          />
        ))}
      </div>
    </>
  )
}
