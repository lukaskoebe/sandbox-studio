import { useEffect } from "react"
import { Outlet, createFileRoute, notFound } from "@tanstack/react-router"
import { AppSidebar } from "@/components/app-sidebar"
import { SidebarInset, SidebarProvider } from "@/components/ui/sidebar"
import { $api } from "@/lib/api/client"
import { rememberEnvironment } from "@/lib/environment"

export const Route = createFileRoute("/e/$env")({
  loader: async ({ context, params }) => {
    const envs = await context.queryClient.ensureQueryData(
      $api.queryOptions("get", "/api/environments")
    )
    if (!envs?.some((e) => e.id === params.env)) throw notFound()
  },
  component: EnvironmentLayout,
})

function EnvironmentLayout() {
  const { env } = Route.useParams()
  useEffect(() => rememberEnvironment(env), [env])

  return (
    <SidebarProvider>
      <AppSidebar env={env} />
      <SidebarInset className="h-svh min-w-0 overflow-hidden">
        <Outlet />
      </SidebarInset>
    </SidebarProvider>
  )
}
