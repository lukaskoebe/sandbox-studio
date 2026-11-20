import { createFileRoute } from "@tanstack/react-router"
import { LeadDayPage } from "@/components/lead/day-page"

/** A day Lead wrote, by its date: /e/$env/lead/2026-11-19 (docs/lead.md). */
export const Route = createFileRoute("/e/$env/lead/$day")({
  component: LeadDay,
})

function LeadDay() {
  const { env, day } = Route.useParams()
  return <LeadDayPage env={env} day={day} />
}
