import { createFileRoute, redirect } from "@tanstack/react-router"

// Builds moved to Settings → Templates. Old address kept for bookmarks.
export const Route = createFileRoute("/e/$env/builds/")({
  beforeLoad: ({ params }) => {
    throw redirect({
      to: "/e/$env",
      params,
      search: { settings: "templates" },
      replace: true,
    })
  },
})
