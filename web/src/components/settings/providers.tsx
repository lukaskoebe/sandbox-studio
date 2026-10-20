import { useState } from "react"
import { useQueryClient } from "@tanstack/react-query"
import { toast } from "sonner"
import {
  PencilSimpleIcon,
  PlusIcon,
  SignInIcon,
  TrashIcon,
} from "@phosphor-icons/react"
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
import { Ident } from "@/components/ident"
import { EnvironmentLabel, PageHeader } from "@/components/page-header"
import { ProviderDialog } from "@/components/provider-dialog"
import { $api, errorMessage, type Provider } from "@/lib/api/client"
import { harnessLabels, isSubscription, kindLabels } from "@/lib/teammates"
import { formatAge } from "@/lib/utils"

const explanation =
  "Providers give teammates access to a model. An API key is stored as a secret bound to the provider's API host, so sandboxes see only its placeholder."

export function ProvidersSettings({ env }: { env: string }) {
  // The open dialog: an empty object adds a provider, a provider edits it, null is closed.
  const [dialog, setDialog] = useState<{ provider?: Provider } | null>(null)
  const providers = $api.useQuery("get", "/api/environments/{env}/providers", {
    params: { path: { env } },
  })
  const list = providers.data ?? []

  return (
    <>
      <PageHeader>
        <h1 className="text-sm font-medium">AI providers</h1>
        <EnvironmentLabel env={env} />
        <Button size="sm" className="ml-auto" onClick={() => setDialog({})}>
          <PlusIcon />
          Add provider
        </Button>
      </PageHeader>
      <ScrollArea className="min-h-0 flex-1" viewportClassName="space-y-6 p-4">
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
                        // A long URL is cut, not wrapped mid-word; the title has all of it.
                        <div className="grid max-w-72 font-mono text-[0.6875rem] text-muted-foreground">
                          <span className="truncate" title={p.baseUrl}>
                            {p.baseUrl}
                          </span>
                          {p.model && (
                            <span className="truncate" title={p.model}>
                              {p.model}
                            </span>
                          )}
                        </div>
                      )}
                    </TableCell>
                    <TableCell>
                      {p.state === "login_required" ? (
                        <Badge variant="outline">Sign-in needed</Badge>
                      ) : p.state === "expired" ? (
                        <Badge variant="destructive">Sign-in expired</Badge>
                      ) : p.state === "key_required" ? (
                        <Badge variant="outline">Key required</Badge>
                      ) : (
                        <div className="whitespace-normal">
                          {p.envVar && <Ident>{p.envVar}</Ident>}
                          <div className="text-muted-foreground">
                            {p.secretName && (
                              <>
                                as <Ident>{p.secretName}</Ident>
                              </>
                            )}
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
                      <div className="flex items-center justify-end gap-0.5">
                        {isSubscription(p.kind) ? (
                          <Button
                            variant={p.state === "ready" ? "ghost" : "outline"}
                            size="sm"
                            onClick={() => setDialog({ provider: p })}
                          >
                            <SignInIcon />
                            {p.state === "ready" ? "Sign in again" : "Sign in"}
                          </Button>
                        ) : (
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
      </ScrollArea>
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
            Teammates using it must be changed or deleted first.
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
