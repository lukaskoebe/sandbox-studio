import { createFileRoute } from "@tanstack/react-router"
import { TeammateConversation } from "@/components/teammate-conversation"
import { $api } from "@/lib/api/client"

export const Route = createFileRoute("/e/$env/teammates/$id/")({
  component: TeammateHome,
})

/** The teammate's page opens on the conversation with it. */
function TeammateHome() {
  const { env, id } = Route.useParams()
  const teammates = $api.useQuery("get", "/api/environments/{env}/teammates", {
    params: { path: { env } },
  })
  const teammate = teammates.data?.find((p) => p.id === id)
  if (!teammate) return null
  return <TeammateConversation env={env} teammate={teammate} />
}
