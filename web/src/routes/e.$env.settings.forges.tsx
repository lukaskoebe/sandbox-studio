import { createFileRoute } from "@tanstack/react-router"
import { GitSettings } from "@/components/settings/git"

export const Route = createFileRoute("/e/$env/settings/forges")({
  component: Section,
})

function Section() {
  const { env } = Route.useParams()
  return <GitSettings env={env} />
}
