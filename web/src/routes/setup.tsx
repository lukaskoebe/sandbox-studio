import { Link, createFileRoute } from "@tanstack/react-router"
import { SetupWizard } from "@/components/setup-wizard"
import { Spinner } from "@/components/ui/spinner"
import { $api } from "@/lib/api/client"
import { lastEnvironment } from "@/lib/environment"

// The guided setup. It sits outside an environment's layout: it works on the
// environment last opened here, or the first one.
export const Route = createFileRoute("/setup")({
  component: SetupPage,
})

function SetupPage() {
  const envs = $api.useQuery("get", "/api/environments")
  const last = lastEnvironment()
  const env = envs.data?.find((e) => e.id === last) ?? envs.data?.at(0)
  return (
    <main className="mx-auto flex min-h-svh w-full max-w-2xl flex-col gap-6 p-4 sm:p-8">
      <header className="flex items-center gap-3">
        <h1 className="text-base font-semibold tracking-tight">
          Set up Studio
        </h1>
        {env && (
          <Link
            to="/e/$env"
            params={{ env: env.id }}
            className="ml-auto text-xs underline underline-offset-4"
          >
            Finish later
          </Link>
        )}
      </header>
      {env ? (
        <SetupWizard env={env.id} />
      ) : envs.isPending ? (
        <Spinner className="mx-auto block" />
      ) : (
        <p className="text-xs text-muted-foreground">No environments yet.</p>
      )}
    </main>
  )
}
