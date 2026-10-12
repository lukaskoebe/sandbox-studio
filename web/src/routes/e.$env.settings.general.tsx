import { createFileRoute } from "@tanstack/react-router"
import { useQueryClient } from "@tanstack/react-query"
import { toast } from "sonner"
import { Badge } from "@/components/ui/badge"
import { Button } from "@/components/ui/button"
import {
  Card,
  CardContent,
  CardDescription,
  CardHeader,
  CardTitle,
} from "@/components/ui/card"
import { Checkbox } from "@/components/ui/checkbox"
import { ScrollArea } from "@/components/ui/scroll-area"
import { AgentViewToggle } from "@/components/agent-view"
import { PageHeader } from "@/components/page-header"
import { $api, errorMessage } from "@/lib/api/client"
import { formatAge } from "@/lib/utils"

export const Route = createFileRoute("/e/$env/settings/general")({
  component: GeneralPage,
})

// Settings apply to the whole installation, not to one environment.
function GeneralPage() {
  return (
    <>
      <PageHeader>
        <h1 className="text-sm font-medium">General</h1>
      </PageHeader>
      <ScrollArea className="min-h-0 flex-1" viewportClassName="space-y-4 p-4">
        <p className="max-w-2xl text-xs/relaxed text-muted-foreground">
          These settings apply to this Studio installation and every
          environment.
        </p>
        <AgentsCard />
        <AutostartCard />
        <UpdatesCard />
      </ScrollArea>
    </>
  )
}

function AgentsCard() {
  return (
    <Card className="max-w-3xl">
      <CardHeader>
        <CardTitle>Work mode</CardTitle>
        <CardDescription>
          How you work with your teammates' agents by default: chatting, or in
          the agent's own terminal app. The other way stays one click away on
          each sandbox.
        </CardDescription>
      </CardHeader>
      <CardContent>
        <AgentViewToggle />
      </CardContent>
    </Card>
  )
}

function AutostartCard() {
  const queryClient = useQueryClient()
  const status = $api.useQuery("get", "/api/system/autostart")
  const set = $api.useMutation("put", "/api/system/autostart", {
    onSuccess: (data) =>
      queryClient.setQueryData(["get", "/api/system/autostart"], data),
    onError: (err) =>
      toast.error("Could not change autostart", {
        description: errorMessage(err),
      }),
  })
  const st = status.data
  return (
    <Card className="max-w-3xl">
      <CardHeader>
        <CardTitle>Start at login</CardTitle>
        <CardDescription>
          Starts Studio when you log in, using a user-level systemd unit,
          LaunchAgent or Run registry entry. It takes effect at your next login.
        </CardDescription>
      </CardHeader>
      <CardContent className="space-y-2 text-xs">
        {st && !st.supported ? (
          <p className="text-muted-foreground">
            Not supported on this platform.
          </p>
        ) : (
          <label className="flex items-center gap-2">
            <Checkbox
              checked={!!st?.enabled}
              disabled={!st || set.isPending}
              onCheckedChange={(enabled) => set.mutate({ body: { enabled } })}
            />
            Start Sandbox Studio at login
          </label>
        )}
        {st?.enabled && st.path && (
          <p className="text-muted-foreground">
            Entry: <code>{st.path}</code>
          </p>
        )}
        {st?.stale && (
          <p className="flex items-center gap-2 text-amber-700 dark:text-amber-500">
            The entry starts a different Studio binary.
            <Button
              size="sm"
              variant="outline"
              onClick={() => set.mutate({ body: { enabled: true } })}
            >
              Update it
            </Button>
          </p>
        )}
      </CardContent>
    </Card>
  )
}

function UpdatesCard() {
  const queryClient = useQueryClient()
  const state = $api.useQuery("get", "/api/system/updates")
  const set = $api.useMutation("put", "/api/system/updates", {
    onSuccess: (data) =>
      queryClient.setQueryData(["get", "/api/system/updates"], data),
    onError: (err) =>
      toast.error("Could not change the update check", {
        description: errorMessage(err),
      }),
  })
  const st = state.data
  return (
    <Card className="max-w-3xl">
      <CardHeader>
        <CardTitle>Updates</CardTitle>
        <CardDescription>
          Checks GitHub for a newer release at most once a day. Studio never
          downloads or installs anything by itself.
        </CardDescription>
      </CardHeader>
      <CardContent className="space-y-2 text-xs">
        <label className="flex items-center gap-2">
          <Checkbox
            checked={!!st?.enabled}
            disabled={!st || set.isPending}
            onCheckedChange={(enabled) => set.mutate({ body: { enabled } })}
          />
          Check for updates
        </label>
        {st && (
          <p className="text-muted-foreground">
            Running {st.current}
            {st.enabled && st.checkedAt && (
              <> · checked {formatAge(st.checkedAt)}</>
            )}
          </p>
        )}
        {st?.enabled && st.error && (
          <p className="text-destructive">Last check failed: {st.error}</p>
        )}
        {st?.available && (
          <p className="flex items-center gap-2">
            <Badge>Update available</Badge>
            {st.url ? (
              <a
                href={st.url}
                target="_blank"
                rel="noreferrer"
                className="underline underline-offset-4"
              >
                {st.latest}
              </a>
            ) : (
              st.latest
            )}
          </p>
        )}
      </CardContent>
    </Card>
  )
}
