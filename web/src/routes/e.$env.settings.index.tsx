import { createFileRoute, redirect } from "@tanstack/react-router"

export const Route = createFileRoute("/e/$env/settings/")({
  beforeLoad: ({ params }) => {
    throw redirect({ to: "/e/$env/settings/general", params, replace: true })
  },
})
