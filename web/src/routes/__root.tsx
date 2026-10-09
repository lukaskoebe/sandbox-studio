import { Outlet, createRootRouteWithContext } from "@tanstack/react-router"
import type { QueryClient } from "@tanstack/react-query"
import { ApprovalStack } from "@/components/approval-stack"
import { TooltipProvider } from "@/components/ui/tooltip"
import { Toaster } from "@/components/ui/sonner"
import { useEvents } from "@/hooks/use-events"

export const Route = createRootRouteWithContext<{ queryClient: QueryClient }>()(
  {
    component: RootLayout,
    notFoundComponent: () => (
      <main className="p-6 text-sm">
        <h1 className="font-medium">Not found</h1>
      </main>
    ),
  }
)

function RootLayout() {
  useEvents()
  return (
    <TooltipProvider>
      <Outlet />
      <ApprovalStack />
      <Toaster position="top-center" />
    </TooltipProvider>
  )
}
