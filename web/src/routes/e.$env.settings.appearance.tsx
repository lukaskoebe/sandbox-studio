import { createFileRoute } from "@tanstack/react-router"
import { AppearanceSettings } from "@/components/settings/general"

export const Route = createFileRoute("/e/$env/settings/appearance")({
  component: AppearanceSettings,
})
