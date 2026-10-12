import { useState } from "react"
import { Link, createFileRoute } from "@tanstack/react-router"
import { useQueryClient } from "@tanstack/react-query"
import { toast } from "sonner"
import { PencilSimpleIcon, PlusIcon, TrashIcon } from "@phosphor-icons/react"
import {
  AlertDialog,
  AlertDialogAction,
  AlertDialogCancel,
  AlertDialogContent,
  AlertDialogDescription,
  AlertDialogFooter,
  AlertDialogHeader,
  AlertDialogTitle,
  AlertDialogTrigger,
} from "@/components/ui/alert-dialog"
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
import {
  $api,
  errorMessage,
  type Teammate,
  type Provider,
} from "@/lib/api/client"
import { harnessLabels } from "@/lib/teammates"

export const Route = createFileRoute("/e/$env/teammates")({
  component: TeammatesPage,
})

const explanation =
  "Teammates are the agents of an environment: a name, a role and a soul, the harness they run in, the provider and model they use, and the identity they commit under. Sandboxes can belong to a teammate, and network rules can be scoped to one."

function TeammatesPage() {
  const { env } = Route.useParams()
  // The open dialog: an empty object adds a teammate, a teammate edits it, null is closed.
  const [dialog, setDialog] = useState<{ teammate?: Teammate } | null>(null)
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
      onClick={() => setDialog({})}
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
                  <TableHead>
                    <span className="sr-only">Actions</span>
                  </TableHead>
                </TableRow>
              </TableHeader>
              <TableBody>
                {list.map((p) => (
                  <TableRow key={p.id}>
                    <TableCell>
                      <div className="flex items-center gap-2">
                        <TeammateAvatar name={p.name} />
                        <div className="min-w-0">
                          <div className="font-medium">{p.name}</div>
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
                    <TableCell>
                      <div className="flex justify-end gap-0.5">
                        <Button
                          variant="ghost"
                          size="icon-sm"
                          title="Edit teammate"
                          onClick={() => setDialog({ teammate: p })}
                        >
                          <PencilSimpleIcon />
                        </Button>
                        <DeleteTeammate env={env} teammate={p} />
                      </div>
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
        teammate={dialog?.teammate}
        providers={providerList}
        open={dialog !== null}
        onOpenChange={(open) => {
          if (!open) setDialog(null)
        }}
      />
    </>
  )
}

function DeleteTeammate({
  env,
  teammate,
}: {
  env: string
  teammate: Teammate
}) {
  const queryClient = useQueryClient()
  const remove = $api.useMutation(
    "delete",
    "/api/environments/{env}/teammates/{id}",
    {
      onSettled: () => {
        queryClient.invalidateQueries({
          queryKey: ["get", "/api/environments/{env}/teammates"],
        })
        queryClient.invalidateQueries({
          queryKey: ["get", "/api/environments/{env}/rules"],
        })
      },
      onError: (err) =>
        toast.error("Could not delete the teammate", {
          description: errorMessage(err),
        }),
    }
  )

  return (
    <AlertDialog>
      <AlertDialogTrigger
        render={
          <Button variant="ghost" size="icon-sm" title="Delete teammate" />
        }
      >
        <TrashIcon />
      </AlertDialogTrigger>
      <AlertDialogContent>
        <AlertDialogHeader>
          <AlertDialogTitle>Delete {teammate.name}?</AlertDialogTitle>
          <AlertDialogDescription>
            Network rules scoped to {teammate.name} are deleted too. Sandboxes
            it owns must be deleted first.
          </AlertDialogDescription>
        </AlertDialogHeader>
        <AlertDialogFooter>
          <AlertDialogCancel>Cancel</AlertDialogCancel>
          <AlertDialogAction
            variant="destructive"
            disabled={remove.isPending}
            onClick={() =>
              remove.mutate({ params: { path: { env, id: teammate.id } } })
            }
          >
            {remove.isPending && <Spinner />}
            Delete
          </AlertDialogAction>
        </AlertDialogFooter>
      </AlertDialogContent>
    </AlertDialog>
  )
}

/**
 * Whether the teammate's agent sessions can start: its harness is in the base image, so it
 * comes down to the provider being able to run that harness with a key.
 */
function Readiness({
  teammate,
  provider,
}: {
  teammate: Teammate
  provider?: Provider
}) {
  if (!provider) return null
  if (provider.state === "login_required") {
    return (
      <Badge
        variant="outline"
        title={`${provider.name} needs a subscription login, which Studio can't do yet`}
      >
        Login required
      </Badge>
    )
  }
  if (!(provider.harnesses ?? []).includes(teammate.harness)) {
    return (
      <Badge variant="outline">
        {provider.name} can't run {harnessLabels[teammate.harness]}
      </Badge>
    )
  }
  return (
    <Badge
      variant="secondary"
      title={`${harnessLabels[teammate.harness]} reads the key from ${provider.envVar}`}
    >
      Ready
    </Badge>
  )
}
