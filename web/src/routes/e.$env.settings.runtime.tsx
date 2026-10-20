import { createFileRoute } from "@tanstack/react-router"
import { RuntimeSettings } from "@/components/settings/runtime"

export const Route = createFileRoute("/e/$env/settings/runtime")({
  component: Section,
})

function Section() {
  return <RuntimeSettings />
}
