import { createFileRoute, redirect } from "@tanstack/react-router"

// Shared memory moved to Settings → Shared memory. Old address kept for bookmarks.
export const Route = createFileRoute("/e/$env/memory/")({
  beforeLoad: ({ params }) => {
    throw redirect({ to: "/e/$env/settings/memory", params, replace: true })
  },
})
