import { createFileRoute, redirect } from "@tanstack/react-router"

// Network rules moved to Settings → Network. Old address kept for bookmarks.
export const Route = createFileRoute("/e/$env/network/")({
  beforeLoad: ({ params }) => {
    throw redirect({ to: "/e/$env/settings/network", params, replace: true })
  },
})
