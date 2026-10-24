import { createFileRoute, redirect } from "@tanstack/react-router"
import { teammateSearch, type TeammateSearch } from "@/lib/computer"

/**
 * A teammate's tabs before the page became a conversation beside its computer: tasks, memory
 * and settings open in its profile now, chats are its threads, and the browser is a tab of the
 * screen. Old links land there.
 */
export const Route = createFileRoute("/e/$env/teammates/$id/$view")({
  beforeLoad: ({ params, search }) => {
    const s = search as Record<string, unknown>
    const to: Record<string, TeammateSearch> = {
      tasks: { profile: "tasks" },
      memory: { profile: "memory" },
      settings: { ...teammateSearch(s), profile: "settings" },
      browser: { tab: "browser" },
    }
    throw redirect({
      to: "/e/$env/teammates/$id",
      params: { env: params.env, id: params.id },
      search: to[params.view] ?? {},
      replace: true,
    })
  },
})
