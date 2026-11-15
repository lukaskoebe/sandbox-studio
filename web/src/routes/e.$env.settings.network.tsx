import { createFileRoute, redirect } from "@tanstack/react-router"

// Network moved out of Settings to its own page under Environment. Old address kept
// for bookmarks, with its filters.
export const Route = createFileRoute("/e/$env/settings/network")({
  beforeLoad: ({ params, search }) => {
    throw redirect({
      to: "/e/$env/network",
      params,
      search: search as Record<string, string>,
      replace: true,
    })
  },
})
