import { createFileRoute, notFound } from "@tanstack/react-router"
import {
  concepts,
  type Concept,
  type TeammateMockupSearch,
} from "@/mockups/teammate/frame"
import { TeammateMockup } from "@/mockups/teammate/page"

// Concepts for an interaction-first teammate page. Dev builds only, like every mockup.
export const Route = createFileRoute("/mockups/teammate/$concept")({
  beforeLoad: ({ params }) => {
    if (!concepts.includes(params.concept as Concept)) throw notFound()
  },
  validateSearch: (search: Record<string, unknown>): TeammateMockupSearch => ({
    theme:
      search.theme === "light" || search.theme === "dark"
        ? search.theme
        : undefined,
    presence:
      search.presence === "idle" ||
      search.presence === "working" ||
      search.presence === "waiting"
        ? search.presence
        : undefined,
    bare: search.bare === true || search.bare === "true" || undefined,
  }),
  component: function TeammateMockupRoute() {
    const { concept } = Route.useParams()
    return <TeammateMockup concept={concept as Concept} />
  },
})
