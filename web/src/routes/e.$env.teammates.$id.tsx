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
import { Readiness } from "@/components/teammate-readiness"
import { ScrollArea } from "@/components/ui/scroll-area"
import { Tabs, TabsList, TabsTrigger } from "@/components/ui/tabs"
import { usePresence, presenceLabels } from "@/hooks/use-teammate-presence"
import { $api } from "@/lib/api/client"
import { harnessLabels } from "@/lib/teammates"

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
  {
    value: "conversation",
    label: "Conversation",
    to: "/e/$env/teammates/$id",
  },
  { value: "tasks", label: "Tasks", to: "/e/$env/teammates/$id/tasks" },
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

/**
 * One teammate: its header (who it is, the agent it runs, what it does now) and tabs above the
 * tab's page. Its chats are listed from the conversation's rail, at /chats. Tabs scroll sideways on phones. The
 * conversation scrolls itself, with its composer at the bottom, and the browser fills the page;
 * the other tabs scroll as pages.
 */
function TeammateLayout() {
  const { env, id } = Route.useParams()
  const matchRoute = useMatchRoute()
  const teammates = $api.useQuery("get", "/api/environments/{env}/teammates", {
    params: { path: { env } },
  })
  const providers = $api.useQuery("get", "/api/environments/{env}/providers", {
    params: { path: { env } },
  })
  const presence = usePresence(env)
  const state = presence(id)
  const teammate = teammates.data?.find((p) => p.id === id)
  const active = tabs.find((t) => matchRoute({ to: t.to, params: { env, id } }))
  const chats = !!matchRoute({
    to: "/e/$env/teammates/$id/chats",
    params: { env, id },
  })
  if (!teammate) return null

  return (
    <>
      <PageHeader>
        <TeammateAvatar
          name={teammate.name}
          avatar={teammate.avatar}
          className="size-7 text-[0.6875rem]"
        />
        <div className="grid min-w-0 leading-tight">
          <h1 className="truncate font-heading text-sm font-semibold">
            {teammate.name}
          </h1>
          <span className="flex min-w-0 items-center gap-1.5 text-[0.6875rem] text-muted-foreground">
            <span className="truncate">
              {[teammate.role, harnessLabels[teammate.harness]]
                .filter(Boolean)
                .join(" · ")}
            </span>
            <span aria-hidden>·</span>
            <PresenceDot presence={state} />
            <span className="shrink-0">{presenceLabels[state]}</span>
          </span>
        </div>
        <div className="ml-auto flex shrink-0 items-center gap-2">
          <Readiness
            teammate={teammate}
            provider={providers.data?.find((p) => p.id === teammate.providerId)}
            problemsOnly
          />
          <TeammateOptionsMenu env={env} teammate={teammate} />
        </div>
      </PageHeader>
      <div className="overflow-x-auto border-b px-3 pt-1">
        <Tabs value={chats ? "" : (active?.value ?? "conversation")}>
          <TabsList variant="line" className="h-9 gap-2">
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
      {!chats &&
      ["conversation", "browser"].includes(active?.value ?? "conversation") ? (
        <Outlet />
      ) : (
        <ScrollArea className="min-h-0 flex-1" viewportClassName="p-4">
          <Outlet />
        </ScrollArea>
      )}
    </>
  )
}
