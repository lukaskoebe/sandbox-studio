import { useEffect, useState } from "react"
import { createFileRoute, redirect } from "@tanstack/react-router"
import {
  ArrowLineLeftIcon,
  ArrowLineRightIcon,
  WarningIcon,
} from "@phosphor-icons/react"
import { LowDiskStatus } from "@/components/low-disk"
import { BackToSetup, fromSetup } from "@/components/back-to-setup"
import { ComputerScreen, type ScreenChange } from "@/components/computer/screen"
import {
  Empty,
  EmptyDescription,
  EmptyHeader,
  EmptyTitle,
} from "@/components/ui/empty"
import { ThreadDot } from "@/components/thread-status"
import { Button } from "@/components/ui/button"
import { Spinner } from "@/components/ui/spinner"
import { $api, errorMessage, errorStatus, isReady } from "@/lib/api/client"
import { screenSearch, type ScreenSearch } from "@/lib/computer"
import { programName, sortTerminals } from "@/lib/terminals"
import { cn, formatAge } from "@/lib/utils"

/**
 * A computer without a teammate: its screen at full width (docs/design/teammate-computer.md). A
 * teammate's computer is on the teammate's desk, so its address redirects there.
 */
export const Route = createFileRoute("/e/$env/sandboxes/$id")({
  validateSearch: (
    s: Record<string, unknown>
  ): ScreenSearch & { from?: "setup" } => ({
    ...screenSearch(s),
    ...fromSetup(s),
  }),
  beforeLoad: async ({ context, params, location }) => {
    const { env } = params
    const list = $api.queryOptions("get", "/api/environments/{env}/sandboxes", {
      params: { path: { env } },
    })
    const find = (l?: { id: string; teammateId?: string }[] | null) =>
      l?.find((s) => s.id === params.id)
    // A sandbox made a moment ago is not in the cached list yet.
    const sb =
      find(
        await context.queryClient.ensureQueryData(list).catch(() => undefined)
      ) ??
      find(await context.queryClient.fetchQuery(list).catch(() => undefined))
    const owner = sb?.teammateId
    if (!owner) return
    // A computer whose teammate was deleted stays here, as a plain one.
    const teammates = await context.queryClient
      .ensureQueryData(
        $api.queryOptions("get", "/api/environments/{env}/teammates", {
          params: { path: { env } },
        })
      )
      .catch(() => undefined)
    if (teammates && !teammates.some((t) => t.id === owner)) return
    const s = location.search as Record<string, unknown>
    const thread = typeof s.chat === "string" && s.chat ? s.chat : undefined
    const screen = screenSearch(s)
    // Without a thread, the teammate's desk: the computer open beside its home.
    throw redirect({
      to: "/e/$env/teammates/$id",
      params: { env, id: owner },
      search: {
        ...screen,
        ...(!thread && !screen.tab ? { tab: "terminal" as const } : {}),
        computer: params.id,
        ...(thread ? { thread } : {}),
        ...fromSetup(s),
      },
      replace: true,
    })
  },
  component: SandboxPage,
})

/** How long the status may go without fresh data before the page says so. */
const staleAfter = 15_000

/**
 * Says the status may be out of date once refreshing it has failed for a while. A refresh
 * fails now and then while microsandbox is busy starting or checkpointing another sandbox,
 * and the next one succeeds, so a single failure is not worth a banner.
 */
function StaleStatus({
  failing,
  updatedAt,
  error,
}: {
  failing: boolean
  updatedAt: number
  error: unknown
}) {
  const [now, setNow] = useState(Date.now)
  useEffect(() => {
    if (!failing) return
    setNow(Date.now())
    const tick = setInterval(() => setNow(Date.now()), 5000)
    return () => clearInterval(tick)
  }, [failing])
  if (!failing || now - updatedAt < staleAfter) return null
  return (
    <div
      role="status"
      title={errorMessage(error)}
      className="flex shrink-0 items-center gap-2 border-b bg-amber-500/5 px-3 py-1 text-xs text-amber-700 dark:text-amber-400"
    >
      <WarningIcon className="size-3 shrink-0" />
      Status may be out of date (updated{" "}
      {formatAge(new Date(updatedAt).toISOString(), now)}). Retrying…
    </div>
  )
}

