import { createFileRoute, redirect } from "@tanstack/react-router"
import { $api } from "@/lib/api/client"

// Requests that wait for a decision come first; otherwise the rules.
export const Route = createFileRoute("/e/$env/network/")({
  beforeLoad: async ({ context, params }) => {
    const pending = await context.queryClient
      .fetchQuery(
        $api.queryOptions("get", "/api/environments/{env}/approvals", {
          params: { path: params, query: { status: "pending" } },
        })
      )
      .catch(() => [])
    const to = (pending ?? []).some((a) => a.kind === "network")
      ? "/e/$env/network/requests"
      : "/e/$env/network/rules"
    throw redirect({ to, params, replace: true })
  },
})
