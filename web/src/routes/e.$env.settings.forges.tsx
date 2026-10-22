import { createFileRoute } from "@tanstack/react-router"
import { GitSettings } from "@/components/settings/git"

export const Route = createFileRoute("/e/$env/settings/forges")({
  // add opens the Add forge dialog with a repository URL filled in, as a git.credential
  // card does.
  validateSearch: (s: Record<string, unknown>): { add?: string } => ({
    add: typeof s.add === "string" && s.add !== "" ? s.add : undefined,
  }),
  component: Section,
})

function Section() {
  const { env } = Route.useParams()
  const { add } = Route.useSearch()
  const navigate = Route.useNavigate()
  return (
    <GitSettings
      env={env}
      addURL={add}
      onAdded={() => void navigate({ search: {}, replace: true })}
    />
  )
}
