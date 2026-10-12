import { Outlet, createRootRouteWithContext } from "@tanstack/react-router"
import type { QueryClient } from "@tanstack/react-query"
import { ApprovalStack } from "@/components/approval-stack"
import { TooltipProvider } from "@/components/ui/tooltip"
import { Toaster } from "@/components/ui/sonner"
import { useEvents } from "@/hooks/use-events"
import { mockupsActive } from "@/mockups/active"

export const Route = createRootRouteWithContext<{ queryClient: QueryClient }>()(
  {
    component: mockupsActive ? MockupsLayout : RootLayout,
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
      <Toaster position="top-center" closeButton />
    </TooltipProvider>
  )
}

/** The design mockups are static: no event stream, approvals or onboarding. */
function MockupsLayout() {
  return (
    <TooltipProvider>
      <Outlet />
    </TooltipProvider>
  )
}
