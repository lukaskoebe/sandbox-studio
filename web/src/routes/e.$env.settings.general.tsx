import { createFileRoute } from "@tanstack/react-router"
import { GeneralSettings } from "@/components/settings/general"

export const Route = createFileRoute("/e/$env/settings/general")({
  component: Section,
})

function Section() {
  const { env } = Route.useParams()
  return <GeneralSettings env={env} />
}
