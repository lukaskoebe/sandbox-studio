import { Fragment, useState } from "react"
import { createFileRoute } from "@tanstack/react-router"
import { useQueryClient } from "@tanstack/react-query"
import { toast } from "sonner"
import { PencilSimpleIcon, PlusIcon, TrashIcon } from "@phosphor-icons/react"
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
import { Input } from "@/components/ui/input"
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select"
import { Spinner } from "@/components/ui/spinner"
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "@/components/ui/table"
import { RuleActionBadge } from "@/components/status-badge"
import { RuleDialog } from "@/components/rule-dialog"
import { $api, errorMessage, type Rule } from "@/lib/api/client"
import {
  filterRules,
  groupRules,
  ruleActions,
  ruleScopes,
  type RuleFilter,
} from "@/lib/network-rules"
import { formatAge } from "@/lib/utils"

export const Route = createFileRoute("/e/$env/network/rules")({
  component: RulesTab,
  validateSearch: (s: Record<string, unknown>): RuleFilter => ({
    q: typeof s.q === "string" && s.q !== "" ? s.q : undefined,
    scope: ruleScopes.find((v) => v === s.scope),
    action: ruleActions.find((v) => v === s.action),
  }),
})

/** Rules grouped by the domain they belong to, with search and scope and action filters. */
function RulesTab() {
  const { env } = Route.useParams()
  const filter = Route.useSearch()
  const navigate = Route.useNavigate()
  const [query, setQuery] = useState(filter.q ?? "")
  // The open dialog: an empty object adds a rule, a rule edits it, null is closed.
  const [dialog, setDialog] = useState<{ rule?: Rule } | null>(null)
  const rules = $api.useQuery("get", "/api/environments/{env}/rules", {
    params: { path: { env } },
  })
  const sandboxes = $api.useQuery("get", "/api/environments/{env}/sandboxes", {
    params: { path: { env } },
  })
  const names = new Map((sandboxes.data ?? []).map((sb) => [sb.id, sb.name]))
  const teammates = $api.useQuery("get", "/api/environments/{env}/teammates", {
    params: { path: { env } },
  })
  const teammateNames = new Map(
    (teammates.data ?? []).map((p) => [p.id, p.name])
  )
  const groups = groupRules(filterRules(rules.data ?? [], filter))

  // Filters live in the URL, so a filtered view can be bookmarked or shared.
  const setFilter = (patch: RuleFilter) =>
    void navigate({
      search: (prev) => ({ ...prev, ...patch }),
      replace: true,
    })
  const clearFilters = () => {
    setQuery("")
    setFilter({ q: undefined, scope: undefined, action: undefined })
  }

  return (
    <div className="space-y-3">
      <div className="flex flex-wrap items-center gap-2">
        <Input
          type="search"
          aria-label="Search hosts"
          placeholder="Search hosts"
          autoComplete="off"
          className="sm:w-56"
          value={query}
          onChange={(e) => {
            setQuery(e.target.value)
            setFilter({ q: e.target.value || undefined })
          }}
        />
        <FilterSelect
          label="Scope"
          value={filter.scope ?? "any"}
          options={{
            any: "Any scope",
            environment: "Environment",
            teammate: "Teammate",
            sandbox: "Sandbox",
          }}
          onChange={(v) =>
            setFilter({ scope: ruleScopes.find((s) => s === v) })
          }
        />
        <FilterSelect
          label="Action"
          value={filter.action ?? "any"}
          options={{
            any: "Any action",
            allow: "Allow",
            proxy: "Proxy",
            caddy: "Caddy",
            deny: "Deny",
          }}
          onChange={(v) =>
            setFilter({ action: ruleActions.find((a) => a === v) })
          }
        />
        <Button size="sm" className="sm:ml-auto" onClick={() => setDialog({})}>
          <PlusIcon />
          Add rule
        </Button>
      </div>

      {rules.isPending ? (
        <Spinner className="mx-auto block" />
      ) : !rules.data?.length ? (
        <Empty>
          <EmptyHeader>
            <EmptyTitle>No rules yet</EmptyTitle>
            <EmptyDescription>
              Approved connections become rules, so the same host is not asked
              about again. You can also add one here.
            </EmptyDescription>
          </EmptyHeader>
        </Empty>
      ) : !groups.length ? (
        <div className="flex flex-wrap items-center gap-2 text-xs text-muted-foreground">
          No rules match these filters.
          <Button variant="link" size="xs" onClick={clearFilters}>
            Clear filters
          </Button>
        </div>
      ) : (
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
              {groups.map((group) => (
                <Fragment key={group.domain}>
                  <TableRow className="bg-muted/50 hover:bg-muted/50">
                    <TableCell colSpan={7} className="font-medium">
                      {group.domain}{" "}
                      <span className="font-normal text-muted-foreground">
                        {group.rules.length}
                      </span>
                    </TableCell>
                  </TableRow>
                  {group.rules.map((r) => (
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
                          : r.teammateId
                            ? `Teammate ${teammateNames.get(r.teammateId) ?? ""}`
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
                </Fragment>
              ))}
            </TableBody>
          </Table>
        </div>
      )}
      <RuleDialog
        env={env}
        rule={dialog?.rule}
        sandboxes={sandboxes.data ?? []}
        open={dialog !== null}
        onOpenChange={(open) => {
          if (!open) setDialog(null)
        }}
      />
    </div>
  )
}

/** A dropdown for one filter; its "any" option clears it. */
function FilterSelect({
  label,
  value,
  options,
  onChange,
}: {
  label: string
  value: string
  options: Record<string, string>
  onChange: (value: string) => void
}) {
  return (
    <Select
      value={value}
      onValueChange={(v) => v && onChange(v)}
      items={options}
    >
      <SelectTrigger
        size="sm"
        aria-label={label}
        className="min-w-0 flex-1 sm:w-36 sm:flex-none"
      >
        <SelectValue />
      </SelectTrigger>
      <SelectContent>
        {Object.entries(options).map(([v, text]) => (
          <SelectItem key={v} value={v}>
            {text}
          </SelectItem>
        ))}
      </SelectContent>
    </Select>
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
