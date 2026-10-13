import {
  Link,
  Outlet,
  createFileRoute,
  notFound,
  useMatchRoute,
} from "@tanstack/react-router"
import { PageHeader } from "@/components/page-header"
import { PresenceDot, TeammateAvatar } from "@/components/teammate-avatar"
import { TeammateOptionsMenu } from "@/components/teammate-options-menu"
import { ScrollArea } from "@/components/ui/scroll-area"
import { Tabs, TabsList, TabsTrigger } from "@/components/ui/tabs"
import { usePresence, presenceLabels } from "@/hooks/use-teammate-presence"
import { $api } from "@/lib/api/client"

export const Route = createFileRoute("/e/$env/teammates/$id")({
  loader: async ({ context, params }) => {
    const teammates = await context.queryClient.ensureQueryData(
      $api.queryOptions("get", "/api/environments/{env}/teammates", {
        params: { path: { env: params.env } },
      })
    )
    if (!teammates?.some((p) => p.id === params.id)) throw notFound()
  },
  component: TeammateLayout,
})

const tabs = [
  { value: "overview", label: "Overview", to: "/e/$env/teammates/$id" },
  { value: "tasks", label: "Tasks", to: "/e/$env/teammates/$id/tasks" },
  { value: "chats", label: "Chats", to: "/e/$env/teammates/$id/chats" },
  {
    value: "memory",
    label: "Memory",
    to: "/e/$env/teammates/$id/memory",
  },
  {
    value: "browser",
    label: "Browser",
    to: "/e/$env/teammates/$id/browser",
  },
  {
    value: "settings",
    label: "Settings",
    to: "/e/$env/teammates/$id/settings",
  },
] as const

/** One teammate: its header and tabs above the tab's page. Tabs scroll sideways on phones. */
function TeammateLayout() {
  const { env, id } = Route.useParams()
  const matchRoute = useMatchRoute()
  const teammates = $api.useQuery("get", "/api/environments/{env}/teammates", {
    params: { path: { env } },
  })
  const presence = usePresence(env)
  const state = presence(id)
  const teammate = teammates.data?.find((p) => p.id === id)
  const active = tabs.find((t) => matchRoute({ to: t.to, params: { env, id } }))
  if (!teammate) return null

  return (
    <>
      <PageHeader>
        <TeammateAvatar
          name={teammate.name}
          avatar={teammate.avatar}
          className="size-5 text-[0.5625rem]"
        />
        <h1 className="truncate text-sm font-medium">{teammate.name}</h1>
        <span className="flex shrink-0 items-center gap-1.5 text-xs text-muted-foreground">
          <PresenceDot presence={state} />
          {presenceLabels[state]}
        </span>
        <div className="ml-auto">
          <TeammateOptionsMenu env={env} teammate={teammate} />
        </div>
      </PageHeader>
      <div className="overflow-x-auto border-b px-3 py-2">
        <Tabs value={active?.value ?? "overview"}>
          <TabsList>
            {tabs.map((t) => (
              <TabsTrigger
                key={t.value}
                value={t.value}
                render={<Link to={t.to} params={{ env, id }} />}
              >
                {t.label}
              </TabsTrigger>
            ))}
          </TabsList>
        </Tabs>
      </div>
      <ScrollArea className="min-h-0 flex-1" viewportClassName="p-4">
        <Outlet />
      </ScrollArea>
    </>
  )
}
