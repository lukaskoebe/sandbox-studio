import { createFileRoute } from "@tanstack/react-router"
import { NotificationsSettings } from "@/components/settings/general"

export const Route = createFileRoute("/e/$env/settings/notifications")({
  component: NotificationsSettings,
})
