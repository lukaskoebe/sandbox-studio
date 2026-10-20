import { createFileRoute, redirect } from "@tanstack/react-router"

// The forges' old address.
export const Route = createFileRoute("/e/$env/settings/git")({
  beforeLoad: ({ params }) => {
    throw redirect({ to: "/e/$env/settings/forges", params, replace: true })
  },
})
