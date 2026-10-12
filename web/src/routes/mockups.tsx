import { Outlet, createFileRoute, notFound } from "@tanstack/react-router"

// Design mockups for N4 (docs/design/ui-style.md). Dev builds only.
export const Route = createFileRoute("/mockups")({
  beforeLoad: () => {
    if (!import.meta.env.DEV) throw notFound()
  },
  component: Outlet,
})
