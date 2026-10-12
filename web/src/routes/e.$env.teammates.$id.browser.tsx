import { createFileRoute } from "@tanstack/react-router"
import { GlobeIcon } from "@phosphor-icons/react"
import { SectionHeader } from "@/components/section-header"
import { TeammateBrowser } from "@/components/teammate-browser"

export const Route = createFileRoute("/e/$env/teammates/$id/browser")({
  component: TeammateBrowserTab,
})

const explanation =
  "Each teammate has its own browser, a separate VM that only Studio reaches. Its agents drive it with the browser tools; new sites need your approval, and logins come from the vault without the agent seeing them. Take over to drive it yourself: agent calls pause until you hand back."

function TeammateBrowserTab() {
  const { env, id } = Route.useParams()
  return (
    <div className="flex max-w-5xl flex-col gap-4">
      <SectionHeader title="Browser" icon={GlobeIcon} area="browser" />
      <p className="max-w-3xl text-sm text-muted-foreground">{explanation}</p>
      <TeammateBrowser env={env} teammate={id} />
    </div>
  )
}
