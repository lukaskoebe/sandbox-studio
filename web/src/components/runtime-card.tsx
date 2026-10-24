import { useQueryClient } from "@tanstack/react-query"
import {
  ArrowClockwiseIcon,
  CheckCircleIcon,
  CpuIcon,
  WarningIcon,
  XCircleIcon,
} from "@phosphor-icons/react"
import { Button } from "@/components/ui/button"
import { Spinner } from "@/components/ui/spinner"
import { Ident } from "@/components/ident"
import { SectionHeader } from "@/components/section-header"
import { $api, errorMessage, type HostCheck } from "@/lib/api/client"

function useDoctor() {
  return $api.useQuery("get", "/api/system/doctor", undefined, {
    staleTime: 60_000,
  })
}

/** The host checks (doctor) of the runtime settings page, with their fixes. */
export function RuntimeSection() {
  return (
    <section className="grid gap-2 text-xs">
      <SectionHeader
        title="Runtime"
        icon={CpuIcon}
        actions={<CheckAgainButton />}
      />
      <p className="text-xs/relaxed text-muted-foreground">
        Host checks for microsandbox, virtualization, the base image, disk space
        and ports. The same checks run at startup and as{" "}
        <Ident>studio doctor</Ident>.
      </p>
      <HostChecks refresh={false} />
    </section>
  )
}

/** Runs the host checks again. */
function CheckAgainButton() {
  const queryClient = useQueryClient()
  const doctor = useDoctor()
  return (
    <Button
      size="sm"
      variant="outline"
      disabled={doctor.isFetching}
      onClick={() => {
        doctor.refetch()
        // The setup's host check reads the same checks.
        queryClient.invalidateQueries({ queryKey: ["get", "/api/setup"] })
      }}
    >
      {doctor.isFetching ? <Spinner /> : <ArrowClockwiseIcon />}
      Check again
    </Button>
  )
}

/** The host checks with their fixes as a ruled list, and with `refresh` a button to run them again. */
export function HostChecks({ refresh = true }: { refresh?: boolean }) {
  const doctor = useDoctor()
  const report = doctor.data
  return (
    <div className="grid gap-2 text-xs">
      {refresh && (
        <div className="flex justify-end">
          <CheckAgainButton />
        </div>
      )}
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
    </div>
  )
}

function CheckIcon({ status }: { status: HostCheck["status"] }) {
  if (status === "ok")
    return <CheckCircleIcon className="size-4 shrink-0 text-muted-foreground" />
  if (status === "warn") return <WarningIcon className="size-4 shrink-0" />
  return <XCircleIcon className="size-4 shrink-0 text-destructive" />
}
