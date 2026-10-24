import { useEffect, useState } from "react"
import { createFileRoute, redirect } from "@tanstack/react-router"
import { WarningIcon } from "@phosphor-icons/react"
import { BackToSetup, fromSetup } from "@/components/back-to-setup"
import { ComputerScreen, type ScreenChange } from "@/components/computer/screen"
import {
  Empty,
  EmptyDescription,
  EmptyHeader,
  EmptyTitle,
} from "@/components/ui/empty"
import { SidebarTrigger } from "@/components/ui/sidebar"
import { Spinner } from "@/components/ui/spinner"
import { $api, errorMessage, errorStatus, isReady } from "@/lib/api/client"
import { screenSearch, type ScreenSearch } from "@/lib/computer"
import { formatAge } from "@/lib/utils"

/**
 * A computer without a teammate: its screen at full width (docs/design/teammate-computer.md). A
 * teammate's computer is on the teammate's page, so its address redirects there.
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
    throw redirect({
      to: "/e/$env/teammates/$id",
      params: { env, id: owner },
      search: {
        ...screenSearch(s),
        computer: params.id,
        ...(typeof s.chat === "string" && s.chat ? { thread: s.chat } : {}),
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
  const sandboxes = $api.useQuery(
    "get",
    "/api/environments/{env}/sandboxes",
    { params: { path: { env } } },
    { refetchInterval: 5000 }
  )
  const teammates = $api.useQuery("get", "/api/environments/{env}/teammates", {
    params: { path: { env } },
  })
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

  // The switcher offers the other computers without a teammate.
  const plain = (sandboxes.data ?? []).filter(
    (s) =>
      !s.teammateId ||
      (teammates.data && !teammates.data.some((t) => t.id === s.teammateId))
  )
  const computers = plain.some((s) => s.id === sb.id) ? plain : [sb, ...plain]
  const change = ({ computer, fold: _, ...rest }: ScreenChange) => {
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
      replace: true,
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
      <div className="flex min-h-0 flex-1">
        <ComputerScreen
          key={sb.id}
          env={env}
          sandbox={sb}
          computers={computers}
          search={search}
          onChange={change}
          solo
          lead={<SidebarTrigger className="md:hidden" />}
        />
      </div>
    </>
  )
}
