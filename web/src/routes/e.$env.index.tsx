import { useState } from "react"
import { Link, createFileRoute } from "@tanstack/react-router"
import { CubeIcon, PlusIcon } from "@phosphor-icons/react"
import { Button } from "@/components/ui/button"
import {
  Card,
  CardAction,
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
import { Skeleton } from "@/components/ui/skeleton"
import { CreateSandboxDialog } from "@/components/create-sandbox-dialog"
import { PageHeader } from "@/components/page-header"
import { StatusBadge } from "@/components/status-badge"
import { $api } from "@/lib/api/client"

export const Route = createFileRoute("/e/$env/")({ component: Overview })

function Overview() {
  const { env } = Route.useParams()
  const [creating, setCreating] = useState(false)
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
        <Button size="sm" className="ml-auto" onClick={() => setCreating(true)}>
          <PlusIcon />
          New sandbox
        </Button>
      </PageHeader>
      <div className="flex-1 overflow-auto p-4">
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
      </div>
      <CreateSandboxDialog
        env={env}
        open={creating}
        onOpenChange={setCreating}
      />
    </>
  )
}
