import { useRef, useState } from "react"
import { createFileRoute } from "@tanstack/react-router"
import { ArrowClockwiseIcon, HammerIcon, PlusIcon } from "@phosphor-icons/react"
import { BuildDialog, exampleTemplateSource } from "@/components/build-dialog"
import { BuildJobSheet } from "@/components/build-job-sheet"
import { BuildCleanupStatus, BuildStatusBadge } from "@/components/build-status"
import { CreateTemplateSandboxDialog } from "@/components/create-template-sandbox-dialog"
import { PageHeader } from "@/components/page-header"
import { Button } from "@/components/ui/button"
import {
  Empty,
  EmptyContent,
  EmptyDescription,
  EmptyHeader,
  EmptyTitle,
} from "@/components/ui/empty"
import { Spinner } from "@/components/ui/spinner"
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "@/components/ui/table"
import { $api, errorMessage } from "@/lib/api/client"
import { formatAge } from "@/lib/utils"

export const Route = createFileRoute("/e/$env/builds")({
  component: BuildsPage,
})

function BuildsPage() {
  const { env } = Route.useParams()
  return <BuildsWorkspace key={env} env={env} />
}

function BuildsWorkspace({ env }: { env: string }) {
  const [selectedId, setSelectedId] = useState<string | null>(null)
  const [creatingFromTemplateId, setCreatingFromTemplateId] = useState<
    string | null
  >(null)
  const [submission, setSubmission] = useState<{
    source: string
    key: number
  } | null>(null)
  const submissionKey = useRef(0)
  const builds = $api.useQuery(
    "get",
    "/api/environments/{env}/builds",
    { params: { path: { env } } },
    { refetchInterval: 3000 }
  )

  function openSubmission(source: string) {
    submissionKey.current += 1
    setSubmission({ source, key: submissionKey.current })
  }

  return (
    <>
      <PageHeader>
        <h1 className="text-sm font-medium">Builds</h1>
        <Button
          size="sm"
          className="ml-auto"
          onClick={() => openSubmission(exampleTemplateSource)}
        >
          <PlusIcon />
          New build
        </Button>
      </PageHeader>
      <div className="flex-1 space-y-5 overflow-auto p-4">
        <p className="max-w-2xl text-xs/relaxed text-muted-foreground">
          Build reusable sandbox templates from YAML, then start fresh sandboxes
          from ready templates.
        </p>

        {builds.isPending ? (
          <Spinner className="mx-auto block" />
        ) : builds.isError && !builds.data ? (
          <Empty>
            <EmptyHeader>
              <EmptyTitle>Could not load builds</EmptyTitle>
              <EmptyDescription>{errorMessage(builds.error)}</EmptyDescription>
            </EmptyHeader>
            <EmptyContent>
              <Button variant="outline" onClick={() => void builds.refetch()}>
                <ArrowClockwiseIcon />
                Retry
              </Button>
            </EmptyContent>
          </Empty>
        ) : (
          <>
            {builds.isError && (
              <div
                role="alert"
                className="flex flex-wrap items-center gap-2 rounded-md border border-destructive/30 p-3 text-destructive"
              >
                <p className="mr-auto">
                  Could not refresh builds: {errorMessage(builds.error)}
                </p>
                <Button
                  variant="outline"
                  size="sm"
                  onClick={() => void builds.refetch()}
                >
                  <ArrowClockwiseIcon />
                  Retry
                </Button>
              </div>
            )}
            {builds.data?.length ? (
              <div className="overflow-x-auto rounded-lg ring-1 ring-foreground/10">
                <Table className="min-w-[38rem]">
                  <TableHeader>
                    <TableRow>
                      <TableHead>Build</TableHead>
                      <TableHead>Status</TableHead>
                      <TableHead>Created</TableHead>
                      <TableHead>Cleanup</TableHead>
                      <TableHead>
                        <span className="sr-only">Actions</span>
                      </TableHead>
                    </TableRow>
                  </TableHeader>
                  <TableBody>
                    {builds.data.map((job) => {
                      const createdAt = new Date(job.createdAt)
                      return (
                        <TableRow key={job.id}>
                          <TableCell className="font-mono">
                            Build {shortId(job.id)}
                          </TableCell>
                          <TableCell>
                            <BuildStatusBadge status={job.status} />
                          </TableCell>
                          <TableCell className="text-muted-foreground">
                            <time
                              dateTime={job.createdAt}
                              title={createdAt.toLocaleString()}
                            >
                              {formatAge(job.createdAt)}
                            </time>
                          </TableCell>
                          <TableCell>
                            <BuildCleanupStatus job={job} />
                          </TableCell>
                          <TableCell>
                            <div className="flex justify-end">
                              <Button
                                variant="outline"
                                size="sm"
                                aria-label={`Open build ${shortId(job.id)}`}
                                onClick={() => setSelectedId(job.id)}
                              >
                                Open
                              </Button>
                            </div>
                          </TableCell>
                        </TableRow>
                      )
                    })}
                  </TableBody>
                </Table>
              </div>
            ) : (
              <Empty>
                <EmptyHeader>
                  <EmptyTitle>No template builds yet</EmptyTitle>
                  <EmptyDescription>
                    Submit a YAML spec to build a reusable template. You can
                    review its output and source here.
                  </EmptyDescription>
                </EmptyHeader>
                <EmptyContent>
                  <Button onClick={() => openSubmission(exampleTemplateSource)}>
                    <HammerIcon />
                    Build a template
                  </Button>
                </EmptyContent>
              </Empty>
            )}
          </>
        )}
      </div>
      {selectedId && (
        <BuildJobSheet
          env={env}
          id={selectedId}
          onClose={() => setSelectedId(null)}
          onUseSpec={(source) => {
            setSelectedId(null)
            openSubmission(source)
          }}
          onCreate={(templateId) => {
            setSelectedId(null)
            setCreatingFromTemplateId(templateId)
          }}
        />
      )}
      {creatingFromTemplateId && (
        <CreateTemplateSandboxDialog
          key={creatingFromTemplateId}
          env={env}
          templateId={creatingFromTemplateId}
          open
          onOpenChange={(open) => {
            if (!open) setCreatingFromTemplateId(null)
          }}
        />
      )}
      {submission && (
        <BuildDialog
          key={submission.key}
          env={env}
          open
          source={submission.source}
          onOpenChange={(open) => {
            if (!open) setSubmission(null)
          }}
          onSubmitted={(job) => {
            setSubmission(null)
            setSelectedId(job.id)
          }}
        />
      )}
    </>
  )
}

function shortId(id: string) {
  return id.slice(0, 8)
}
