import { createFileRoute, redirect } from "@tanstack/react-router"
import { $api } from "@/lib/api/client"
import { lastEnvironment } from "@/lib/environment"

// Studio always has at least one environment; open the last one used here, or the first.
export const Route = createFileRoute("/")({
  beforeLoad: async ({ context }) => {
    const envs = await context.queryClient.ensureQueryData(
      $api.queryOptions("get", "/api/environments")
    )
    const last = lastEnvironment()
    const env = envs?.find((e) => e.id === last) ?? envs?.at(0)
    if (env)
      throw redirect({ to: "/e/$env", params: { env: env.id }, replace: true })
  },
  component: () => (
    <main className="p-6 text-sm text-muted-foreground">
      No environments yet.
    </main>
  ),
})
