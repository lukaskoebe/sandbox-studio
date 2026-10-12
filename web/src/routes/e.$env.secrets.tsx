import { useState } from "react"
import { createFileRoute } from "@tanstack/react-router"
import { useQueryClient } from "@tanstack/react-query"
import { toast } from "sonner"
import {
  CopyIcon,
  KeyIcon,
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
import { PageHeader } from "@/components/page-header"
import { SecretDialog } from "@/components/secret-dialog"
import { SectionHeader } from "@/components/section-header"
import { TeammateAvatar } from "@/components/teammate-avatar"
import { $api, errorMessage, type Secret } from "@/lib/api/client"
import { copyText } from "@/lib/clipboard"
import { formatAge } from "@/lib/utils"

export const Route = createFileRoute("/e/$env/secrets")({
  component: SecretsPage,
})

const intro =
  "Sandboxes see each secret as an environment variable holding a placeholder, and Studio swaps in the real value only in requests to the secret's hosts."

const docsUrl = "https://lukaskoebe.github.io/sandbox-studio/secrets/"

function SecretsPage() {
  const { env } = Route.useParams()
  // The open dialog: an empty object adds a secret, a secret edits it, null is closed.
  const [dialog, setDialog] = useState<{ secret?: Secret } | null>(null)
  const secrets = $api.useQuery("get", "/api/environments/{env}/secrets", {
    params: { path: { env } },
  })
  const teammates = $api.useQuery("get", "/api/environments/{env}/teammates", {
    params: { path: { env } },
  })
  const sandboxes = $api.useQuery("get", "/api/environments/{env}/sandboxes", {
    params: { path: { env } },
  })
  const teammateNames = new Map(
    (teammates.data ?? []).map((p) => [p.id, p.name])
  )
  const sandboxNames = new Map(
    (sandboxes.data ?? []).map((sb) => [sb.id, sb.name])
  )
  const list = sortByScope(secrets.data ?? [], teammateNames, sandboxNames)
  const providers = $api.useQuery("get", "/api/environments/{env}/providers", {
    params: { path: { env } },
  })
  // Provider keys are changed and deleted in Settings → Providers.
  const managed = new Map(
    (providers.data ?? []).flatMap((p) =>
      p.secretId ? [[p.secretId, p.name] as const] : []
    )
  )

  return (
    <>
      <PageHeader>
        <h1 className="text-sm font-medium">Secrets</h1>
        <Button size="sm" className="ml-auto" onClick={() => setDialog({})}>
          <PlusIcon />
          Add secret
        </Button>
      </PageHeader>
      <ScrollArea className="min-h-0 flex-1" viewportClassName="space-y-6 p-4">
        <p className="max-w-2xl text-xs/relaxed text-muted-foreground">
          {intro}{" "}
          <a
            href={docsUrl}
            target="_blank"
            rel="noreferrer"
            className="underline underline-offset-4"
          >
            Learn more
          </a>
        </p>
        {secrets.isPending ? (
          <Spinner className="mx-auto block" />
        ) : (
          <div className="space-y-2">
            <SectionHeader title="Secrets" icon={KeyIcon} count={list.length} />
            {list.length ? (
              <div className="rounded-lg ring-1 ring-foreground/10">
                <Table>
                  <TableHeader>
                    <TableRow>
                      <TableHead>Name</TableHead>
                      <TableHead>Scope</TableHead>
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
                        <TableCell>
                          <Ident>{s.name}</Ident>
                          {managed.has(s.id) && (
                            <Badge
                              variant="secondary"
                              className="ml-1.5 font-sans"
                            >
                              Provider {managed.get(s.id)}
                            </Badge>
                          )}
                          {s.studioOnly && (
                            <Badge
                              variant="secondary"
                              className="ml-1.5 font-sans"
                            >
                              {s.name.endsWith("_SSH_KEY")
                                ? "Forge SSH key"
                                : "Forge token"}
                            </Badge>
                          )}
                        </TableCell>
                        <TableCell>
                          <ScopeCell
                            secret={s}
                            teammates={teammateNames}
                            sandboxes={sandboxNames}
                          />
                        </TableCell>
                        <TableCell>
                          <div className="flex max-w-xs flex-wrap gap-x-2 gap-y-0.5">
                            {(s.hosts ?? []).map((host) => (
                              <Ident key={host}>{host}</Ident>
                            ))}
                          </div>
                        </TableCell>
                        <TableCell>
                          <div className="flex items-center gap-1">
                            <span className="block max-w-40 truncate text-muted-foreground">
                              <Ident>{s.placeholder}</Ident>
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
                          <span className="block max-w-48 truncate">
                            {s.note}
                          </span>
                        </TableCell>
                        <TableCell className="text-muted-foreground">
                          {formatAge(s.updatedAt)}
                        </TableCell>
                        <TableCell>
                          {!managed.has(s.id) && !s.studioOnly && (
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
                          )}
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
                  <EmptyDescription>
                    Add one to let a sandbox use a credential without ever
                    seeing its value.
                  </EmptyDescription>
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
        )}
      </ScrollArea>
      <SecretDialog
        env={env}
        secret={dialog?.secret}
        sandboxes={sandboxes.data ?? []}
        open={dialog !== null}
        onOpenChange={(open) => {
          if (!open) setDialog(null)
        }}
      />
    </>
  )
}

/** Environment secrets first, then each teammate's, then each sandbox's; by name within a scope. */
function sortByScope(
  list: Secret[],
  teammates: Map<string, string>,
  sandboxes: Map<string, string>
) {
  const rank = (s: Secret) => (s.sandboxId ? 2 : s.teammateId ? 1 : 0)
  const owner = (s: Secret) =>
    (s.teammateId
      ? teammates.get(s.teammateId)
      : sandboxes.get(s.sandboxId ?? "")) ?? ""
  return [...list].sort(
    (a, b) =>
      rank(a) - rank(b) ||
      owner(a).localeCompare(owner(b)) ||
      a.name.localeCompare(b.name)
  )
}

/** Where a secret applies: the environment, one teammate's sandboxes or one sandbox. */
function ScopeCell({
  secret,
  teammates,
  sandboxes,
}: {
  secret: Secret
  teammates: Map<string, string>
  sandboxes: Map<string, string>
}) {
  if (secret.teammateId) {
    const name = teammates.get(secret.teammateId) ?? "…"
    return (
      <span
        className="inline-flex min-w-0 items-center gap-1.5"
        title="Every sandbox of this teammate"
      >
        <TeammateAvatar name={name} className="size-5 text-[0.5625rem]" />
        <span className="truncate">{name}</span>
      </span>
    )
  }
  if (secret.sandboxId) {
    return (
      <span className="block truncate" title="Only this sandbox">
        {sandboxes.get(secret.sandboxId) ?? "…"}
      </span>
    )
  }
  return <span className="text-muted-foreground">Environment</span>
}

async function copy(text: string) {
  try {
    await copyText(text)
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
