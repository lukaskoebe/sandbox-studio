import { Outlet, createRootRouteWithContext } from "@tanstack/react-router"
import type { QueryClient } from "@tanstack/react-query"
import { ApprovalStack } from "@/components/approval-stack"
import { WindowBar, WindowEdges } from "@/components/window-bar"
import { TooltipProvider } from "@/components/ui/tooltip"
import { Toaster } from "@/components/ui/sonner"
import { useEvents } from "@/hooks/use-events"

export const Route = createRootRouteWithContext<{ queryClient: QueryClient }>()(
  {
    component: RootLayout,
    notFoundComponent: () => (
      <>
        <WindowBar />
        <main className="p-6 text-sm">
          <h1 className="font-medium">Not found</h1>
        </main>
      </>
    ),
  }
)

function RootLayout() {
  useEvents()
  return (
    <TooltipProvider>
      <Outlet />
      <WindowEdges />
      <ApprovalStack />
      <Toaster position="top-center" closeButton />
    </TooltipProvider>
  )
}
