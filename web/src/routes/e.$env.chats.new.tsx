import { createFileRoute, redirect } from "@tanstack/react-router"
import { $api } from "@/lib/api/client"
import { openNewChat } from "@/lib/new-chat"
import { newThreadWith } from "@/lib/new-thread"

/**
 * New chat used to be a page of its own; a chat now starts on its teammate's home. Old links
 * land there: with a teammate or one of its computers on that home, with the composer focused;
 * without, on Chats with "Who should do it?" open, or straight at the only teammate.
 */
export const Route = createFileRoute("/e/$env/chats/new")({
  validateSearch: (s: Record<string, unknown>) => {
    const search: { sandbox?: string; teammate?: string } = {}
    if (typeof s.sandbox === "string") search.sandbox = s.sandbox
    if (typeof s.teammate === "string") search.teammate = s.teammate
    return search
  },
  loaderDeps: ({ search }) => search,
  loader: async ({ context, params, deps, preload }) => {
    if (preload) return
    const { env } = params
    let teammate = deps.teammate
    let computer: string | undefined
    if (!teammate && deps.sandbox) {
      const sandboxes = await context.queryClient.ensureQueryData(
        $api.queryOptions("get", "/api/environments/{env}/sandboxes", {
          params: { path: { env } },
        })
      )
      const sb = sandboxes?.find((s) => s.id === deps.sandbox)
      teammate = sb?.teammateId || undefined
      computer = sb?.id
    }
    if (!teammate) {
      const teammates = await context.queryClient.ensureQueryData(
        $api.queryOptions("get", "/api/environments/{env}/teammates", {
          params: { path: { env } },
        })
      )
      if (teammates?.length === 1) teammate = teammates[0].id
    }
    if (teammate) {
      newThreadWith(teammate)
      throw redirect({
        to: "/e/$env/teammates/$id",
        params: { env, id: teammate },
        search: computer ? { computer } : {},
        replace: true,
      })
    }
    openNewChat()
    throw redirect({ to: "/e/$env/chats", params: { env }, replace: true })
  },
})
