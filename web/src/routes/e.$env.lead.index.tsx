import { createFileRoute } from "@tanstack/react-router"
import { LeadDayPage } from "@/components/lead/day-page"
import { $api } from "@/lib/api/client"

/** Lead's page: today, written or so far (docs/lead.md). */
export const Route = createFileRoute("/e/$env/lead/")({
  component: LeadToday,
})

function LeadToday() {
  const { env } = Route.useParams()
  // Today is Studio's, which Lead says.
  const lead = $api.useQuery("get", "/api/environments/{env}/lead", {
    params: { path: { env } },
  })
  if (!lead.data) return null
  return <LeadDayPage env={env} day={lead.data.today.day} />
}
