import { useState } from "react"
import { Link, createFileRoute } from "@tanstack/react-router"
import { PlusIcon } from "@phosphor-icons/react"
import { Badge } from "@/components/ui/badge"
import { Button } from "@/components/ui/button"
import {
  Empty,
  EmptyContent,
  EmptyDescription,
  EmptyHeader,
  EmptyTitle,
} from "@/components/ui/empty"
import { ScrollArea } from "@/components/ui/scroll-area"
import { Spinner } from "@/components/ui/spinner"
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "@/components/ui/table"
import { PageHeader } from "@/components/page-header"
import { TeammateAvatar } from "@/components/teammate-avatar"
import { TeammateDialog } from "@/components/teammate-dialog"
import { Readiness } from "@/components/teammate-readiness"
import { $api } from "@/lib/api/client"
import { harnessLabels } from "@/lib/teammates"

export const Route = createFileRoute("/e/$env/teammates/")({
  component: TeammatesPage,
})

const explanation =
  "Teammates are the agents of an environment: a name, a role and a soul, the harness they run in, the provider and model they use, and the identity they commit under. Sandboxes can belong to a teammate, and network rules can be scoped to one."

function TeammatesPage() {
  const { env } = Route.useParams()
  const [adding, setAdding] = useState(false)
  const teammates = $api.useQuery("get", "/api/environments/{env}/teammates", {
    params: { path: { env } },
  })
  const providers = $api.useQuery("get", "/api/environments/{env}/providers", {
    params: { path: { env } },
  })
  const list = teammates.data ?? []
  const providerList = providers.data ?? []
  const providerById = new Map(providerList.map((p) => [p.id, p]))
  const canAdd = providerList.length > 0

  const addButton = (
    <Button
      size="sm"
      disabled={!canAdd}
      title={canAdd ? undefined : "Add a provider first"}
      onClick={() => setAdding(true)}
    >
      <PlusIcon />
      Add a teammate
    </Button>
  )

  return (
    <>
      <PageHeader>
        <h1 className="text-sm font-medium">Teammates</h1>
        <div className="ml-auto">{addButton}</div>
      </PageHeader>
      <ScrollArea className="min-h-0 flex-1" viewportClassName="space-y-6 p-4">
        <p className="max-w-2xl text-xs/relaxed text-muted-foreground">
          {explanation}
        </p>
        {teammates.isPending || providers.isPending ? (
          <Spinner className="mx-auto block" />
        ) : list.length ? (
          <div className="rounded-lg ring-1 ring-foreground/10">
            <Table>
              <TableHeader>
                <TableRow>
                  <TableHead>Name</TableHead>
                  <TableHead>Harness</TableHead>
                  <TableHead>Provider</TableHead>
                  <TableHead>Model</TableHead>
                  <TableHead>Sessions</TableHead>
                  <TableHead>Git identity</TableHead>
                </TableRow>
              </TableHeader>
              <TableBody>
                {list.map((p) => (
                  <TableRow key={p.id}>
                    <TableCell>
                      <div className="flex items-center gap-2">
                        <TeammateAvatar name={p.name} />
                        <div className="min-w-0">
                          <Link
                            to="/e/$env/teammates/$id"
                            params={{ env, id: p.id }}
                            className="font-medium hover:underline"
                          >
                            {p.name}
                          </Link>
                          <div className="max-w-56 truncate text-muted-foreground">
                            {p.role}
                          </div>
                        </div>
                      </div>
                    </TableCell>
                    <TableCell>
                      <Badge variant="secondary">
                        {harnessLabels[p.harness]}
                      </Badge>
                    </TableCell>
                    <TableCell>
                      {providerById.get(p.providerId)?.name}
                    </TableCell>
                    <TableCell className="font-mono">
                      {p.model || (
                        <span className="text-muted-foreground">default</span>
                      )}
                    </TableCell>
                    <TableCell>
                      <Readiness
                        teammate={p}
                        provider={providerById.get(p.providerId)}
                      />
                    </TableCell>
                    <TableCell className="text-muted-foreground">
                      <span className="block max-w-56 truncate">
                        {p.gitName} &lt;{p.gitEmail}&gt;
                      </span>
                    </TableCell>
                  </TableRow>
                ))}
              </TableBody>
            </Table>
          </div>
        ) : (
          <Empty>
            <EmptyHeader>
              <EmptyTitle>No teammates yet</EmptyTitle>
              <EmptyDescription>
                {canAdd ? (
                  explanation
                ) : (
                  <>
                    A teammate needs a provider for its model.{" "}
                    <Link to="/e/$env/settings/providers" params={{ env }}>
                      Add a provider
                    </Link>{" "}
                    first.
                  </>
                )}
              </EmptyDescription>
            </EmptyHeader>
            {canAdd && <EmptyContent>{addButton}</EmptyContent>}
          </Empty>
        )}
      </ScrollArea>
      <TeammateDialog
        env={env}
        providers={providerList}
        open={adding}
        onOpenChange={setAdding}
      />
    </>
  )
}
