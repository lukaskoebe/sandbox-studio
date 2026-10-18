import { createFileRoute, redirect } from "@tanstack/react-router"

// Settings are a dialog; the page's old address opens it over Home.
export const Route = createFileRoute("/e/$env/settings/network")({
  beforeLoad: ({ params }) => {
    throw redirect({
      to: "/e/$env",
      params,
      search: { settings: "network" },
      replace: true,
    })
  },
})
