import { createFileRoute } from "@tanstack/react-router"
import { NetworkSettings } from "@/components/settings/network"

export const Route = createFileRoute("/e/$env/settings/ports")({
  component: Section,
})

function Section() {
  const { env } = Route.useParams()
  return <NetworkSettings env={env} />
}
