import { Outlet, createRootRouteWithContext } from "@tanstack/react-router"
import type { QueryClient } from "@tanstack/react-query"

export const Route = createRootRouteWithContext<{ queryClient: QueryClient }>()(
  {
    component: () => <Outlet />,
    notFoundComponent: () => (
      <main className="p-6 text-sm">
        <h1 className="font-medium">Not found</h1>
      </main>
    ),
  }
)
