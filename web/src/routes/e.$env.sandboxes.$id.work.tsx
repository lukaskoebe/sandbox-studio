import { createFileRoute, redirect } from "@tanstack/react-router"
import { fromSetup } from "@/components/back-to-setup"
import { screenSearch } from "@/lib/computer"

/**
 * The sandbox's workspace was a page of tabs; its address now opens the computer's screen, on the
 * tab its link named (?terminal=, ?session=, ?review=, ?panel=).
 */
export const Route = createFileRoute("/e/$env/sandboxes/$id/work")({
  beforeLoad: ({ params, location }) => {
    const s = location.search as Record<string, unknown>
    throw redirect({
      to: "/e/$env/sandboxes/$id",
      params,
      search: { ...screenSearch(s), ...fromSetup(s) },
      replace: true,
    })
  },
})
