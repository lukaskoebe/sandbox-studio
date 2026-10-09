import { useState } from "react"
import { useQueryClient } from "@tanstack/react-query"
import { PlusIcon } from "@phosphor-icons/react"
import { Button } from "@/components/ui/button"
import {
  Empty,
  EmptyDescription,
  EmptyHeader,
  EmptyTitle,
} from "@/components/ui/empty"
import { Spinner } from "@/components/ui/spinner"
import {
  Sheet,
  SheetContent,
  SheetDescription,
  SheetHeader,
  SheetTitle,
} from "@/components/ui/sheet"
import {
  BuildCleanupStatus,
  BuildStatusBadge,
  isBuildActive,
} from "@/components/build-status"
import {
  $api,
  errorMessage,
  fetchClient,
  type BuildJob,
} from "@/lib/api/client"
import { formatAge } from "@/lib/utils"

export function BuildJobSheet({
  env,
  id,
  onClose,
  onUseSpec,
  onCreate,
}: {
  env: string
  id: string
  onClose: () => void
  onUseSpec: (source: string) => void
  onCreate: (templateId: string) => void
}) {
  const job = $api.useQuery(
    "get",
    "/api/environments/{env}/builds/{id}",
    { params: { path: { env, id } } },
    {
      refetchInterval: (query) => {
        const data = query.state.data
        return data && !isBuildActive(data.status) && !data.cleanupPending
          ? 5000
          : 1000
      },
    }
  )
  const refreshMs =
    job.data && !isBuildActive(job.data.status) && !job.data.cleanupPending
      ? 5000
      : 1000

  return (
    <Sheet open onOpenChange={(open) => !open && onClose()}>
      <SheetContent className="gap-0 overflow-y-auto data-[side=right]:sm:max-w-2xl">
        <SheetHeader className="border-b pr-12">
          <SheetTitle>Build {shortId(id)}</SheetTitle>
          <SheetDescription>
            Build job details, output, and the submitted template spec.
          </SheetDescription>
        </SheetHeader>
        {job.isPending ? (
          <div className="grid flex-1 place-items-center p-6">
            <Spinner />
          </div>
        ) : job.isError ? (
          <Empty className="m-6">
            <EmptyHeader>
              <EmptyTitle>Build unavailable</EmptyTitle>
              <EmptyDescription>{errorMessage(job.error)}</EmptyDescription>
            </EmptyHeader>
            <Button variant="outline" onClick={() => void job.refetch()}>
              Retry
            </Button>
          </Empty>
        ) : (
          <BuildJobContents
            env={env}
            id={id}
            job={job.data}
            refreshMs={refreshMs}
            onUseSpec={onUseSpec}
            onCreate={onCreate}
          />
        )}
      </SheetContent>
    </Sheet>
  )
}

