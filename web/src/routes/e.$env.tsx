import { useEffect } from "react"
import {
  Outlet,
  createFileRoute,
  notFound,
  redirect,
} from "@tanstack/react-router"
import { AppSidebar } from "@/components/app-sidebar"
import { SettingsDialog } from "@/components/settings/dialog"
import {
  isSettingsSection,
  type SettingsSection,
} from "@/components/settings/link"
import { SidebarInset, SidebarProvider } from "@/components/ui/sidebar"
import { $api } from "@/lib/api/client"
import { rememberEnvironment } from "@/lib/environment"
import { setupOnFirstLoad } from "@/lib/setup"
import { useVisualViewportHeight } from "@/hooks/use-visual-viewport-height"

export const Route = createFileRoute("/e/$env")({
  // ?settings=<section> opens the settings dialog over any page of the environment.
  validateSearch: (
    s: Record<string, unknown>
  ): { settings?: SettingsSection } =>
    isSettingsSection(s.settings) ? { settings: s.settings } : {},
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
  const { settings } = Route.useSearch()
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
      <SettingsDialog env={env} section={settings} />
    </SidebarProvider>
  )
}
