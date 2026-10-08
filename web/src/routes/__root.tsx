import { Outlet, createRootRouteWithContext } from "@tanstack/react-router"
import type { QueryClient } from "@tanstack/react-query"
import { TooltipProvider } from "@/components/ui/tooltip"
import { Toaster } from "@/components/ui/sonner"

export const Route = createRootRouteWithContext<{ queryClient: QueryClient }>()(
  {
    component: () => (
      <TooltipProvider>
        <Outlet />
        <Toaster />
      </TooltipProvider>
    ),
    notFoundComponent: () => (
      <main className="p-6 text-sm">
        <h1 className="font-medium">Not found</h1>
      </main>
    ),
  }
)
