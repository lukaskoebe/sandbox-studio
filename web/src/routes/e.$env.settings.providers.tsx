import { createFileRoute } from "@tanstack/react-router"
import { ProvidersSettings } from "@/components/settings/providers"

export const Route = createFileRoute("/e/$env/settings/providers")({
  component: Section,
})

function Section() {
  const { env } = Route.useParams()
  return <ProvidersSettings env={env} />
}
