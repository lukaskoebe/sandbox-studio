import { createFileRoute } from "@tanstack/react-router"
import { useQuery } from "@tanstack/react-query"
import { getJSON, type Health } from "@/lib/api"

export const Route = createFileRoute("/")({ component: Home })

function Home() {
  const health = useQuery({
    queryKey: ["health"],
    queryFn: () => getJSON<Health>("/api/health"),
  })
  return (
    <main className="flex min-h-svh flex-col gap-2 p-6 text-sm">
      <h1 className="font-medium">Sandbox Studio</h1>
      <p className="text-muted-foreground">
        {health.isPending
          ? "Connecting…"
          : health.isError
            ? `API unavailable: ${health.error.message}`
            : `API ${health.data.status} · ${health.data.version}`}
      </p>
    </main>
  )
}
