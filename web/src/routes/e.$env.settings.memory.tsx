import { createFileRoute, redirect } from "@tanstack/react-router"

// Shared memory moved out of Settings to its own page under Environment. Old address kept
// for bookmarks, with its filters.
export const Route = createFileRoute("/e/$env/settings/memory")({
  beforeLoad: ({ params, search }) => {
    throw redirect({
      to: "/e/$env/memory",
      params,
      search: search as Record<string, string>,
      replace: true,
    })
  },
})
