import { createFileRoute, redirect } from "@tanstack/react-router"

// Decided requests moved to Activity. Old address kept for bookmarks.
export const Route = createFileRoute("/e/$env/network/activity")({
  beforeLoad: ({ params }) => {
    throw redirect({
      to: "/e/$env/activity",
      params,
      search: { kind: "network" },
      replace: true,
    })
  },
})
