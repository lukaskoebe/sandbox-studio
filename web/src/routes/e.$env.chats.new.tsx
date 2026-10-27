import { useState } from "react"
import { createFileRoute, Link, useNavigate } from "@tanstack/react-router"
import { useQueryClient } from "@tanstack/react-query"
import { SendingMessage } from "@/components/chat/chat-items"
import { Composer, type Prompt } from "@/components/chat/composer"
import { PageHeader } from "@/components/page-header"
import { TeammateAvatar } from "@/components/teammate-avatar"
import {
  Empty,
  EmptyDescription,
  EmptyHeader,
  EmptyTitle,
} from "@/components/ui/empty"
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select"
import { Spinner } from "@/components/ui/spinner"
import { $api, errorMessage, fetchClient, isReady } from "@/lib/api/client"

export const Route = createFileRoute("/e/$env/chats/new")({
  component: NewChatPage,
  // teammate and sandbox preselect.
  validateSearch: (s: Record<string, unknown>) => {
    const search: { sandbox?: string; teammate?: string } = {}
    if (typeof s.sandbox === "string") search.sandbox = s.sandbox
    if (typeof s.teammate === "string") search.teammate = s.teammate
    return search
  },
})

/**
 * A chat before it exists: pick the sandbox, write the first message. The chat is created
 * with that message, so there are no empty chats lying around.
 */
function NewChatPage() {
  const { env } = Route.useParams()
  const search = Route.useSearch()
  const navigate = useNavigate()
  const queryClient = useQueryClient()
  const sandboxes = $api.useQuery("get", "/api/environments/{env}/sandboxes", {
    params: { path: { env } },
  })
  const teammates = $api.useQuery("get", "/api/environments/{env}/teammates", {
    params: { path: { env } },
  })
  // Chats talk to the sandbox's owner, so only owned sandboxes qualify.
  const owned = (sandboxes.data ?? []).filter((s) => s.teammateId)
  const [picked, setPicked] = useState(search.sandbox ?? "")
  // A teammate's chat goes to one of its sandboxes: the ready one, else the first.
  const teammateSandboxes = owned.filter(
    (s) => s.teammateId === search.teammate
  )
  const sandbox =
    owned.find((s) => s.id === picked) ??
    teammateSandboxes.find(isReady) ??
    teammateSandboxes.at(0) ??
    owned.find(isReady) ??
    owned.at(0)
  const owner = teammates.data?.find((t) => t.id === sandbox?.teammateId)
  const [error, setError] = useState<string>()
  // The first message shows while the chat is created and its agent starts.
  const [sending, setSending] = useState<Prompt>()

  const send = async (prompt: Prompt) => {
    if (!sandbox) return false
    setError(undefined)
    setSending(prompt)
    let created: { id: string; teammateId: string }
    try {
      const res = await fetchClient.POST(
        "/api/environments/{env}/sandboxes/{id}/chats",
        { params: { path: { env, id: sandbox.id } }, body: prompt }
      )
      if (!res.data) throw res.error
      created = res.data
    } catch (err) {
      setSending(undefined)
      setError(errorMessage(err))
      return false
    }
    void queryClient.invalidateQueries({
      queryKey: ["get", "/api/environments/{env}/chats"],
    })
    void queryClient.invalidateQueries({
      queryKey: ["get", "/api/environments/{env}/sandboxes/{id}/chats"],
    })
    void navigate({
      to: "/e/$env/teammates/$id",
      params: { env, id: created.teammateId },
      search: { thread: created.id },
      replace: true,
    })
    return true
  }

  return (
    <>
      <PageHeader>
        <h1 className="text-sm font-medium">New chat</h1>
        {owned.length > 0 && (
          <Select
            value={sandbox?.id ?? ""}
            onValueChange={(v) => {
              setPicked(v ?? "")
              setError(undefined)
            }}
            items={Object.fromEntries(owned.map((s) => [s.id, s.name]))}
          >
            <SelectTrigger size="sm" className="ml-2 w-52">
              <SelectValue />
            </SelectTrigger>
            <SelectContent>
              {owned.map((s) => (
                <SelectItem key={s.id} value={s.id}>
                  {s.name}
                </SelectItem>
              ))}
            </SelectContent>
          </Select>
        )}
      </PageHeader>
      <div className="mx-auto flex min-h-0 w-full max-w-3xl flex-1 flex-col justify-end gap-4 p-4">
        {sandboxes.isPending ? (
          <Spinner className="m-auto" />
        ) : !sandbox ? (
          <Empty className="m-auto">
            <EmptyHeader>
              <EmptyTitle>No sandbox to chat in</EmptyTitle>
              <EmptyDescription>
                Chats happen in a teammate's sandbox. Create one on the{" "}
                <Link to="/e/$env" params={{ env }} className="underline">
                  Sandboxes
                </Link>{" "}
                page and pick a teammate for it.
              </EmptyDescription>
            </EmptyHeader>
          </Empty>
        ) : (
          <>
            {sending ? (
              <SendingMessage prompt={sending} />
            ) : (
              <div className="m-auto flex flex-col items-center gap-2 text-center">
                <TeammateAvatar
                  name={owner?.name ?? "?"}
                  avatar={owner?.avatar}
                  className="size-10"
                />
                <p className="text-sm font-medium">
                  What should {owner?.name ?? "your teammate"} do?
                </p>
                <p className="max-w-md text-xs text-muted-foreground">
                  It works in {sandbox.name} on its own. You can attach files.
                </p>
                {!isReady(sandbox) && (
                  <p className="max-w-md text-xs">
                    {sandbox.name} isn't running.{" "}
                    <Link
                      to="/e/$env/sandboxes/$id"
                      params={{ env, id: sandbox.id }}
                      className="underline"
                    >
                      Start it
                    </Link>{" "}
                    before you send.
                  </p>
                )}
              </div>
            )}
            <Composer
              key={sandbox.id}
              onSend={send}
              autoFocus
              error={error}
              mentionsIn={{ env, sandbox: sandbox.id }}
            />
          </>
        )}
      </div>
    </>
  )
}
