import { createFileRoute, redirect } from "@tanstack/react-router"

// Providers moved to Settings → Providers. Old address kept for bookmarks.
export const Route = createFileRoute("/e/$env/providers")({
  beforeLoad: ({ params }) => {
    throw redirect({ to: "/e/$env/settings/providers", params, replace: true })
  },
})
