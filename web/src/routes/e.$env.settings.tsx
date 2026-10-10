import { createFileRoute } from "@tanstack/react-router"
import { useQueryClient } from "@tanstack/react-query"
import { toast } from "sonner"
import {
  ArrowClockwiseIcon,
  CheckCircleIcon,
  WarningIcon,
  XCircleIcon,
} from "@phosphor-icons/react"
import { Badge } from "@/components/ui/badge"
import { Button } from "@/components/ui/button"
import {
  Card,
  CardAction,
  CardContent,
  CardDescription,
  CardHeader,
  CardTitle,
} from "@/components/ui/card"
import { Checkbox } from "@/components/ui/checkbox"
import { Spinner } from "@/components/ui/spinner"
import { PageHeader } from "@/components/page-header"
import { $api, errorMessage, type HostCheck } from "@/lib/api/client"
import { formatAge } from "@/lib/utils"

export const Route = createFileRoute("/e/$env/settings")({
  component: SettingsPage,
})

// Settings apply to the whole installation, not to one environment.
function SettingsPage() {
  return (
    <>
      <PageHeader>
        <h1 className="text-sm font-medium">Settings</h1>
      </PageHeader>
      <div className="flex-1 space-y-4 overflow-auto p-4">
        <p className="max-w-2xl text-xs/relaxed text-muted-foreground">
          These settings apply to this Studio installation and every
          environment.
        </p>
        <RuntimeCard />
        <AutostartCard />
        <UpdatesCard />
      </div>
    </>
  )
}

function CheckIcon({ status }: { status: HostCheck["status"] }) {
  if (status === "ok")
    return <CheckCircleIcon className="size-4 shrink-0 text-emerald-600" />
  if (status === "warn")
    return <WarningIcon className="size-4 shrink-0 text-amber-600" />
  return <XCircleIcon className="size-4 shrink-0 text-destructive" />
}

function RuntimeCard() {
  const doctor = $api.useQuery("get", "/api/system/doctor", undefined, {
    staleTime: 60_000,
  })
  const report = doctor.data
  return (
    <Card className="max-w-3xl">
      <CardHeader>
        <CardTitle>Runtime</CardTitle>
        <CardDescription>
          Host checks for microsandbox, virtualization, the base image, disk
          space and ports. The same checks run at startup and as{" "}
          <code>studio doctor</code>.
        </CardDescription>
        <CardAction>
          <Button
            size="sm"
            variant="outline"
            disabled={doctor.isFetching}
            onClick={() => doctor.refetch()}
          >
            {doctor.isFetching ? <Spinner /> : <ArrowClockwiseIcon />}
            Check again
          </Button>
        </CardAction>
      </CardHeader>
      <CardContent className="space-y-3 text-xs">
        {doctor.error && (
          <p className="text-destructive">{errorMessage(doctor.error)}</p>
        )}
        {doctor.isPending && <Spinner className="mx-auto block" />}
        {report && (
          <ul className="space-y-2">
            {report.checks?.map((c) => (
              <li key={c.id} className="flex gap-2">
                <CheckIcon status={c.status} />
                <div className="min-w-0 space-y-0.5">
                  <p>
                    <span className="font-medium">{c.name}</span>{" "}
                    <span className="text-muted-foreground">{c.message}</span>
                  </p>
                  {c.fix && <p className="text-muted-foreground">{c.fix}</p>}
                </div>
              </li>
            ))}
          </ul>
        )}
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
