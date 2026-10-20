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

/**
 * One sandbox: its workspace (/work; the index redirects there) gets this frame and the
 * sandbox it loads.
 */
export const Route = createFileRoute("/e/$env/sandboxes/$id")({
  component: SandboxLayout,
})

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
      {sandbox.isRefetchError && (
        <div
          role="status"
          className="flex shrink-0 items-center gap-2 border-b bg-amber-500/5 px-3 py-1 text-xs text-amber-700 dark:text-amber-400"
        >
          <WarningIcon className="size-3 shrink-0" />
          Sandbox status refresh failed. Retrying…
        </div>
      )}
      <div className="flex min-h-0 flex-1 flex-col">
        <Outlet />
      </div>
    </SandboxPageProvider>
  )
}
