import { useState } from "react"
import { createFileRoute } from "@tanstack/react-router"
import { useQueryClient } from "@tanstack/react-query"
import { toast } from "sonner"
import { PencilSimpleIcon, PlusIcon, TrashIcon } from "@phosphor-icons/react"
import { ApprovalDecision } from "@/components/approval-decision"
import { Button } from "@/components/ui/button"
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
import {
  Empty,
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
import { ApprovalStatusBadge, RuleActionBadge } from "@/components/status-badge"
import { PageHeader } from "@/components/page-header"
import { RuleDialog } from "@/components/rule-dialog"
import { $api, errorMessage, type Rule } from "@/lib/api/client"
import { formatAge } from "@/lib/utils"

export const Route = createFileRoute("/e/$env/network")({
  component: NetworkPage,
})

function NetworkPage() {
  const { env } = Route.useParams()
  // The open dialog: an empty object adds a rule, a rule edits it, null is closed.
  const [dialog, setDialog] = useState<{ rule?: Rule } | null>(null)
  const rules = $api.useQuery("get", "/api/environments/{env}/rules", {
    params: { path: { env } },
  })
  const sandboxes = $api.useQuery("get", "/api/environments/{env}/sandboxes", {
    params: { path: { env } },
  })
  const requests = $api.useQuery("get", "/api/environments/{env}/approvals", {
    params: { path: { env }, query: { status: "all" } },
  })
  const names = new Map((sandboxes.data ?? []).map((sb) => [sb.id, sb.name]))
  const personas = $api.useQuery("get", "/api/environments/{env}/personas", {
    params: { path: { env } },
  })
  const personaNames = new Map((personas.data ?? []).map((p) => [p.id, p.name]))

  return (
    <>
      <PageHeader>
        <h1 className="text-sm font-medium">Network</h1>
        <Button size="sm" className="ml-auto" onClick={() => setDialog({})}>
          <PlusIcon />
          Add rule
        </Button>
      </PageHeader>
      <div className="flex-1 space-y-6 overflow-auto p-4">
        <p className="max-w-2xl text-xs/relaxed text-muted-foreground">
          Sandboxes reach the internet only through Studio. Connections that no
          rule allows wait for your approval. Proxy and Caddy rules can handle
          HTTP requests; an environment-wide deny always wins.
        </p>

        <section className="space-y-2">
          <h2 className="text-xs font-medium">Rules</h2>
          {rules.isPending ? (
            <Spinner className="mx-auto block" />
          ) : rules.data?.length ? (
            <div className="rounded-lg ring-1 ring-foreground/10">
              <Table>
                <TableHeader>
                  <TableRow>
                    <TableHead>Host</TableHead>
                    <TableHead>Ports</TableHead>
                    <TableHead>Action</TableHead>
                    <TableHead>Scope</TableHead>
                    <TableHead>Note</TableHead>
                    <TableHead>Added</TableHead>
                    <TableHead>
                      <span className="sr-only">Actions</span>
                    </TableHead>
                  </TableRow>
                </TableHeader>
                <TableBody>
                  {rules.data.map((r) => (
                    <TableRow key={r.id}>
                      <TableCell className="font-mono">{r.host}</TableCell>
                      <TableCell>
                        {r.ports?.length ? r.ports.join(", ") : "any"}
                      </TableCell>
                      <TableCell>
                        <RuleAction rule={r} />
                      </TableCell>
                      <TableCell>
                        {r.sandboxId
                          ? (names.get(r.sandboxId) ?? "Other sandbox")
                          : r.personaId
                            ? `Persona ${personaNames.get(r.personaId) ?? ""}`
                            : "Environment"}
                      </TableCell>
                      <TableCell className="text-muted-foreground">
                        <span className="block max-w-48 truncate">
                          {r.note}
                        </span>
                      </TableCell>
                      <TableCell className="text-muted-foreground">
                        {formatAge(r.createdAt)}
                      </TableCell>
                      <TableCell>
                        <div className="flex justify-end gap-0.5">
                          <Button
                            variant="ghost"
                            size="icon-sm"
                            title="Edit rule"
                            onClick={() => setDialog({ rule: r })}
                          >
                            <PencilSimpleIcon />
                          </Button>
                          <DeleteRule env={env} rule={r} />
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
                <EmptyTitle>No rules yet</EmptyTitle>
                <EmptyDescription>
                  Approved connections become rules, so the same host is not
                  asked about again. You can also add one here.
                </EmptyDescription>
              </EmptyHeader>
            </Empty>
          )}
        </section>

        <section className="space-y-2">
          <h2 className="text-xs font-medium">Requests</h2>
          {requests.isPending ? (
            <Spinner className="mx-auto block" />
          ) : requests.data?.length ? (
            <ul className="divide-y rounded-lg ring-1 ring-foreground/10">
              {requests.data.slice(0, 50).map((a) => (
                <li key={a.id} className="grid gap-2 p-3">
                  <div className="flex flex-wrap items-center gap-x-2 gap-y-1 text-xs">
                    <span className="font-mono">{a.subject}</span>
                    <span className="text-muted-foreground">
                      {a.network?.sandboxName}
                    </span>
                    <ApprovalStatusBadge status={a.status} />
                    <span className="ml-auto text-muted-foreground">
                      {a.attempts} {a.attempts === 1 ? "attempt" : "attempts"} ·{" "}
                      {formatAge(a.createdAt)}
                    </span>
                  </div>
                  {a.status === "pending" && <ApprovalDecision approval={a} />}
                </li>
              ))}
            </ul>
          ) : (
            <p className="text-xs text-muted-foreground">No requests yet.</p>
          )}
        </section>
      </div>
      <RuleDialog
        env={env}
        rule={dialog?.rule}
        sandboxes={sandboxes.data ?? []}
        open={dialog !== null}
        onOpenChange={(open) => {
          if (!open) setDialog(null)
        }}
      />
    </>
  )
}

/** A rule's proxy headers or Caddyfile summary; secret values stay hidden. */
function RuleAction({ rule }: { rule: Rule }) {
  const names =
    rule.action === "proxy"
      ? Object.keys(rule.config.headers ?? {}).join(", ")
      : ""
  const caddyfile = rule.action === "caddy" ? (rule.config.caddyfile ?? "") : ""
  const caddySummary = caddyfile
    .split(/\r?\n/)
    .map((line) => line.trim())
    .find((line) => line && !line.startsWith("#"))
  const caddyLineCount = caddyfile ? caddyfile.split(/\r?\n/).length : 0
  return (
    <div className="flex items-center gap-2">
      <RuleActionBadge action={rule.action} />
      {names && (
        <span
          className="max-w-40 truncate font-mono text-xs text-muted-foreground"
          title={names}
        >
          {names}
        </span>
      )}
      {rule.action === "caddy" && (
        <>
          <span
            className="max-w-48 truncate font-mono text-xs text-muted-foreground"
            title={caddySummary}
          >
            {caddySummary ?? "Empty Caddyfile"}
          </span>
          <span className="shrink-0 text-xs text-muted-foreground">
            {caddyLineCount} {caddyLineCount === 1 ? "line" : "lines"}
          </span>
        </>
      )}
    </div>
  )
}

function DeleteRule({ env, rule }: { env: string; rule: Rule }) {
  const queryClient = useQueryClient()
  const remove = $api.useMutation(
    "delete",
    "/api/environments/{env}/rules/{id}",
    {
      onSettled: () => {
        queryClient.invalidateQueries({
          queryKey: ["get", "/api/environments/{env}/rules"],
        })
        queryClient.invalidateQueries({
          queryKey: ["get", "/api/environments/{env}/approvals"],
        })
      },
      onError: (err) =>
        toast.error("Could not delete the rule", {
          description: errorMessage(err),
        }),
    }
  )

  return (
    <AlertDialog>
      <AlertDialogTrigger
        render={<Button variant="ghost" size="icon-sm" title="Delete rule" />}
      >
        <TrashIcon />
      </AlertDialogTrigger>
      <AlertDialogContent>
        <AlertDialogHeader>
          <AlertDialogTitle>Delete the rule for {rule.host}?</AlertDialogTitle>
          <AlertDialogDescription>
            Connections it covers wait for approval again.
          </AlertDialogDescription>
        </AlertDialogHeader>
        <AlertDialogFooter>
          <AlertDialogCancel>Cancel</AlertDialogCancel>
          <AlertDialogAction
            variant="destructive"
            disabled={remove.isPending}
            onClick={() =>
              remove.mutate({ params: { path: { env, id: rule.id } } })
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
