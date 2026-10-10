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
import { PersonaAvatar } from "@/components/persona-avatar"
import { PersonaDialog } from "@/components/persona-dialog"
import {
  $api,
  errorMessage,
  type Persona,
  type Provider,
} from "@/lib/api/client"
import { harnessLabels } from "@/lib/personas"

export const Route = createFileRoute("/e/$env/personas")({
  component: PersonasPage,
})

const explanation =
  "Personas are the agents of an environment: a name, a role and a soul, the harness they run in, the provider and model they use, and the identity they commit under. Sandboxes can belong to a persona, and network rules can be scoped to one."

function PersonasPage() {
  const { env } = Route.useParams()
  // The open dialog: an empty object adds a persona, a persona edits it, null is closed.
  const [dialog, setDialog] = useState<{ persona?: Persona } | null>(null)
  const personas = $api.useQuery("get", "/api/environments/{env}/personas", {
    params: { path: { env } },
  })
  const providers = $api.useQuery("get", "/api/environments/{env}/providers", {
    params: { path: { env } },
  })
  const list = personas.data ?? []
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
      Add persona
    </Button>
  )

  return (
    <>
      <PageHeader>
        <h1 className="text-sm font-medium">Personas</h1>
        <div className="ml-auto">{addButton}</div>
      </PageHeader>
      <div className="flex-1 space-y-6 overflow-auto p-4">
        <p className="max-w-2xl text-xs/relaxed text-muted-foreground">
          {explanation}
        </p>
        {personas.isPending || providers.isPending ? (
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
                        <PersonaAvatar name={p.name} />
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
                        persona={p}
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
                          title="Edit persona"
                          onClick={() => setDialog({ persona: p })}
                        >
                          <PencilSimpleIcon />
                        </Button>
                        <DeletePersona env={env} persona={p} />
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
              <EmptyTitle>No personas yet</EmptyTitle>
              <EmptyDescription>
                {canAdd ? (
                  explanation
                ) : (
                  <>
                    A persona needs a provider for its model.{" "}
                    <Link to="/e/$env/providers" params={{ env }}>
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
      </div>
      <PersonaDialog
        env={env}
        persona={dialog?.persona}
        providers={providerList}
        open={dialog !== null}
        onOpenChange={(open) => {
          if (!open) setDialog(null)
        }}
      />
    </>
  )
}

function DeletePersona({ env, persona }: { env: string; persona: Persona }) {
  const queryClient = useQueryClient()
  const remove = $api.useMutation(
    "delete",
    "/api/environments/{env}/personas/{id}",
    {
      onSettled: () => {
        queryClient.invalidateQueries({
          queryKey: ["get", "/api/environments/{env}/personas"],
        })
        queryClient.invalidateQueries({
          queryKey: ["get", "/api/environments/{env}/rules"],
        })
      },
      onError: (err) =>
        toast.error("Could not delete the persona", {
          description: errorMessage(err),
        }),
    }
  )

  return (
    <AlertDialog>
      <AlertDialogTrigger
        render={
          <Button variant="ghost" size="icon-sm" title="Delete persona" />
        }
      >
        <TrashIcon />
      </AlertDialogTrigger>
      <AlertDialogContent>
        <AlertDialogHeader>
          <AlertDialogTitle>Delete {persona.name}?</AlertDialogTitle>
          <AlertDialogDescription>
            Network rules scoped to {persona.name} are deleted too. Sandboxes it
            owns must be deleted first.
          </AlertDialogDescription>
        </AlertDialogHeader>
        <AlertDialogFooter>
          <AlertDialogCancel>Cancel</AlertDialogCancel>
          <AlertDialogAction
            variant="destructive"
            disabled={remove.isPending}
            onClick={() =>
              remove.mutate({ params: { path: { env, id: persona.id } } })
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
 * Whether the persona's agent sessions can start: its harness is in the base image, so it
 * comes down to the provider being able to run that harness with a key.
 */
function Readiness({
  persona,
  provider,
}: {
  persona: Persona
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
  if (!(provider.harnesses ?? []).includes(persona.harness)) {
    return (
      <Badge variant="outline">
        {provider.name} can't run {harnessLabels[persona.harness]}
      </Badge>
    )
  }
  return (
    <Badge
      variant="secondary"
      title={`${harnessLabels[persona.harness]} reads the key from ${provider.envVar}`}
    >
      Ready
    </Badge>
  )
}
