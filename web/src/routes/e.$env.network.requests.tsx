import { createFileRoute, redirect } from "@tanstack/react-router"

// Requests that wait for you moved to Home. Old address kept for bookmarks.
export const Route = createFileRoute("/e/$env/network/requests")({
  beforeLoad: ({ params }) => {
    throw redirect({ to: "/e/$env", params, replace: true })
  },
})
