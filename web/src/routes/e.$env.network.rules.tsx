import { createFileRoute, redirect } from "@tanstack/react-router"

// Network rules moved to the Network page, with their filters. Old address kept for
// bookmarks.
export const Route = createFileRoute("/e/$env/network/rules")({
  beforeLoad: ({ params, search }) => {
    throw redirect({
      to: "/e/$env/network",
      params,
      search: search as Record<string, string>,
      replace: true,
    })
  },
})
