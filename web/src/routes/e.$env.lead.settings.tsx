import { createFileRoute } from "@tanstack/react-router"
import { LeadSettingsPage } from "@/components/lead/settings-page"

/** How Lead writes the day (docs/lead.md). */
export const Route = createFileRoute("/e/$env/lead/settings")({
  component: LeadSettings,
})

function LeadSettings() {
  const { env } = Route.useParams()
  return <LeadSettingsPage env={env} />
}
