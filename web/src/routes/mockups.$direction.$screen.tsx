import { createFileRoute, notFound } from "@tanstack/react-router"
import { MockupScreen } from "@/mockups/screen"
import { screens, type MockupSearch, type Screen } from "@/mockups/shared"

export const Route = createFileRoute("/mockups/$direction/$screen")({
  beforeLoad: ({ params }) => {
    if (
      !["a", "b"].includes(params.direction) ||
      !screens.includes(params.screen as Screen)
    )
      throw notFound()
  },
  validateSearch: (search: Record<string, unknown>): MockupSearch => ({
    theme:
      search.theme === "light" || search.theme === "dark"
        ? search.theme
        : undefined,
    menu: search.menu === true || search.menu === "true" || undefined,
    bare: search.bare === true || search.bare === "true" || undefined,
  }),
  component: MockupRoute,
})

function MockupRoute() {
  const { direction, screen } = Route.useParams()
  return (
    <MockupScreen
      direction={direction === "b" ? "b" : "a"}
      screen={screen as Screen}
    />
  )
}
