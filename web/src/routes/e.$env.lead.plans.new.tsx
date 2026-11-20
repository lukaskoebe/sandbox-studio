import { createFileRoute } from "@tanstack/react-router"
import { NewLeadPlanPage } from "@/components/lead/plan-page"

/** A new plan of Lead's; text starts the box, as a day's suggestion fills it. */
export const Route = createFileRoute("/e/$env/lead/plans/new")({
  validateSearch: (s: Record<string, unknown>): { text?: string } =>
    typeof s.text === "string" && s.text ? { text: s.text.slice(0, 2000) } : {},
  component: NewLeadPlan,
})

function NewLeadPlan() {
  const { env } = Route.useParams()
  const { text } = Route.useSearch()
  return <NewLeadPlanPage key={text ?? ""} env={env} text={text} />
}
