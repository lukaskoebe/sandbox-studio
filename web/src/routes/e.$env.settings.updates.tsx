import { createFileRoute } from "@tanstack/react-router"
import { UpdatesSettings } from "@/components/settings/general"

export const Route = createFileRoute("/e/$env/settings/updates")({
  component: Section,
})

function Section() {
  return <UpdatesSettings />
}
