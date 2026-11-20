import { createFileRoute } from "@tanstack/react-router"
import { LeadPlanPage } from "@/components/lead/plan-page"

/** One of Lead's plans: /e/$env/lead/plans/<id> (docs/lead.md#plans). */
export const Route = createFileRoute("/e/$env/lead/plans/$plan")({
  component: LeadPlan,
})

function LeadPlan() {
  const { env, plan } = Route.useParams()
  return <LeadPlanPage key={plan} env={env} plan={plan} />
}
