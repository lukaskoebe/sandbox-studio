import { createFileRoute, redirect } from "@tanstack/react-router"

// Forges moved to Settings → Git. Old address kept for bookmarks.
export const Route = createFileRoute("/e/$env/git/")({
  beforeLoad: ({ params }) => {
    throw redirect({ to: "/e/$env/settings/git", params, replace: true })
  },
})
