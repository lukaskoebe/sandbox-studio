import { Link, createFileRoute } from "@tanstack/react-router"
import { SetupWizard } from "@/components/setup-wizard"
import { WindowBar } from "@/components/window-bar"
import { Spinner } from "@/components/ui/spinner"
import { $api } from "@/lib/api/client"
import { lastEnvironment } from "@/lib/environment"
import { setupSteps, type SetupKey } from "@/lib/setup"
import { RouteError } from "@/components/route-error"

// The guided setup. It sits outside an environment's layout: it works on the
// environment last opened here, or the first one.
export const Route = createFileRoute("/setup")({
  // The step to open, so a page the setup links to comes back to it: ?step=teammate.
  validateSearch: (s: Record<string, unknown>): { step?: SetupKey } => {
    const step = setupSteps.find((st) => st.key === s.step)?.key
    return step ? { step } : {}
  },
  // The page draws the window's bar, so its error page does too.
  errorComponent: (props) => <RouteError {...props} bar />,
  component: SetupPage,
})

function SetupPage() {
  const { step } = Route.useSearch()
  const envs = $api.useQuery("get", "/api/environments")
  const last = lastEnvironment()
  const env = envs.data?.find((e) => e.id === last) ?? envs.data?.at(0)
  return (
    <div className="flex min-h-svh flex-col">
      <WindowBar />
      <main className="mx-auto flex w-full max-w-2xl flex-1 flex-col gap-6 p-4 sm:p-8">
        <header className="flex items-center gap-3">
          <h1 className="font-heading text-base font-semibold tracking-tight">
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
          <SetupWizard env={env.id} step={step} />
        ) : envs.isPending ? (
          <Spinner className="mx-auto block" />
        ) : (
          <p className="text-xs text-muted-foreground">No environments yet.</p>
        )}
      </main>
    </div>
  )
}
