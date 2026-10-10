import { useState } from "react"
import { createFileRoute } from "@tanstack/react-router"
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
import { ProviderDialog } from "@/components/provider-dialog"
import { $api, errorMessage, type Provider } from "@/lib/api/client"
import { harnessLabels, isSubscription, kindLabels } from "@/lib/personas"
import { formatAge } from "@/lib/utils"

export const Route = createFileRoute("/e/$env/providers")({
  component: ProvidersPage,
})

const explanation =
  "Providers give personas access to a model. An API key is stored as a secret bound to the provider's API host, so sandboxes see only its placeholder."

function ProvidersPage() {
  const { env } = Route.useParams()
  // The open dialog: an empty object adds a provider, a provider edits it, null is closed.
  const [dialog, setDialog] = useState<{ provider?: Provider } | null>(null)
  const providers = $api.useQuery("get", "/api/environments/{env}/providers", {
    params: { path: { env } },
  })
  const list = providers.data ?? []

  return (
    <>
      <PageHeader>
        <h1 className="text-sm font-medium">Providers</h1>
        <Button size="sm" className="ml-auto" onClick={() => setDialog({})}>
          <PlusIcon />
          Add provider
        </Button>
      </PageHeader>
      <div className="flex-1 space-y-6 overflow-auto p-4">
        <p className="max-w-2xl text-xs/relaxed text-muted-foreground">
          {explanation}
        </p>
        {providers.isPending ? (
          <Spinner className="mx-auto block" />
        ) : list.length ? (
          <div className="rounded-lg ring-1 ring-foreground/10">
            <Table>
              <TableHeader>
                <TableRow>
                  <TableHead>Name</TableHead>
                  <TableHead>Kind</TableHead>
                  <TableHead>Key</TableHead>
                  <TableHead>Harnesses</TableHead>
                  <TableHead>Updated</TableHead>
                  <TableHead>
                    <span className="sr-only">Actions</span>
                  </TableHead>
                </TableRow>
              </TableHeader>
              <TableBody>
                {list.map((p) => (
                  <TableRow key={p.id}>
                    <TableCell className="font-medium">{p.name}</TableCell>
                    <TableCell>
                      <div>{kindLabels[p.kind]}</div>
                      {p.baseUrl && (
                        <div className="max-w-56 truncate font-mono text-muted-foreground">
                          {p.baseUrl} · {p.model}
                        </div>
                      )}
                    </TableCell>
                    <TableCell>
                      {p.state === "login_required" ? (
                        <Badge variant="outline">Login required</Badge>
                      ) : (
                        <div className="font-mono">
                          <div>{p.envVar}</div>
                          <div className="text-muted-foreground">
                            as {p.secretName}
                          </div>
                        </div>
                      )}
                    </TableCell>
                    <TableCell>
                      <div className="flex flex-wrap gap-1">
                        {(p.harnesses ?? []).map((h) => (
                          <Badge key={h} variant="secondary">
                            {harnessLabels[h]}
                          </Badge>
                        ))}
                      </div>
                    </TableCell>
                    <TableCell className="text-muted-foreground">
                      {formatAge(p.updatedAt)}
                    </TableCell>
                    <TableCell>
                      <div className="flex justify-end gap-0.5">
                        {!isSubscription(p.kind) && (
                          <Button
                            variant="ghost"
                            size="icon-sm"
                            title="Edit provider"
                            onClick={() => setDialog({ provider: p })}
                          >
                            <PencilSimpleIcon />
                          </Button>
                        )}
                        <DeleteProvider env={env} provider={p} />
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
              <EmptyTitle>No providers yet</EmptyTitle>
              <EmptyDescription>{explanation}</EmptyDescription>
            </EmptyHeader>
            <EmptyContent>
              <Button onClick={() => setDialog({})}>
                <PlusIcon />
                Add provider
              </Button>
            </EmptyContent>
          </Empty>
        )}
      </div>
      <ProviderDialog
        env={env}
        provider={dialog?.provider}
        open={dialog !== null}
        onOpenChange={(open) => {
          if (!open) setDialog(null)
        }}
      />
    </>
  )
}

function DeleteProvider({
  env,
  provider,
}: {
  env: string
  provider: Provider
}) {
  const queryClient = useQueryClient()
  const remove = $api.useMutation(
    "delete",
    "/api/environments/{env}/providers/{id}",
    {
      onSettled: () => {
        queryClient.invalidateQueries({
          queryKey: ["get", "/api/environments/{env}/providers"],
        })
        queryClient.invalidateQueries({
          queryKey: ["get", "/api/environments/{env}/secrets"],
        })
      },
      onError: (err) =>
        toast.error("Could not delete the provider", {
          description: errorMessage(err),
        }),
    }
  )

  return (
    <AlertDialog>
      <AlertDialogTrigger
        render={
          <Button variant="ghost" size="icon-sm" title="Delete provider" />
        }
      >
        <TrashIcon />
      </AlertDialogTrigger>
      <AlertDialogContent>
        <AlertDialogHeader>
          <AlertDialogTitle>Delete {provider.name}?</AlertDialogTitle>
          <AlertDialogDescription>
            {provider.secretName
              ? `Its key ${provider.secretName} is deleted too. `
              : ""}
            Personas using it must be changed or deleted first.
          </AlertDialogDescription>
        </AlertDialogHeader>
        <AlertDialogFooter>
          <AlertDialogCancel>Cancel</AlertDialogCancel>
          <AlertDialogAction
            variant="destructive"
            disabled={remove.isPending}
            onClick={() =>
              remove.mutate({ params: { path: { env, id: provider.id } } })
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
