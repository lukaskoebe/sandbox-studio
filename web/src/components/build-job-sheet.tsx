import { useState } from "react"
import { useQueryClient } from "@tanstack/react-query"
import { Link } from "@tanstack/react-router"
import {
  ArrowSquareOutIcon,
  FileTextIcon,
  PlusIcon,
  TerminalWindowIcon,
} from "@phosphor-icons/react"
import { Button } from "@/components/ui/button"
import {
  Empty,
  EmptyDescription,
  EmptyHeader,
  EmptyTitle,
} from "@/components/ui/empty"
import { ScrollArea } from "@/components/ui/scroll-area"
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
import { Ident } from "@/components/ident"
import { SectionHeader } from "@/components/section-header"
import {
  $api,
  errorMessage,
  errorStatus,
  fetchClient,
  type BuildJob,
} from "@/lib/api/client"
import {
  downloadTemplateSource,
  isTemplateSourceDownloadable,
} from "@/lib/template-source"
import { formatAge } from "@/lib/utils"

/** A build job's details, output and spec in a sheet, from the builds list. */
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
  return (
    <Sheet open onOpenChange={(open) => !open && onClose()}>
      <SheetContent className="gap-0 data-[side=right]:w-full data-[side=right]:sm:max-w-2xl">
        <SheetHeader className="border-b pr-12">
          <SheetTitle>
            Build <Ident>{shortId(id)}</Ident>
          </SheetTitle>
          <SheetDescription>
            Build job details, output, and the submitted template spec.
          </SheetDescription>
          <Button
            variant="ghost"
            size="xs"
            className="self-start"
            render={
              <Link
                to="/e/$env/builds/$id"
                params={{ env, id }}
                onClick={onClose}
              />
            }
          >
            <ArrowSquareOutIcon />
            Open as page
          </Button>
        </SheetHeader>
        <ScrollArea className="min-h-0 flex-1">
          <BuildJobView
            env={env}
            id={id}
            onUseSpec={onUseSpec}
            onCreate={onCreate}
          />
        </ScrollArea>
      </SheetContent>
    </Sheet>
  )
}

/**
 * One build job, kept current while it runs: status, errors, cancel, the log and the spec. The
 * sheet and the build's own page show it. A build that doesn't exist says so.
 */
export function BuildJobView({
  env,
  id,
  onUseSpec,
  onCreate,
}: {
  env: string
  id: string
  onUseSpec: (source: string) => void
  onCreate: (templateId: string) => void
}) {
  const job = $api.useQuery(
    "get",
    "/api/environments/{env}/builds/{id}",
    { params: { path: { env, id } } },
    {
      retry: (n, err) => errorStatus(err) !== 404 && n < 2,
      refetchInterval: (query) => {
        if (query.state.status === "error") return false
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

  if (job.isPending)
    return (
      <div className="grid h-full place-items-center p-6">
        <Spinner />
      </div>
    )
  if (job.isError) {
    const missing = errorStatus(job.error) === 404
    return (
      <Empty className="m-6 border">
        <EmptyHeader>
          <EmptyTitle>
            {missing ? "No such build" : "Build unavailable"}
          </EmptyTitle>
          <EmptyDescription>
            {missing
              ? "This build isn't in this environment. It may have been removed with its template."
              : errorMessage(job.error)}
          </EmptyDescription>
        </EmptyHeader>
        {!missing && (
          <Button variant="outline" onClick={() => void job.refetch()}>
            Retry
          </Button>
        )}
      </Empty>
    )
  }
  return (
    <BuildJobContents
      env={env}
      id={id}
      job={job.data}
      refreshMs={refreshMs}
      onUseSpec={onUseSpec}
      onCreate={onCreate}
    />
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
  const [downloadError, setDownloadError] = useState<string | null>(null)
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
          <p className="rounded-md bg-muted p-3">
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

      <section className="grid gap-2" aria-label="Build log">
        <SectionHeader
          title="Build log"
          icon={TerminalWindowIcon}
          actions={
            log.isFetching ? (
              <span className="text-muted-foreground">Updating…</span>
            ) : undefined
          }
        />
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
              <p className="text-muted-foreground">
                Some output was omitted because this log reached its size limit
                or output arrived too quickly.
              </p>
            )}
            <ScrollArea
              className="rounded-md bg-muted"
              viewportClassName="max-h-[min(55vh,36rem)] min-h-16"
            >
              <pre className="p-3 font-mono text-[0.6875rem]/relaxed [overflow-wrap:anywhere] break-words whitespace-pre-wrap">
                {log.data.text || "No output yet."}
              </pre>
            </ScrollArea>
          </>
        )}
      </section>

      <section className="grid gap-2" aria-label="Template spec">
        <SectionHeader
          title="Template spec"
          icon={FileTextIcon}
          actions={
            <>
              <Button
                variant="outline"
                size="sm"
                onClick={() => onUseSpec(job.source)}
              >
                Use spec
              </Button>
              <Button
                variant="outline"
                size="sm"
                disabled={!isTemplateSourceDownloadable(job.source)}
                onClick={() => {
                  setDownloadError(null)
                  try {
                    downloadTemplateSource(job.source)
                  } catch {
                    setDownloadError("Could not download the template spec.")
                  }
                }}
              >
                Download YAML
              </Button>
            </>
          }
        />
        {downloadError && (
          <p role="alert" className="text-destructive">
            {downloadError}
          </p>
        )}
        <ScrollArea
          className="rounded-md border bg-muted/40"
          viewportClassName="max-h-80"
        >
          <pre className="p-3 font-mono text-[0.6875rem]/relaxed [overflow-wrap:anywhere] break-words whitespace-pre-wrap">
            {job.source}
          </pre>
        </ScrollArea>
      </section>
    </div>
  )
}

export function shortId(id: string) {
  return id.slice(0, 8)
}
