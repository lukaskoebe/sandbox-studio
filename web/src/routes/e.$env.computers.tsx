import { createFileRoute } from "@tanstack/react-router"
import { ComputersPage } from "@/components/computers-page"

/** Every computer of the environment, grouped by base (docs/design/teammate-computer.md). */
export const Route = createFileRoute("/e/$env/computers")({
  component: Computers,
})

function Computers() {
  const { env } = Route.useParams()
  return <ComputersPage env={env} />
}
