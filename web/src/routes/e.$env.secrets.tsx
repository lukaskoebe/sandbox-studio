import { useState } from "react"
import { createFileRoute } from "@tanstack/react-router"
import { useQueryClient } from "@tanstack/react-query"
import { toast } from "sonner"
import {
  CopyIcon,
  PencilSimpleIcon,
  PlusIcon,
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
import { SecretDialog } from "@/components/secret-dialog"
import { $api, errorMessage, type Secret } from "@/lib/api/client"
import { formatAge } from "@/lib/utils"

export const Route = createFileRoute("/e/$env/secrets")({
  component: SecretsPage,
})

const explanation =
  "Sandboxes see each secret as an environment variable holding a placeholder. Studio swaps in the real value only in the headers and URLs of HTTPS requests to the secret's hosts, and masks it in their responses, so the value never enters a sandbox."

function SecretsPage() {
  const { env } = Route.useParams()
  // The open dialog: an empty object adds a secret, a secret edits it, null is closed.
  const [dialog, setDialog] = useState<{ secret?: Secret } | null>(null)
  const secrets = $api.useQuery("get", "/api/environments/{env}/secrets", {
    params: { path: { env } },
  })
  const list = secrets.data ?? []

  return (
    <>
      <PageHeader>
        <h1 className="text-sm font-medium">Secrets</h1>
        <Button size="sm" className="ml-auto" onClick={() => setDialog({})}>
          <PlusIcon />
          Add secret
        </Button>
      </PageHeader>
      <div className="flex-1 space-y-6 overflow-auto p-4">
        <p className="max-w-2xl text-xs/relaxed text-muted-foreground">
          {explanation}
        </p>
        {secrets.isPending ? (
          <Spinner className="mx-auto block" />
        ) : list.length ? (
          <div className="rounded-lg ring-1 ring-foreground/10">
            <Table>
              <TableHeader>
                <TableRow>
                  <TableHead>Name</TableHead>
                  <TableHead>Hosts</TableHead>
                  <TableHead>Placeholder</TableHead>
                  <TableHead>Note</TableHead>
                  <TableHead>Updated</TableHead>
                  <TableHead>
                    <span className="sr-only">Actions</span>
                  </TableHead>
                </TableRow>
              </TableHeader>
              <TableBody>
                {list.map((s) => (
                  <TableRow key={s.id}>
                    <TableCell className="font-mono">{s.name}</TableCell>
                    <TableCell>
                      <div className="flex max-w-xs flex-wrap gap-1">
                        {(s.hosts ?? []).map((host) => (
                          <Badge
                            key={host}
                            variant="outline"
                            className="font-mono"
                          >
                            {host}
                          </Badge>
                        ))}
                      </div>
                    </TableCell>
                    <TableCell>
                      <div className="flex items-center gap-1">
                        <span className="block max-w-40 truncate font-mono text-muted-foreground">
                          {s.placeholder}
                        </span>
                        <Button
                          variant="ghost"
                          size="icon-xs"
                          title="Copy placeholder"
                          aria-label="Copy placeholder"
                          onClick={() => copy(s.placeholder)}
                        >
                          <CopyIcon />
                        </Button>
                      </div>
                    </TableCell>
                    <TableCell className="text-muted-foreground">
                      <span className="block max-w-48 truncate">{s.note}</span>
                    </TableCell>
                    <TableCell className="text-muted-foreground">
                      {formatAge(s.updatedAt)}
                    </TableCell>
                    <TableCell>
                      <div className="flex justify-end gap-0.5">
                        <Button
                          variant="ghost"
                          size="icon-sm"
                          title="Edit secret"
                          onClick={() => setDialog({ secret: s })}
                        >
                          <PencilSimpleIcon />
                        </Button>
                        <DeleteSecret env={env} secret={s} />
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
              <EmptyTitle>No secrets yet</EmptyTitle>
              <EmptyDescription>{explanation}</EmptyDescription>
            </EmptyHeader>
            <EmptyContent>
              <Button onClick={() => setDialog({})}>
                <PlusIcon />
                Add secret
              </Button>
            </EmptyContent>
          </Empty>
        )}
      </div>
      <SecretDialog
        env={env}
        secret={dialog?.secret}
        open={dialog !== null}
        onOpenChange={(open) => {
          if (!open) setDialog(null)
        }}
      />
    </>
  )
}

async function copy(text: string) {
  try {
    await navigator.clipboard.writeText(text)
    toast.success("Copied")
  } catch {
    toast.error("Could not copy")
  }
}

function DeleteSecret({ env, secret }: { env: string; secret: Secret }) {
  const queryClient = useQueryClient()
  const remove = $api.useMutation(
    "delete",
    "/api/environments/{env}/secrets/{id}",
    {
      onSettled: () => {
        queryClient.invalidateQueries({
          queryKey: ["get", "/api/environments/{env}/secrets"],
        })
      },
      onError: (err) =>
        toast.error("Could not delete the secret", {
          description: errorMessage(err),
        }),
    }
  )

  return (
    <AlertDialog>
      <AlertDialogTrigger
        render={<Button variant="ghost" size="icon-sm" title="Delete secret" />}
      >
        <TrashIcon />
      </AlertDialogTrigger>
      <AlertDialogContent>
        <AlertDialogHeader>
          <AlertDialogTitle>Delete {secret.name}?</AlertDialogTitle>
          <AlertDialogDescription>
            Requests that use its placeholder will fail until you add it again.
          </AlertDialogDescription>
        </AlertDialogHeader>
        <AlertDialogFooter>
          <AlertDialogCancel>Cancel</AlertDialogCancel>
          <AlertDialogAction
            variant="destructive"
            disabled={remove.isPending}
            onClick={() =>
              remove.mutate({ params: { path: { env, id: secret.id } } })
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
