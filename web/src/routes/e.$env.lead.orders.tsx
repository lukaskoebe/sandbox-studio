import { createFileRoute } from "@tanstack/react-router"
import { LeadOrdersPage } from "@/components/lead/orders-page"

/** Lead's standing orders; text starts a new order's box, as a done plan's link fills it. */
export const Route = createFileRoute("/e/$env/lead/orders")({
  validateSearch: (s: Record<string, unknown>): { text?: string } =>
    typeof s.text === "string" && s.text ? { text: s.text.slice(0, 2000) } : {},
  component: LeadOrders,
})

function LeadOrders() {
  const { env } = Route.useParams()
  const { text } = Route.useSearch()
  return <LeadOrdersPage env={env} text={text} />
}
