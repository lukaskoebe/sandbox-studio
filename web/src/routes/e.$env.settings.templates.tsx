import { createFileRoute } from "@tanstack/react-router"
import { TemplatesSettings } from "@/components/settings/templates"

export const Route = createFileRoute("/e/$env/settings/templates")({
  component: Section,
})

function Section() {
  const { env } = Route.useParams()
  return <TemplatesSettings env={env} />
}
