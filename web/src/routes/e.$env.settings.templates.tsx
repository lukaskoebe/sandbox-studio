import { createFileRoute, redirect } from "@tanstack/react-router"

// Settings are a dialog; the page's old address opens it over Home.
export const Route = createFileRoute("/e/$env/settings/templates")({
  beforeLoad: ({ params }) => {
    throw redirect({
      to: "/e/$env",
      params,
      search: { settings: "templates" },
      replace: true,
    })
  },
})
