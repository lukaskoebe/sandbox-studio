import { createFileRoute, Link } from "@tanstack/react-router"
import { PlusIcon } from "@phosphor-icons/react"
import { ChatTable } from "@/components/chat/chat-table"
import { PageHeader } from "@/components/page-header"
import { Button } from "@/components/ui/button"
import {
  Empty,
  EmptyDescription,
  EmptyHeader,
  EmptyTitle,
} from "@/components/ui/empty"
import { ScrollArea } from "@/components/ui/scroll-area"
import { Spinner } from "@/components/ui/spinner"
import { $api, errorMessage } from "@/lib/api/client"

export const Route = createFileRoute("/e/$env/chats/")({
  component: ChatsPage,
})

function ChatsPage() {
  const { env } = Route.useParams()
  const chats = $api.useQuery("get", "/api/environments/{env}/chats", {
    params: { path: { env } },
  })
  const list = chats.data ?? []

  return (
    <>
      <PageHeader>
        <h1 className="text-sm font-medium">Chats</h1>
        <Button
          size="sm"
          className="ml-auto"
          render={<Link to="/e/$env/chats/new" params={{ env }} />}
        >
          <PlusIcon />
          New chat
        </Button>
      </PageHeader>
      <ScrollArea
        className="min-h-0 flex-1"
        viewportClassName="flex flex-col gap-4 p-4"
      >
        <p className="max-w-3xl text-sm text-muted-foreground">
          Talk to a teammate about the work in one of its sandboxes. It works in
          the sandbox and asks you before doing anything risky.
        </p>
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
                Start one to ask a teammate for something.
              </EmptyDescription>
            </EmptyHeader>
            <Button
              size="sm"
              render={<Link to="/e/$env/chats/new" params={{ env }} />}
            >
              <PlusIcon />
              New chat
            </Button>
          </Empty>
        ) : (
          <ChatTable env={env} chats={list} />
        )}
      </ScrollArea>
    </>
  )
}
