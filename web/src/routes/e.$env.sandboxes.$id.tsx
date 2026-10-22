import { useEffect, useState } from "react"
import { createFileRoute, Outlet } from "@tanstack/react-router"
import { WarningIcon } from "@phosphor-icons/react"
import {
  Empty,
  EmptyDescription,
  EmptyHeader,
  EmptyTitle,
} from "@/components/ui/empty"
import { Spinner } from "@/components/ui/spinner"
import { SandboxHeader, SandboxPageProvider } from "@/components/sandbox-page"
import { $api, errorMessage, errorStatus, isReady } from "@/lib/api/client"
import { formatAge } from "@/lib/utils"

/**
 * One sandbox: its workspace (/work; the index redirects there) gets this frame and the
 * sandbox it loads.
 */
export const Route = createFileRoute("/e/$env/sandboxes/$id")({
  component: SandboxLayout,
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

function SandboxLayout() {
  const { env, id } = Route.useParams()
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
          <EmptyTitle>Sandbox unavailable</EmptyTitle>
          <EmptyDescription>{errorMessage(sandbox.error)}</EmptyDescription>
        </EmptyHeader>
      </Empty>
    )
  }

  return (
    <SandboxPageProvider value={{ env, sandbox: sb }}>
      <SandboxHeader env={env} sandbox={sb} />
      <StaleStatus
        failing={sandbox.isRefetchError}
        updatedAt={sandbox.dataUpdatedAt}
        error={sandbox.error}
      />
      <div className="flex min-h-0 flex-1 flex-col">
        <Outlet />
      </div>
    </SandboxPageProvider>
  )
}