function SandboxPage() {
  const { env, id } = Route.useParams()
  const search = Route.useSearch()
  const navigate = Route.useNavigate()
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
  const sb = sandbox.data
  if (!sb || (sandbox.isError && errorStatus(sandbox.error) === 404)) {
    if (sandbox.isPending) return <Spinner className="m-auto" />
    return (
      <Empty>
        <EmptyHeader>
          <EmptyTitle>Computer unavailable</EmptyTitle>
          <EmptyDescription>{errorMessage(sandbox.error)}</EmptyDescription>
        </EmptyHeader>
      </Empty>
    )
  }

  const change = ({ computer, fold: _, push, ...rest }: ScreenChange) => {
    if (computer && computer !== id) {
      void navigate({
        to: "/e/$env/sandboxes/$id",
        params: { env, id: computer },
        search: {},
      })
      return
    }
    void navigate({
      search: (prev) => ({
        ...screenSearch({ ...prev, ...rest }),
        ...fromSetup(prev),
      }),
      replace: !push,
    })
  }

  return (
    <>
      {search.from === "setup" && (
        <div className="shrink-0 px-4 pt-3">
          <BackToSetup step="sandbox" />
        </div>
      )}
      <StaleStatus
        failing={sandbox.isRefetchError}
        updatedAt={sandbox.dataUpdatedAt}
        error={sandbox.error}
      />
      <LowDiskStatus env={env} sandbox={sb} />
      <div className="flex min-h-0 flex-1">
        <TerminalsPanel
          env={env}
          sandbox={sb.id}
          open={
            search.tab === "terminal" || !search.tab
              ? (search.term ?? "main")
              : undefined
          }
          onOpen={(term) => change({ tab: "terminal", term, push: true })}
        />
        <ComputerScreen
          key={sb.id}
          env={env}
          sandbox={sb}
          search={search}
          onChange={change}
          solo
        />
      </div>
    </>
  )
}

const panelKey = "studio.computer-terminals"

/**
 * The computer's terminal sessions as threads beside its screen, as a teammate's desk lists its
 * threads; folded to a rail until opened, which this browser remembers.
 */
function TerminalsPanel({
  env,
  sandbox,
  open,
  onOpen,
}: {
  env: string
  sandbox: string
  /** The session the screen's terminal shows. */
  open?: string
  onOpen: (term: string) => void
}) {
  const [shown, setShown] = useState(() => {
    try {
      return localStorage.getItem(panelKey) === "open"
    } catch {
      return false
    }
  })
  const terminals = $api.useQuery("get", "/api/environments/{env}/terminals", {
    params: { path: { env } },
  })
  const list = sortTerminals(
    terminals.data?.find((t) => t.sandboxId === sandbox)?.terminals ?? []
  )
  // With no session in the URL the screen shows "main", or the first there is.
  const current =
    open === "main" && !list.some((t) => t.name === "main")
      ? list.at(0)?.name
      : open
  const show = (on: boolean) => {
    setShown(on)
    try {
      localStorage.setItem(panelKey, on ? "open" : "folded")
    } catch {
      // The panel just starts folded next time.
    }
  }
  if (!shown)
    return (
      <section
        aria-label="Terminals"
        className="hidden w-10 shrink-0 flex-col items-center gap-1 border-r py-2 md:flex"
      >
        <Button
          variant="ghost"
          size="icon-sm"
          aria-label="Show the terminals"
          title="Show the terminals"
          className="text-muted-foreground hover:text-foreground"
          onClick={() => show(true)}
        >
          <ArrowLineRightIcon />
        </Button>
        {list.some((t) => t.harness) && (
          <ThreadDot status="terminal" className="mt-1" />
        )}
      </section>
    )
  return (
    <section
      aria-label="Terminals"
      className="hidden w-64 shrink-0 flex-col border-r md:flex"
    >
      <header className="flex h-12 shrink-0 items-center pr-1.5 pl-3">
        <h2 className="font-heading text-sm font-semibold">Terminals</h2>
        <Button
          variant="ghost"
          size="icon-sm"
          aria-label="Fold the terminals"
          title="Fold the terminals"
          className="ml-auto text-muted-foreground hover:text-foreground"
          onClick={() => show(false)}
        >
          <ArrowLineLeftIcon />
        </Button>
      </header>
      <div className="grid min-h-0 flex-1 content-start gap-1 overflow-y-auto px-2 pb-2">
        {list.map((t) => (
          <button
            key={t.name}
            type="button"
            aria-current={t.name === current || undefined}
            onClick={() => onOpen(t.name)}
            className={cn(
              "flex min-w-0 items-center gap-2.5 rounded-lg px-2 py-1.5 text-left text-sm outline-none hover:bg-muted focus-visible:ring-2 focus-visible:ring-ring/40",
              t.name === current && "bg-muted"
            )}
          >
            <span className="flex w-3.5 shrink-0 justify-center">
              {t.harness ? (
                <ThreadDot status="terminal" />
              ) : (
                <span
                  className={cn(
                    "size-1.5 rounded-full",
                    t.state === "idle"
                      ? "ring-1 ring-muted-foreground/60 ring-inset"
                      : "bg-(--st-running)"
                  )}
                />
              )}
            </span>
            <span className="grid min-w-0 flex-1 leading-tight">
              <span className="truncate">{t.title || t.label || t.name}</span>
              <span className="truncate font-mono text-caption text-muted-foreground">
                {[t.program && programName(t.program), t.state]
                  .filter(Boolean)
                  .join(" · ")}
              </span>
            </span>
          </button>
        ))}
        {terminals.data && list.length === 0 && (
          <p className="px-2 text-xs text-muted-foreground">
            No terminal sessions yet.
          </p>
        )}
      </div>
    </section>
  )
}
