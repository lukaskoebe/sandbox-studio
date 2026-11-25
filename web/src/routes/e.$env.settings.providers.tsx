import { createFileRoute } from "@tanstack/react-router"
import { ProvidersSettings } from "@/components/settings/providers"

export const Route = createFileRoute("/e/$env/settings/providers")({
  // signIn opens that provider's sign-in (or its key), as a teammate that can't start links
  // here.
  validateSearch: (s: Record<string, unknown>): { signIn?: string } => ({
    signIn:
      typeof s.signIn === "string" && s.signIn !== "" ? s.signIn : undefined,
  }),
  component: Section,
})

function Section() {
  const { env } = Route.useParams()
  const { signIn } = Route.useSearch()
  const navigate = Route.useNavigate()
  return (
    <ProvidersSettings
      env={env}
      signIn={signIn}
      onSignInClosed={() => void navigate({ search: {}, replace: true })}
    />
  )
}
