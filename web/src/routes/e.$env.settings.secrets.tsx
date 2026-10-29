import { useState } from "react"
import { createFileRoute } from "@tanstack/react-router"
import {
  CopyIcon,
  CubeIcon,
  GlobeSimpleIcon,
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
import { SettingsPage } from "@/components/settings/page"
import { copyPassword } from "@/components/browser-logins"
import { SecretDialog } from "@/components/secret-dialog"
import {
  DeleteSecret,
  SecretHosts,
  SecretName,
  ManagedHint,
  SecretPlaceholder,
  editable,
  useManagedSecrets,
} from "@/components/secret-parts"
import { SectionHeader } from "@/components/section-header"
import { $api, type Secret } from "@/lib/api/client"
import { groupByScope, scopeValue, type SecretGroup } from "@/lib/secrets"
import { formatAge } from "@/lib/utils"

export const Route = createFileRoute("/e/$env/settings/secrets")({
  // edit opens the dialog of the secret with that ID, as a secret.rejected card does.
  validateSearch: (s: Record<string, unknown>): { edit?: string } => ({
    edit: typeof s.edit === "string" && s.edit !== "" ? s.edit : undefined,
  }),
  component: SecretsPage,
})

const intro =
  "Sandboxes see each secret as an environment variable holding a placeholder, and Studio swaps in the real value only in requests to the secret's hosts. A teammate's or a sandbox's secret replaces the environment's of the same name there."

const docsUrl = "https://lukaskoebe.github.io/sandbox-studio/secrets/"

/** The open dialog: a scope adds a secret there, a secret edits it, null is closed. */
type DialogState = { secret?: Secret; scope?: string } | null

function SecretsPage() {
  const { env } = Route.useParams()
  const { edit } = Route.useSearch()
  const navigate = Route.useNavigate()
  const [dialog, setDialog] = useState<DialogState>(null)
  const secrets = $api.useQuery("get", "/api/environments/{env}/secrets", {
    params: { path: { env } },
  })
  // A card's link to a secret opens its dialog once, when the list has it.
  const [opened, setOpened] = useState<string>()
  const linked = edit ? secrets.data?.find((s) => s.id === edit) : undefined
  if (linked && opened !== linked.id) {
    setOpened(linked.id)
    setDialog({ secret: linked })
  }
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
      <SettingsPage
        title="Secrets"
        actions={
          <Button size="sm" onClick={() => setDialog({})}>
            <PlusIcon />
            Add secret
          </Button>
        }
        intro={
          <>
            {intro}{" "}
            <a
              href={docsUrl}
              target="_blank"
              rel="noreferrer"
              className="underline underline-offset-4"
            >
              Learn more
            </a>
          </>
        }
      >
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
      </SettingsPage>
      <SecretDialog
        env={env}
        secret={dialog?.secret}
        scope={dialog?.scope}
        sandboxes={sandboxes.data ?? []}
        open={dialog !== null}
        onOpenChange={(open) => {
          if (open) return
          setDialog(null)
          if (edit) void navigate({ search: {}, replace: true })
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
    case "browser":
      return {
        label: `${name}'s browser logins`,
        title: null,
        icon: GlobeSimpleIcon,
      }
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
  // Browser logins are saved from the teammate's browser, which offers it as a password is typed.
  const logins = group.scope.kind === "browser"
  return (
    <section className="space-y-2" aria-label={label}>
      <SectionHeader
        title={title ?? label}
        icon={icon}
        count={group.secrets.length}
        actions={
          !logins && (
            <Button
              variant="ghost"
              size="icon-sm"
              title={`Add a secret to ${label}`}
              aria-label={`Add a secret to ${label}`}
              onClick={onAdd}
            >
              <PlusIcon />
            </Button>
          )
        }
      />
      {logins && (
        <p className="text-xs/relaxed text-muted-foreground">
          Filled into pages of their hosts, and sent to hosts that ask for a
          username and password, in {group.name || "the teammate"}'s browser
          alone. No sandbox sees them.
        </p>
      )}
      <div className="rounded-lg ring-1 ring-foreground/10">
        <Table className="min-w-[40rem] table-fixed">
          <TableHeader>
            <TableRow>
              <TableHead className="w-[34%]">Name</TableHead>
              <TableHead className="w-[22%]">Hosts</TableHead>
              <TableHead>{logins ? "Username" : "Placeholder"}</TableHead>
              <TableHead className="w-24">Updated</TableHead>
              <TableHead className="sticky right-0 w-24 bg-background">
                <span className="sr-only">Actions</span>
              </TableHead>
            </TableRow>
          </TableHeader>
          <TableBody>
            {group.secrets.map((s) => (
              <TableRow key={s.id}>
                <TableCell className="wrap-anywhere whitespace-normal">
                  <SecretName secret={s} managed={managed} />
                  {/* The note under the name, where it has the room to be read. */}
                  {s.note && (
                    <p
                      className="mt-0.5 line-clamp-2 text-muted-foreground"
                      title={s.note}
                    >
                      {s.note}
                    </p>
                  )}
                </TableCell>
                <TableCell className="whitespace-normal">
                  <SecretHosts secret={s} />
                </TableCell>
                <TableCell>
                  {logins ? (
                    <div className="flex min-w-0 items-center gap-1">
                      <span className="min-w-0 truncate">
                        {s.username || (
                          <span className="text-muted-foreground">None</span>
                        )}
                      </span>
                      <Button
                        variant="ghost"
                        size="xs"
                        className="ml-auto shrink-0"
                        aria-label={`Copy the password of ${s.name}`}
                        onClick={() => void copyPassword(env, s)}
                      >
                        <CopyIcon />
                        Copy password
                      </Button>
                    </div>
                  ) : (
                    <SecretPlaceholder secret={s} />
                  )}
                </TableCell>
                <TableCell className="text-muted-foreground">
                  {formatAge(s.updatedAt)}
                </TableCell>
                <TableCell className="sticky right-0 bg-background">
                  {logins ? (
                    <div className="flex justify-end">
                      <DeleteSecret env={env} secret={s} />
                    </div>
                  ) : editable(s, managed) ? (
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
                  ) : (
                    <div className="flex justify-end">
                      <ManagedHint env={env} secret={s} managed={managed} />
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
