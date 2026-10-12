import { Link, createFileRoute } from "@tanstack/react-router"
import { PlusIcon } from "@phosphor-icons/react"
import { ChatTable } from "@/components/chat/chat-table"
import { Button } from "@/components/ui/button"
import {
  Empty,
  EmptyDescription,
  EmptyHeader,
  EmptyTitle,
} from "@/components/ui/empty"
import { Spinner } from "@/components/ui/spinner"
import { $api, errorMessage } from "@/lib/api/client"

export const Route = createFileRoute("/e/$env/teammates/$id/chats")({
  component: TeammateChats,
})

function TeammateChats() {
  const { env, id } = Route.useParams()
  const chats = $api.useQuery("get", "/api/environments/{env}/chats", {
    params: { path: { env } },
  })
  const teammates = $api.useQuery("get", "/api/environments/{env}/teammates", {
    params: { path: { env } },
  })
  const name = teammates.data?.find((t) => t.id === id)?.name ?? "this teammate"
  const list = (chats.data ?? []).filter((c) => c.teammateId === id)

  return (
    <div className="max-w-5xl space-y-4">
      <div className="flex items-center gap-2">
        <p className="text-sm text-muted-foreground">
          Chats with {name} about the work in its sandboxes.
        </p>
        <Button
          size="sm"
          className="ml-auto"
          render={<Link to="/e/$env/chats/new" params={{ env }} />}
        >
          <PlusIcon />
          New chat
        </Button>
      </div>
      {chats.isPending ? (
        <Spinner />
      ) : chats.isError ? (
        <p role="alert" className="text-sm text-destructive">
          {errorMessage(chats.error)}
        </p>
      ) : list.length === 0 ? (
        <Empty>
          <EmptyHeader>
            <EmptyTitle>No chats yet</EmptyTitle>
            <EmptyDescription>
              Start one to ask {name} for something.
            </EmptyDescription>
          </EmptyHeader>
        </Empty>
      ) : (
        <ChatTable env={env} chats={list} showTeammate={false} />
      )}
    </div>
  )
}
