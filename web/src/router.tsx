import { createRouter as createTanStackRouter } from "@tanstack/react-router"
import type { QueryClient } from "@tanstack/react-query"
import { routeTree } from "./routeTree.gen"
import { RouteError } from "@/components/route-error"

export function createRouter(queryClient: QueryClient) {
  return createTanStackRouter({
    routeTree,
    context: { queryClient },
    scrollRestoration: true,
    defaultPreload: "intent",
    defaultPreloadStaleTime: 0,
    // A page that fails shows what went wrong in its place, not the router's bare page.
    defaultErrorComponent: RouteError,
  })
}

declare module "@tanstack/react-router" {
  interface Register {
    router: ReturnType<typeof createRouter>
  }
}
