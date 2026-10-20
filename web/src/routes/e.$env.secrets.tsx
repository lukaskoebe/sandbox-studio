import { createFileRoute, redirect } from "@tanstack/react-router"

// Secrets moved to Settings → Secrets. Old address kept for bookmarks.
export const Route = createFileRoute("/e/$env/secrets")({
  beforeLoad: ({ params }) => {
    throw redirect({ to: "/e/$env/settings/secrets", params, replace: true })
  },
})
