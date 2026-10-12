import { createFileRoute } from "@tanstack/react-router"
import {
  ArrowClockwiseIcon,
  CheckCircleIcon,
  WarningIcon,
  XCircleIcon,
} from "@phosphor-icons/react"
import { Button } from "@/components/ui/button"
import {
  Card,
  CardAction,
  CardContent,
  CardDescription,
  CardHeader,
  CardTitle,
} from "@/components/ui/card"
import { ScrollArea } from "@/components/ui/scroll-area"
import { Spinner } from "@/components/ui/spinner"
import { PageHeader } from "@/components/page-header"
import { $api, errorMessage, type HostCheck } from "@/lib/api/client"

export const Route = createFileRoute("/e/$env/settings/runtime")({
  component: RuntimePage,
})

function RuntimePage() {
  return (
    <>
      <PageHeader>
        <h1 className="text-sm font-medium">Runtime</h1>
      </PageHeader>
      <ScrollArea className="min-h-0 flex-1" viewportClassName="space-y-4 p-4">
        <RuntimeCard />
      </ScrollArea>
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
