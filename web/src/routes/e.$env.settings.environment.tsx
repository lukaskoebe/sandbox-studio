import { createFileRoute } from "@tanstack/react-router"
import { EnvironmentSettings } from "@/components/settings/general"

export const Route = createFileRoute("/e/$env/settings/environment")({
  component: Section,
})

function Section() {
  const { env } = Route.useParams()
  return <EnvironmentSettings env={env} />
}
