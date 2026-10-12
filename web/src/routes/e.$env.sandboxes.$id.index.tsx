import { createFileRoute, redirect } from "@tanstack/react-router"
import { SandboxDashboard } from "@/components/sandbox-dashboard"

/**
 * The sandbox's dashboard. Links from before the dashboard and workspace split carry
 * workspace parameters (?terminal=, ?session=, ?chat=); those go to the workspace.
 */
export const Route = createFileRoute("/e/$env/sandboxes/$id/")({
  beforeLoad: ({ params, search }) => {
    if (Object.keys(search).length > 0)
      throw redirect({
        to: "/e/$env/sandboxes/$id/work",
        params,
        search,
        replace: true,
      })
  },
  component: SandboxDashboard,
})
