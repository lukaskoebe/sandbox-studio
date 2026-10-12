import {
  Link,
  Outlet,
  createFileRoute,
  useMatchRoute,
} from "@tanstack/react-router"
import { PageHeader } from "@/components/page-header"
import { ScrollArea } from "@/components/ui/scroll-area"
import { Tabs, TabsList, TabsTrigger } from "@/components/ui/tabs"

export const Route = createFileRoute("/e/$env/network")({
  component: NetworkLayout,
})

const tabs = [
  { to: "/e/$env/network/rules", label: "Rules" },
  { to: "/e/$env/network/requests", label: "Requests" },
  { to: "/e/$env/network/activity", label: "Activity" },
] as const

/** The tabs share a header and an intro; each tab is a route of its own. */
function NetworkLayout() {
  const { env } = Route.useParams()
  const matchRoute = useMatchRoute()
  const active =
    tabs.find((t) => matchRoute({ to: t.to, params: { env } }))?.to ??
    tabs[0].to

  return (
    <>
      <PageHeader>
        <h1 className="text-sm font-medium">Network</h1>
      </PageHeader>
      <ScrollArea className="min-h-0 flex-1" viewportClassName="space-y-4 p-4">
        <p className="max-w-2xl text-xs/relaxed text-muted-foreground">
          Sandboxes reach the internet only through Studio, and connections that
          no rule allows wait for your approval.{" "}
          <a
            href="https://lukaskoebe.github.io/sandbox-studio/network/"
            target="_blank"
            rel="noreferrer"
            className="underline underline-offset-4"
          >
            Learn more
          </a>
        </p>
        <Tabs value={active}>
          <TabsList>
            {tabs.map((t) => (
              <TabsTrigger
                key={t.to}
                value={t.to}
                render={<Link to={t.to} params={{ env }} />}
              >
                {t.label}
              </TabsTrigger>
            ))}
          </TabsList>
        </Tabs>
        <Outlet />
      </ScrollArea>
    </>
  )
}
