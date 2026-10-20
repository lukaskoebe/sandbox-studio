import { createFileRoute, redirect } from "@tanstack/react-router"

// Pushes that wait for you moved to Home, decided ones to Settings → Activity. Old address
// kept for bookmarks.
export const Route = createFileRoute("/e/$env/git/")({
  beforeLoad: ({ params }) => {
    throw redirect({
      to: "/e/$env/settings/activity",
      params,
      search: { kind: "git" },
      replace: true,
    })
  },
})
