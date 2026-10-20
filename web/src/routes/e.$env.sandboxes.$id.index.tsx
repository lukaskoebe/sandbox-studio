import { createFileRoute, redirect } from "@tanstack/react-router"

/**
 * A sandbox's address opens its workspace, with the tab a link names (?terminal=, ?session=,
 * ?chat=). The dashboard it once showed is the header's details pill and ⋯ menu now.
 */
export const Route = createFileRoute("/e/$env/sandboxes/$id/")({
  beforeLoad: ({ params, search }) => {
    throw redirect({
      to: "/e/$env/sandboxes/$id/work",
      params,
      search,
      replace: true,
    })
  },
})
