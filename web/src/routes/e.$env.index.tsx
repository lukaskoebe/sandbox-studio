import { useState } from "react"
import { Link, createFileRoute } from "@tanstack/react-router"
import { CubeIcon, PlusIcon, UploadSimpleIcon } from "@phosphor-icons/react"
import { Button } from "@/components/ui/button"
import {
  Card,
  CardAction,
  CardContent,
  CardDescription,
  CardHeader,
  CardTitle,
} from "@/components/ui/card"
import {
  Empty,
  EmptyContent,
  EmptyDescription,
  EmptyHeader,
  EmptyMedia,
  EmptyTitle,
} from "@/components/ui/empty"
import { ScrollArea } from "@/components/ui/scroll-area"
import { Skeleton } from "@/components/ui/skeleton"
import { CreateSandboxDialog } from "@/components/create-sandbox-dialog"
import { ImportSandboxDialog } from "@/components/import-sandbox-dialog"
import { LineageTree } from "@/components/lineage-tree"
import { PageHeader } from "@/components/page-header"
import { SandboxOwner } from "@/components/teammate-avatar"
import { StatusBadge } from "@/components/status-badge"
import { $api } from "@/lib/api/client"

export const Route = createFileRoute("/e/$env/")({ component: Overview })

function Overview() {
  const { env } = Route.useParams()
  const [creating, setCreating] = useState(false)
  const [importing, setImporting] = useState(false)
  const sandboxes = $api.useQuery(
    "get",
    "/api/environments/{env}/sandboxes",
    { params: { path: { env } } },
    { refetchInterval: 3000 }
  )

  return (
    <>
      <PageHeader>
        <h1 className="text-sm font-medium">Sandboxes</h1>
        <Button
          size="sm"
          variant="outline"
          className="ml-auto"
          onClick={() => setImporting(true)}
        >
          <UploadSimpleIcon />
          Import sandbox
        </Button>
        <Button size="sm" onClick={() => setCreating(true)}>
          <PlusIcon />
          New sandbox
        </Button>
      </PageHeader>
      <ScrollArea className="min-h-0 flex-1" viewportClassName="p-4">
        {sandboxes.isPending ? (
          <div className="grid gap-3 sm:grid-cols-2 xl:grid-cols-3">
            <Skeleton className="h-24" />
            <Skeleton className="h-24" />
          </div>
        ) : sandboxes.data?.length ? (
          <div className="grid gap-3 sm:grid-cols-2 xl:grid-cols-3">
            {sandboxes.data.map((sb) => (
              <Link
                key={sb.id}
                to="/e/$env/sandboxes/$id"
                params={{ env, id: sb.id }}
              >
                <Card className="transition-colors hover:bg-muted/50">
                  <CardHeader>
                    <CardTitle>{sb.name}</CardTitle>
                    <CardDescription>
                      {sb.cpus} CPUs · {sb.memoryMiB / 1024} GiB memory ·{" "}
                      {sb.workspaceMiB / 1024} GiB workspace
                      {sb.teammateId && (
                        <SandboxOwner
                          env={env}
                          teammateId={sb.teammateId}
                          className="mt-1.5 flex"
                        />
                      )}
                    </CardDescription>
                    <CardAction>
                      <StatusBadge sandbox={sb} />
                    </CardAction>
                  </CardHeader>
                </Card>
              </Link>
            ))}
          </div>
        ) : (
          <Empty className="h-full">
            <EmptyHeader>
              <EmptyMedia variant="icon">
                <CubeIcon />
              </EmptyMedia>
              <EmptyTitle>No sandboxes yet</EmptyTitle>
              <EmptyDescription>
                A sandbox is a Linux VM with Docker and a persistent workspace,
                where agents and you work on a project.
              </EmptyDescription>
            </EmptyHeader>
            <EmptyContent>
              <Button onClick={() => setCreating(true)}>
                <PlusIcon />
                New sandbox
              </Button>
            </EmptyContent>
          </Empty>
        )}
        {/* lineage */}
        {!!sandboxes.data?.length && (
          <Card className="mt-4">
            <CardHeader>
              <CardTitle>Family</CardTitle>
              <CardDescription>
                Where each sandbox came from, grouped by template.
              </CardDescription>
            </CardHeader>
            <CardContent>
              <LineageTree environmentId={env} compact />
            </CardContent>
          </Card>
        )}
      </ScrollArea>
      <CreateSandboxDialog
        env={env}
        open={creating}
        onOpenChange={setCreating}
      />
      <ImportSandboxDialog
        env={env}
        open={importing}
        onOpenChange={setImporting}
      />
    </>
  )
}
