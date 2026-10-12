import { useEffect, useRef, useState } from "react"
import { createFileRoute, useNavigate } from "@tanstack/react-router"
import { useQueryClient } from "@tanstack/react-query"
import { toast } from "sonner"
import {
  ArrowSquareOutIcon,
  BroadcastIcon,
  BrowserIcon,
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
import { LineageTimeline } from "@/components/lineage-timeline"
import { AgentSessions } from "@/components/agent-sessions"
import { SandboxChats } from "@/components/chat/sandbox-chats"
import { PageHeader } from "@/components/page-header"
import { SandboxOwner } from "@/components/teammate-avatar"
import { RebaseSandboxDialog } from "@/components/rebase-sandbox-dialog"
import { StatusBadge, phase } from "@/components/status-badge"
import { SuspendResumeButton } from "@/components/suspend-resume-button"
import { Terminal } from "@/components/terminal"
import { SandboxBrowserPanel } from "@/components/sandbox-browser-panel"
import { SandboxProgress } from "@/components/sandbox-progress"
import {
  $api,
  errorMessage,
  errorStatus,
  isReady,
  type Sandbox,
} from "@/lib/api/client"
import { useAgentView } from "@/lib/agent-view"
import { onStudioMachine, openPreviewTab } from "@/lib/previews"
import { cn } from "@/lib/utils"

export const Route = createFileRoute("/e/$env/sandboxes/$id")({
  component: SandboxPage,
  // terminal: a tmux session to open, such as a chat handed over to the terminal.
  validateSearch: (s: Record<string, unknown>): { terminal?: string } =>
    typeof s.terminal === "string" ? { terminal: s.terminal } : {},
})

function SandboxPage() {
  const { env, id } = Route.useParams()
  const { terminal } = Route.useSearch()
  const { view } = useAgentView()
  // The terminal tab an agent session asked to show; seq makes repeated asks count.
  const [focus, setFocus] = useState<Focus | null>(
    terminal ? { name: terminal, seq: 1 } : null
  )
  const [browserOpenFor, setBrowserOpenFor] = useState<string | null>(null)
  const [browserPanelWidth, setBrowserPanelWidth] = useState(42)
  const workspaceRef = useRef<HTMLDivElement>(null)
  const browserToggleRef = useRef<HTMLButtonElement>(null)
  const [isNarrowViewport, setIsNarrowViewport] = useState(
    () => window.matchMedia("(max-width: 1023.98px)").matches
  )
  useEffect(() => {
    const media = window.matchMedia("(max-width: 1023.98px)")
    const update = () => setIsNarrowViewport(media.matches)
    media.addEventListener("change", update)
    update()
    return () => media.removeEventListener("change", update)
  }, [])
  const sandbox = $api.useQuery(
    "get",
    "/api/environments/{env}/sandboxes/{id}",
    { params: { path: { env, id } } },
    {
      refetchInterval: (q) => {
        const data = q.state.data
        if (data && isReady(data)) return 5000
        // A failed create or fork changes only by its delete, which the sandboxes topic announces.
        return data?.status === "failed" ? false : 1000
      },
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
  // A create or fork that runs or failed: no guest yet, so guest-only controls stay hidden.
  const building = sb.status === "creating" || sb.status === "failed"
  const browserKey = `${sb.id}:${sb.teammateId ?? ""}`
  const browserOpen = !!sb.teammateId && browserOpenFor === browserKey
  const closeBrowser = () => {
    setBrowserOpenFor(null)
    requestAnimationFrame(() => browserToggleRef.current?.focus())
  }
  const resizeBrowser = (clientX: number) => {
    const rect = workspaceRef.current?.getBoundingClientRect()
    if (!rect || rect.width <= 0) return
    const width = ((rect.right - clientX) / rect.width) * 100
    setBrowserPanelWidth(Math.round(Math.min(70, Math.max(30, width))))
  }
  return (
    <>
      <PageHeader>
        <h1 className="min-w-0 flex-1 truncate text-sm font-medium">
          {sb.name}
        </h1>
        <span className="shrink-0">
          <StatusBadge sandbox={sb} />
        </span>
        <SandboxOwner
          env={env}
          teammateId={sb.teammateId}
          className="hidden min-w-0 shrink-0 items-center text-xs text-muted-foreground sm:inline-flex"
        />
        {sb.teammateId && !building && (
          <Button
            ref={browserToggleRef}
            className="shrink-0"
            size="sm"
            variant={browserOpen ? "secondary" : "ghost"}
            aria-pressed={browserOpen}
            aria-expanded={browserOpen}
            aria-label={
              browserOpen ? "Hide teammate browser" : "Show teammate browser"
            }
            title={
              browserOpen ? "Hide teammate browser" : "Show teammate browser"
            }
            onClick={() =>
              browserOpen ? closeBrowser() : setBrowserOpenFor(browserKey)
            }
          >
            <BrowserIcon />
            <span className="hidden sm:inline">Browser</span>
          </Button>
        )}
      </PageHeader>
      <div
        role="group"
        aria-label="Sandbox actions"
        className="flex h-9 min-w-0 shrink-0 items-center overflow-x-auto border-b px-3"
      >
        <div className="flex w-max shrink-0 items-center gap-1">
          {!building && sb.teammateId && view !== "terminal" && (
            <div className="shrink-0">
              <SandboxChats env={env} id={sb.id} />
            </div>
          )}
          {sb.teammateId && isReady(sb) && (
            <div className="shrink-0">
              <AgentSessions
                env={env}
                id={sb.id}
                teammateId={sb.teammateId}
                onAttach={(name) =>
                  setFocus((f) => ({ name, seq: (f?.seq ?? 0) + 1 }))
                }
              />
            </div>
          )}
          {/* Chat or terminal comes first by preference; the other is one click away. */}
          {!building && sb.teammateId && view === "terminal" && (
            <div className="shrink-0">
              <SandboxChats env={env} id={sb.id} />
            </div>
          )}
          <div className="shrink-0">
            <ConnectionsSheet env={env} sandbox={sb} />
          </div>
          {!building && (
            <div className="shrink-0">
              <CheckpointsSheet env={env} sandbox={sb} />
            </div>
          )}
          {!building && (
            <div className="shrink-0">
              <ExportSandboxButton env={env} sandbox={sb} />
            </div>
          )}
          {isReady(sb) && (
            <div className="shrink-0">
              <Previews env={env} sandbox={sb} />
            </div>
          )}
          {!building && (
            <div className="shrink-0">
              <ForkSandboxDialog env={env} sandbox={sb} />
            </div>
          )}
          {!building && (
            <div className="shrink-0">
              <RebaseSandboxDialog env={env} sandbox={sb} />
            </div>
          )}
          {!building && (
            <div className="shrink-0">
              <Lifecycle env={env} sandbox={sb} />
            </div>
          )}
        </div>
      </div>
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
        <div ref={workspaceRef} className="relative flex min-h-0 flex-1">
          <div
            id="sandbox-workspace"
            inert={browserOpen && isNarrowViewport}
            className="flex min-h-0 min-w-0 flex-1 flex-col"
          >
            <Body env={env} sandbox={sb} focus={focus} />
            {/* lineage */}
            <section
              aria-labelledby="sandbox-history"
              className="shrink-0 border-t px-3 py-3"
            >
              <h2
                id="sandbox-history"
                className="mb-2 text-xs font-medium text-muted-foreground"
              >
                History
              </h2>
              <LineageTimeline environmentId={env} sandboxId={sb.id} />
            </section>
          </div>
          {browserOpen && sb.teammateId && (
            <>
              <div
                role="separator"
                aria-label="Resize browser panel"
                aria-orientation="vertical"
                aria-controls="sandbox-browser-panel"
                aria-valuemin={30}
                aria-valuemax={70}
                aria-valuenow={browserPanelWidth}
                aria-valuetext={`Browser panel ${browserPanelWidth}% of workspace width`}
                tabIndex={0}
                className="hidden w-1 shrink-0 cursor-col-resize touch-none items-center justify-center bg-border/50 outline-none hover:bg-primary/40 focus-visible:bg-primary/60 lg:flex"
                onPointerDown={(event) => {
                  event.preventDefault()
                  event.currentTarget.setPointerCapture(event.pointerId)
                  resizeBrowser(event.clientX)
                }}
                onPointerMove={(event) => {
                  if (event.buttons === 1) resizeBrowser(event.clientX)
                }}
                onKeyDown={(event) => {
                  if (event.key === "ArrowRight") {
                    event.preventDefault()
                    setBrowserPanelWidth((width) => Math.max(30, width - 2))
                  } else if (event.key === "ArrowLeft") {
                    event.preventDefault()
                    setBrowserPanelWidth((width) => Math.min(70, width + 2))
                  } else if (event.key === "Home") {
                    event.preventDefault()
                    setBrowserPanelWidth(70)
                  } else if (event.key === "End") {
                    event.preventDefault()
                    setBrowserPanelWidth(30)
                  }
                }}
              >
                <span className="h-8 w-0.5 rounded-full bg-foreground/25" />
              </div>
              <SandboxBrowserPanel
                key={browserKey}
                env={env}
                teammate={sb.teammateId}
                width={browserPanelWidth}
                onClose={closeBrowser}
                autoFocusClose={isNarrowViewport}
              />
            </>
          )}
        </div>
      </div>
    </>
  )
}

type Focus = { name: string; seq: number }

function Body({
  env,
  sandbox: sb,
  focus,
}: {
  env: string
  sandbox: Sandbox
  focus: Focus | null
}) {
  // A create or fork has no guest until it succeeds; a failed one is cleared by deleting it.
  if (sb.status === "creating") return <SandboxProgress sandbox={sb} />
  if (sb.status === "failed")
    return (
      <SandboxProgress
        sandbox={sb}
        action={<DeleteSandbox env={env} sandbox={sb} labelled />}
      />
    )
  switch (phase(sb)) {
    case "ready":
    case "suspended":
      return (
        <Terminals
          key={`${sb.id}:${sb.generation}`}
          env={env}
          id={sb.id}
          suspended={phase(sb) === "suspended"}
          focus={focus}
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
              Your workspace, home directory and Docker images are kept.
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
      <DeleteSandbox env={env} sandbox={sb} />
    </>
  )
}

/** Deletes the sandbox after a confirmation, then returns to the sandbox list. */
function DeleteSandbox({
  env,
  sandbox: sb,
  labelled = false,
}: {
  env: string
  sandbox: Sandbox
  /** A text button for the failed panel; the action bar shows an icon. */
  labelled?: boolean
}) {
  const navigate = useNavigate()
  const invalidate = useInvalidateSandboxes()
  const path = { params: { path: { env, id: sb.id } } }
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

  return (
    <AlertDialog>
      <AlertDialogTrigger
        render={
          labelled ? (
            <Button variant="destructive" />
          ) : (
            <Button variant="ghost" size="icon-sm" title="Delete sandbox" />
          )
        }
      >
        <TrashIcon />
        {labelled && "Delete"}
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
  )
}

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
  focus,
}: {
  env: string
  id: string
  suspended: boolean
  focus: Focus | null
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

  // An agent session to show: open its tab if needed and select it.
  useEffect(() => {
    if (!focus) return
    setTabs((prev) =>
      prev.includes(focus.name) ? prev : [...prev, focus.name]
    )
    setActive(focus.name)
  }, [focus])

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
