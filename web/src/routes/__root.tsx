import {
  Link,
  Outlet,
  createRootRouteWithContext,
} from "@tanstack/react-router"
import type { QueryClient } from "@tanstack/react-query"
import { ApprovalStack } from "@/components/approval-stack"
import { WindowBar, WindowEdges } from "@/components/window-bar"
import { TooltipProvider } from "@/components/ui/tooltip"
import { Toaster } from "@/components/ui/sonner"
import { useEvents } from "@/hooks/use-events"
import { RouteError } from "@/components/route-error"

export const Route = createRootRouteWithContext<{ queryClient: QueryClient }>()(
  {
    // The page draws the window's bar, so its error page does too.
    errorComponent: (props) => <RouteError {...props} bar />,
    component: RootLayout,
    notFoundComponent: () => (
      <>
        <WindowBar />
        <main className="flex flex-col items-start gap-2 p-6 text-sm">
          <h1 className="font-medium">Not found</h1>
          <p className="text-muted-foreground">
            This page doesn't exist, or what it showed was removed.
          </p>
          {/* The app has no address bar, so this is the only way out. */}
          <Link to="/" className="underline underline-offset-4">
            Back to Studio
          </Link>
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
