import { createFileRoute } from "@tanstack/react-router"
import {
  ArrowClockwiseIcon,
  CheckCircleIcon,
  CpuIcon,
  WarningIcon,
  XCircleIcon,
} from "@phosphor-icons/react"
import { Button } from "@/components/ui/button"
import { ScrollArea } from "@/components/ui/scroll-area"
import { Spinner } from "@/components/ui/spinner"
import { Ident } from "@/components/ident"
import { PageHeader } from "@/components/page-header"
import { SectionHeader } from "@/components/section-header"
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
        <RuntimeSection />
      </ScrollArea>
    </>
  )
}

function CheckIcon({ status }: { status: HostCheck["status"] }) {
  if (status === "ok")
    return <CheckCircleIcon className="size-4 shrink-0 text-muted-foreground" />
  if (status === "warn") return <WarningIcon className="size-4 shrink-0" />
  return <XCircleIcon className="size-4 shrink-0 text-destructive" />
}

function RuntimeSection() {
  const doctor = $api.useQuery("get", "/api/system/doctor", undefined, {
    staleTime: 60_000,
  })
  const report = doctor.data
  return (
    <section className="grid max-w-3xl gap-2 text-xs">
      <SectionHeader
        title="Runtime"
        icon={CpuIcon}
        actions={
          <Button
            size="sm"
            variant="outline"
            disabled={doctor.isFetching}
            onClick={() => doctor.refetch()}
          >
            {doctor.isFetching ? <Spinner /> : <ArrowClockwiseIcon />}
            Check again
          </Button>
        }
      />
      <p className="text-xs/relaxed text-muted-foreground">
        Host checks for microsandbox, virtualization, the base image, disk space
        and ports. The same checks run at startup and as{" "}
        <Ident>studio doctor</Ident>.
      </p>
      {doctor.error && (
        <p className="text-destructive">{errorMessage(doctor.error)}</p>
      )}
      {doctor.isPending && <Spinner className="mx-auto block" />}
      {report && (
        <ul className="divide-y rounded-md border">
          {report.checks?.map((c) => (
            <li key={c.id} className="flex gap-2 px-3 py-2">
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
    </section>
  )
}
