import { createFileRoute, redirect } from "@tanstack/react-router"

// Secrets moved out of Settings to its own page under Environment. Old address kept
// for bookmarks, with its filters.
export const Route = createFileRoute("/e/$env/settings/secrets")({
  beforeLoad: ({ params, search }) => {
    throw redirect({
      to: "/e/$env/secrets",
      params,
      search: search as Record<string, string>,
      replace: true,
    })
  },
})