function BuildJobContents({
  env,
  id,
  job,
  refreshMs,
  onUseSpec,
  onCreate,
}: {
  env: string
  id: string
  job: BuildJob
  refreshMs: number
  onUseSpec: (source: string) => void
  onCreate: (templateId: string) => void
}) {
  const queryClient = useQueryClient()
  const [cancelPending, setCancelPending] = useState(false)
  const [cancelError, setCancelError] = useState<string | null>(null)
  const log = $api.useQuery(
    "get",
    "/api/environments/{env}/builds/{id}/log",
    { params: { path: { env, id } } },
    { refetchInterval: refreshMs }
  )

  async function cancel() {
    if (cancelPending) return
    setCancelError(null)
    setCancelPending(true)
    try {
      const result = await fetchClient.POST(
        "/api/environments/{env}/builds/{id}/cancel",
        { params: { path: { env, id } } }
      )
      if (result.error) {
        setCancelError(errorMessage(result.error))
        return
      }
      await Promise.all([
        queryClient.invalidateQueries({
          queryKey: ["get", "/api/environments/{env}/builds"],
        }),
        queryClient.invalidateQueries({
          queryKey: ["get", "/api/environments/{env}/builds/{id}"],
        }),
        queryClient.invalidateQueries({
          queryKey: ["get", "/api/environments/{env}/builds/{id}/log"],
        }),
      ])
    } catch (error) {
      setCancelError(errorMessage(error))
    } finally {
      setCancelPending(false)
    }
  }

  const canCancel = isBuildActive(job.status)
  const readyTemplateId = job.status === "ready" ? job.templateId : undefined
  const updatedAt = new Date(job.updatedAt)

  return (
    <div className="space-y-6 p-4 sm:p-6">
      <section className="space-y-3" aria-label="Build status">
        <div className="flex flex-wrap items-center gap-2">
          <BuildStatusBadge status={job.status} />
          <span className="text-muted-foreground">
            Cleanup: <BuildCleanupStatus job={job} />
          </span>
          <time
            className="ml-auto text-muted-foreground"
            dateTime={job.updatedAt}
            title={updatedAt.toLocaleString()}
          >
            Updated {formatAge(job.updatedAt)}
          </time>
        </div>
        <p className="text-xs/relaxed text-muted-foreground">
          A ready template can start a new sandbox with fresh workspace and
          Docker disks.
        </p>
        {job.status === "cancelled" && (
          <p className="rounded-md bg-muted p-3 text-muted-foreground">
            The build is cancelled. Any remaining cleanup can continue in the
            background.
          </p>
        )}
        {job.cleanupPending && !isBuildActive(job.status) && (
          <p className="rounded-md bg-amber-500/10 p-3 text-amber-800 dark:text-amber-300">
            Studio is still cleaning up build resources. This can continue after
            the job status changes.
          </p>
        )}
        {job.error && (
          <div className="space-y-1 rounded-md bg-destructive/10 p-3 text-destructive">
            <h3 className="font-medium">Build error</h3>
            <p className="break-words whitespace-pre-wrap">{job.error}</p>
          </div>
        )}
        {job.cleanupError && (
          <div className="space-y-1 rounded-md bg-destructive/10 p-3 text-destructive">
            <h3 className="font-medium">Cleanup error</h3>
            <p className="break-words whitespace-pre-wrap">
              {job.cleanupError}
            </p>
          </div>
        )}
      </section>

      {readyTemplateId && (
        <section className="border-b pb-4">
          <Button onClick={() => onCreate(readyTemplateId)}>
            <PlusIcon />
            Create sandbox
          </Button>
        </section>
      )}

      {(canCancel || cancelError) && (
        <section className="space-y-2 border-y py-4">
          {canCancel && (
            <Button
              variant="destructive"
              disabled={cancelPending}
              onClick={() => void cancel()}
            >
              {cancelPending && <Spinner />}
              Cancel build
            </Button>
          )}
          <p className="text-muted-foreground">
            Cancelling updates the job status right away. Studio may still need
            to clean up the build worker afterward.
          </p>
          {cancelError && (
            <p role="alert" className="text-destructive">
              Could not cancel the build: {cancelError}
            </p>
          )}
        </section>
      )}

      <section className="space-y-2" aria-labelledby="build-log-heading">
        <div className="flex items-center gap-2">
          <h3 id="build-log-heading" className="font-medium">
            Build log
          </h3>
          {log.isFetching && (
            <span className="text-muted-foreground">Updating…</span>
          )}
        </div>
        {log.isPending ? (
          <Spinner className="mx-auto block" />
        ) : log.isError ? (
          <div className="space-y-2 rounded-md border p-3" role="alert">
            <p className="text-destructive">{errorMessage(log.error)}</p>
            <Button
              variant="outline"
              size="sm"
              onClick={() => void log.refetch()}
            >
              Retry log
            </Button>
          </div>
        ) : (
          <>
            {log.data.truncated && (
              <p className="text-amber-700 dark:text-amber-400">
                Some output was omitted because this log reached its size limit
                or output arrived too quickly.
              </p>
            )}
            <pre className="max-h-[min(55vh,36rem)] min-h-16 overflow-auto rounded-md bg-muted p-3 font-mono text-[0.6875rem]/relaxed [overflow-wrap:anywhere] break-words whitespace-pre-wrap">
              {log.data.text || "No output yet."}
            </pre>
          </>
        )}
      </section>

      <section className="space-y-2" aria-labelledby="build-source-heading">
        <div className="flex flex-wrap items-center justify-between gap-2">
          <h3 id="build-source-heading" className="font-medium">
            Template spec
          </h3>
          <Button
            variant="outline"
            size="sm"
            onClick={() => onUseSpec(job.source)}
          >
            Use spec
          </Button>
        </div>
        <pre className="max-h-80 overflow-auto rounded-md border bg-muted/40 p-3 font-mono text-[0.6875rem]/relaxed [overflow-wrap:anywhere] break-words whitespace-pre-wrap">
          {job.source}
        </pre>
      </section>
    </div>
  )
}

function shortId(id: string) {
  return id.slice(0, 8)
}
