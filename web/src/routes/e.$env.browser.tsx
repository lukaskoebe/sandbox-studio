import { createFileRoute, redirect } from "@tanstack/react-router"
import { $api } from "@/lib/api/client"

// Browsers live on each teammate's page now. Old links with ?teammate= open that teammate's
// browser; without one, the first teammate's, or the teammates list when there is none.
export const Route = createFileRoute("/e/$env/browser")({
  validateSearch: (search: Record<string, unknown>): { teammate?: string } =>
    typeof search.teammate === "string" ? { teammate: search.teammate } : {},
  beforeLoad: async ({ context, params, search }) => {
    const teammates = await context.queryClient.ensureQueryData(
      $api.queryOptions("get", "/api/environments/{env}/teammates", {
        params: { path: { env: params.env } },
      })
    )
    const teammate =
      teammates?.find((p) => p.id === search.teammate) ?? teammates?.at(0)
    if (teammate)
      throw redirect({
        to: "/e/$env/teammates/$id/browser",
        params: { env: params.env, id: teammate.id },
        replace: true,
      })
    throw redirect({ to: "/e/$env/teammates", params, replace: true })
  },
})
