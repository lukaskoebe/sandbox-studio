import { useState } from "react"
import { Link } from "@tanstack/react-router"
import { KeyIcon, PencilSimpleIcon, PlusIcon } from "@phosphor-icons/react"
import { Badge } from "@/components/ui/badge"
import { Button } from "@/components/ui/button"
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "@/components/ui/table"
import { Ident } from "@/components/ident"
import { SecretDialog } from "@/components/secret-dialog"
import {
  DeleteSecret,
  SecretHosts,
  SecretName,
  editable,
  useManagedSecrets,
} from "@/components/secret-parts"
import { SectionHeader } from "@/components/section-header"
import { $api, type Secret, type Teammate } from "@/lib/api/client"
import { teammateSecrets, type TeammateSecret } from "@/lib/secrets"
import { cn } from "@/lib/utils"

/**
 * The secrets a teammate's sandboxes see: its own, its sandboxes', and the environment's it
 * inherits, each marked with where it comes from. New secrets added here are the teammate's.
 */
export function TeammateSecrets({
  env,
  teammate,
}: {
  env: string
  teammate: Teammate
}) {
  // The open dialog: an empty object adds a secret for the teammate, a secret edits it.
  const [dialog, setDialog] = useState<{ secret?: Secret } | null>(null)
  const secrets = $api.useQuery("get", "/api/environments/{env}/secrets", {
    params: { path: { env } },
  })
  const sandboxes = $api.useQuery("get", "/api/environments/{env}/sandboxes", {
    params: { path: { env } },
  })
  const managed = useManagedSecrets(env)
  const own = (sandboxes.data ?? []).filter(
    (sb) => sb.teammateId === teammate.id
  )
  const sandboxNames = new Map(own.map((sb) => [sb.id, sb.name]))
  const rows = teammateSecrets(
    secrets.data ?? [],
    teammate.id,
    new Set(sandboxNames.keys())
  )

  return (
    <section className="space-y-3" aria-label="Secrets">
      <SectionHeader
        title="Secrets"
        icon={KeyIcon}
        count={secrets.data ? rows.length : undefined}
        actions={
          <Button size="sm" variant="outline" onClick={() => setDialog({})}>
            <PlusIcon />
            Add secret
          </Button>
        }
      />
      <p className="text-xs/relaxed text-muted-foreground">
        What {teammate.name}'s sandboxes see. Its own secrets replace the
        environment's of the same name, and a sandbox's replace both. All
        secrets are in{" "}
        <Link
          to="/e/$env/settings/secrets"
          params={{ env }}
          className="underline underline-offset-4"
        >
          Settings → Secrets
        </Link>
        .
      </p>
      {!secrets.data ? null : rows.length ? (
        <div className="rounded-lg ring-1 ring-foreground/10">
          <Table>
            <TableHeader>
              <TableRow>
                <TableHead>Name</TableHead>
                <TableHead>Scope</TableHead>
                <TableHead>Hosts</TableHead>
                <TableHead>
                  <span className="sr-only">Actions</span>
                </TableHead>
              </TableRow>
            </TableHeader>
            <TableBody>
              {rows.map((row) => (
                <TableRow
                  key={row.secret.id}
                  className={cn(row.overridden && "text-muted-foreground")}
                >
                  <TableCell>
                    <span className={cn(row.overridden && "line-through")}>
                      <SecretName secret={row.secret} managed={managed} />
                    </span>
                  </TableCell>
                  <TableCell className="whitespace-normal">
                    <ScopeNote
                      row={row}
                      teammate={teammate.name}
                      sandboxes={sandboxNames}
                    />
                  </TableCell>
                  <TableCell className="whitespace-normal">
                    <SecretHosts secret={row.secret} />
                  </TableCell>
                  <TableCell>
                    {!row.inherited && editable(row.secret, managed) && (
                      <div className="flex justify-end gap-0.5">
                        <Button
                          variant="ghost"
                          size="icon-sm"
                          title="Edit secret"
                          onClick={() => setDialog({ secret: row.secret })}
                        >
                          <PencilSimpleIcon />
                        </Button>
                        <DeleteSecret env={env} secret={row.secret} />
                      </div>
                    )}
                  </TableCell>
                </TableRow>
              ))}
            </TableBody>
          </Table>
        </div>
      ) : (
        <div className="flex flex-wrap items-center gap-3 rounded-(--radius) border border-dashed px-3 py-2.5 text-xs">
          <span className="text-muted-foreground">
            No secrets reach {teammate.name}'s sandboxes yet.
          </span>
        </div>
      )}
      <SecretDialog
        env={env}
        secret={dialog?.secret}
        scope={`teammate:${teammate.id}`}
        sandboxes={sandboxes.data ?? []}
        open={dialog !== null}
        onOpenChange={(open) => {
          if (!open) setDialog(null)
        }}
      />
    </section>
  )
}

/** Where a row's secret comes from, and what it replaces or what replaces it. */
function ScopeNote({
  row,
  teammate,
  sandboxes,
}: {
  row: TeammateSecret
  teammate: string
  sandboxes: Map<string, string>
}) {
  const { scope } = row
  if (row.inherited)
    return (
      <span className="inline-flex flex-wrap items-center gap-x-1.5 gap-y-0.5">
        <span>Environment</span>
        <Badge variant="outline">Inherited</Badge>
        {row.overridden && (
          <span className="text-muted-foreground">
            replaced by {teammate}'s
          </span>
        )}
      </span>
    )
  const replaces =
    row.overrides === "teammate"
      ? `replaces ${teammate}'s`
      : row.overrides === "environment"
        ? "replaces the environment's"
        : undefined
  return (
    <span className="inline-flex flex-wrap items-center gap-x-1.5">
      {scope.kind === "sandbox" ? (
        <span>
          Sandbox <Ident>{sandboxes.get(scope.id) ?? "…"}</Ident>
        </span>
      ) : (
        <span>{teammate}</span>
      )}
      {replaces && <span className="text-muted-foreground">{replaces}</span>}
    </span>
  )
}
