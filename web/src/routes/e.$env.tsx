import { useEffect } from "react"
import {
  Outlet,
  createFileRoute,
  notFound,
  redirect,
} from "@tanstack/react-router"
import { AppSidebar } from "@/components/app-sidebar"
import { oldSettingsSection, settingsPaths } from "@/components/settings/link"
import { SidebarInset, SidebarProvider } from "@/components/ui/sidebar"
import { $api } from "@/lib/api/client"
import { rememberEnvironment } from "@/lib/environment"
import { setupOnFirstLoad } from "@/lib/setup"
import { useVisualViewportHeight } from "@/hooks/use-visual-viewport-height"

export const Route = createFileRoute("/e/$env")({
  // ?settings=<section> opened the settings dialog that the Settings page replaced; old links
  // land on the section they meant.
  beforeLoad: ({ params, search }) => {
    const section = oldSettingsSection(
      (search as Record<string, unknown>).settings
    )
    if (section)
      throw redirect({ to: settingsPaths[section], params, replace: true })
  },
  loader: async ({ context, params }) => {
    const envs = await context.queryClient.ensureQueryData(
      $api.queryOptions("get", "/api/environments")
    )
    if (!envs?.some((e) => e.id === params.env)) throw notFound()
    if (await setupOnFirstLoad(context.queryClient, params.env))
      throw redirect({ to: "/setup" })
  },
  component: EnvironmentLayout,
})

function EnvironmentLayout() {
  const { env } = Route.useParams()
  const viewportHeight = useVisualViewportHeight()
  const viewportStyle = viewportHeight
    ? { height: viewportHeight, minHeight: viewportHeight }
    : undefined
  useEffect(() => rememberEnvironment(env), [env])

  return (
    <SidebarProvider style={viewportStyle}>
      <AppSidebar env={env} />
      <SidebarInset
        className="h-svh min-w-0 overflow-hidden"
        style={viewportHeight ? { height: viewportHeight } : undefined}
      >
        <Outlet />
      </SidebarInset>
    </SidebarProvider>
  )
}
