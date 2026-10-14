import { useState } from "react"
import { createFileRoute } from "@tanstack/react-router"
import {
  CubeIcon,
  KeyIcon,
  PencilSimpleIcon,
  PlusIcon,
  UserIcon,
} from "@phosphor-icons/react"
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
import {
  DeleteSecret,
  SecretHosts,
  SecretName,
  SecretPlaceholder,
  editable,
  useManagedSecrets,
} from "@/components/secret-parts"
import { SectionHeader } from "@/components/section-header"
import { $api, type Secret } from "@/lib/api/client"
import { groupByScope, scopeValue, type SecretGroup } from "@/lib/secrets"
import { formatAge } from "@/lib/utils"

export const Route = createFileRoute("/e/$env/secrets")({
  component: SecretsPage,
})

const intro =
  "Sandboxes see each secret as an environment variable holding a placeholder, and Studio swaps in the real value only in requests to the secret's hosts. A teammate's or a sandbox's secret replaces the environment's of the same name there."

const docsUrl = "https://lukaskoebe.github.io/sandbox-studio/secrets/"

/** The open dialog: a scope adds a secret there, a secret edits it, null is closed. */
type DialogState = { secret?: Secret; scope?: string } | null

function SecretsPage() {
  const { env } = Route.useParams()
  const [dialog, setDialog] = useState<DialogState>(null)
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
  const groups = groupByScope(secrets.data ?? [], teammateNames, sandboxNames)
  const managed = useManagedSecrets(env)

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
        ) : groups.length ? (
          groups.map((group) => (
            <ScopeSection
              key={group.key}
              env={env}
              group={group}
              managed={managed}
              onAdd={() => setDialog({ scope: scopeValue(group.scope) })}
              onEdit={(secret) => setDialog({ secret })}
            />
          ))
        ) : (
          <Empty>
            <EmptyHeader>
              <EmptyTitle>No secrets yet</EmptyTitle>
              <EmptyDescription>
                Add one to let a sandbox use a credential without ever seeing
                its value.
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
      </ScrollArea>
      <SecretDialog
        env={env}
        secret={dialog?.secret}
        scope={dialog?.scope}
        sandboxes={sandboxes.data ?? []}
        open={dialog !== null}
        onOpenChange={(open) => {
          if (!open) setDialog(null)
        }}
      />
    </>
  )
}

/** A scope's heading: the environment, a teammate or a sandbox, by name. */
function groupHeading(group: SecretGroup) {
  const name = group.name || "…"
  switch (group.scope.kind) {
    case "teammate":
      return { label: `Teammate ${name}`, title: null, icon: UserIcon }
    case "sandbox":
      return {
        label: `Sandbox ${name}`,
        // The sandbox's name is an identifier, so it is set in mono.
        title: (
          <>
            Sandbox <Ident>{name}</Ident>
          </>
        ),
        icon: CubeIcon,
      }
    default:
      return { label: "Environment", title: null, icon: KeyIcon }
  }
}

/** One scope's secrets under its heading. The columns line up from one scope to the next. */
function ScopeSection({
  env,
  group,
  managed,
  onAdd,
  onEdit,
}: {
  env: string
  group: SecretGroup
  managed: Map<string, string>
  onAdd: () => void
  onEdit: (secret: Secret) => void
}) {
  const { label, title, icon } = groupHeading(group)
  return (
    <section className="space-y-2" aria-label={label}>
      <SectionHeader
        title={title ?? label}
        icon={icon}
        count={group.secrets.length}
        actions={
          <Button
            variant="ghost"
            size="icon-sm"
            title={`Add a secret to ${label}`}
            aria-label={`Add a secret to ${label}`}
            onClick={onAdd}
          >
            <PlusIcon />
          </Button>
        }
      />
      <div className="rounded-lg ring-1 ring-foreground/10">
        <Table className="min-w-[46rem] table-fixed">
          <TableHeader>
            <TableRow>
              <TableHead className="w-[28%]">Name</TableHead>
              <TableHead className="w-[22%]">Hosts</TableHead>
              <TableHead className="w-[20%]">Placeholder</TableHead>
              <TableHead>Note</TableHead>
              <TableHead className="w-24">Updated</TableHead>
              <TableHead className="w-20">
                <span className="sr-only">Actions</span>
              </TableHead>
            </TableRow>
          </TableHeader>
          <TableBody>
            {group.secrets.map((s) => (
              <TableRow key={s.id}>
                <TableCell className="wrap-anywhere whitespace-normal">
                  <SecretName secret={s} managed={managed} />
                </TableCell>
                <TableCell className="whitespace-normal">
                  <SecretHosts secret={s} />
                </TableCell>
                <TableCell>
                  <SecretPlaceholder secret={s} />
                </TableCell>
                <TableCell className="truncate text-muted-foreground">
                  {s.note}
                </TableCell>
                <TableCell className="text-muted-foreground">
                  {formatAge(s.updatedAt)}
                </TableCell>
                <TableCell>
                  {editable(s, managed) && (
                    <div className="flex justify-end gap-0.5">
                      <Button
                        variant="ghost"
                        size="icon-sm"
                        title="Edit secret"
                        onClick={() => onEdit(s)}
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
    </section>
  )
}
