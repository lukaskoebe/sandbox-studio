import { Fragment, useEffect, useState, type ReactNode } from "react"
import { createFileRoute, Link } from "@tanstack/react-router"
import { useQueryClient } from "@tanstack/react-query"
import { toast } from "sonner"
import {
  PencilSimpleIcon,
  PlusIcon,
  ShieldCheckIcon,
  TrashIcon,
  WarningIcon,
} from "@phosphor-icons/react"
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
import { Checkbox } from "@/components/ui/checkbox"
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
import { Ident } from "@/components/ident"
import { RuleDialog } from "@/components/rule-dialog"
import { TrackerBlocking } from "@/components/tracker-blocking"
import { CuratedList } from "@/components/curated-list"
import { LevelSection } from "@/components/security-level"
import { SectionHeader } from "@/components/section-header"
import { $api, errorMessage, type Rule } from "@/lib/api/client"
import {
  expiryText,
  filterRules,
  groupRules,
  isUnused,
  ruleActions,
  ruleOrigins,
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
    origin: ruleOrigins.find((v) => v === s.origin),
    // The URL holds 1; JSON parsing reads it back as a number, so accept the string too.
    unused: s.unused === 1 || s.unused === "1" ? 1 : undefined,
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
  const now = useNow()
  const shown = filterRules(rules.data ?? [], filter, now)
  const groups = groupRules(shown)
  const unused = shown.filter((r) => isUnused(r, now))

  // Filters live in the URL, so a filtered view can be bookmarked or shared.
  const setFilter = (patch: RuleFilter) =>
    void navigate({
      search: (prev) => ({ ...prev, ...patch }),
      replace: true,
    })
  const clearFilters = () => {
    setQuery("")
    setFilter({
      q: undefined,
      scope: undefined,
      action: undefined,
      origin: undefined,
      unused: undefined,
    })
  }

  return (
    <div className="space-y-3">
      <LevelSection env={env} />
      <SectionHeader
        title="Rules"
        icon={ShieldCheckIcon}
        area="network"
        count={rules.data ? shown.length : undefined}
        actions={
          <Button size="sm" onClick={() => setDialog({})}>
            <PlusIcon />
            Add rule
          </Button>
        }
      />
      <div className="flex flex-wrap items-center gap-2">
        <Input
          type="search"
          aria-label="Search hosts"
          placeholder="Search hosts"
          autoComplete="off"
          className="h-6 sm:w-56"
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
        <FilterSelect
          label="Origin"
          value={filter.origin ?? "any"}
          options={{
            any: "Any origin",
            manual: "Manual",
            approval: "Approval",
            curated: "Curated",
            reviewer: "Reviewer",
          }}
          onChange={(v) =>
            setFilter({ origin: ruleOrigins.find((o) => o === v) })
          }
        />
        <label className="flex items-center gap-2 text-xs">
          <Checkbox
            checked={filter.unused === 1}
            onCheckedChange={(on) => setFilter({ unused: on ? 1 : undefined })}
          />
          Unused
        </label>
        {filter.unused === 1 && unused.length > 0 && (
          <DeleteUnused env={env} rules={unused} />
        )}
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
                <TableHead>Origin</TableHead>
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
                    <TableCell colSpan={8}>
                      <span className="inline-flex items-center gap-2">
                        <Ident className="font-medium">{group.domain}</Ident>
                        <span className="font-mono text-muted-foreground">
                          {group.rules.length}
                        </span>
                      </span>
                    </TableCell>
                  </TableRow>
                  {group.rules.map((r) => (
                    <TableRow key={r.id}>
                      <TableCell>
                        <span className="inline-flex max-w-full items-center gap-1.5">
                          <Ident>{r.host}</Ident>
                          {(r.method || r.pathPrefix) && (
                            <RuleChip>
                              {`${r.method || "any method"} ${r.pathPrefix || "any path"}`}
                            </RuleChip>
                          )}
                          {isUnused(r, now) && <RuleChip>unused</RuleChip>}
                        </span>
                        {r.unreachable && (
                          <Unreachable env={env} reason={r.unreachable} />
                        )}
                      </TableCell>
                      <TableCell>
                        {r.ports?.length ? (
                          <Ident>{r.ports.join(", ")}</Ident>
                        ) : (
                          <span className="text-muted-foreground">any</span>
                        )}
                      </TableCell>
                      <TableCell>
                        <RuleAction rule={r} />
                      </TableCell>
                      <TableCell>
                        <RuleChip>{r.origin}</RuleChip>
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
                        <ExpiryNote rule={r} now={now} />
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
      <CuratedList env={env} />
      <TrackerBlocking env={env} />
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

/** Why no sandbox reaches a rule's connections, with the way to the settings that change it. */
function Unreachable({ env, reason }: { env: string; reason: string }) {
  return (
    <p className="mt-1 flex max-w-96 gap-1.5 text-[0.6875rem]/relaxed whitespace-normal text-amber-700 dark:text-amber-400">
      <WarningIcon className="mt-0.5 size-3 shrink-0" aria-hidden />
      <span>
        {reason}{" "}
        {reason.includes("Settings → Network") && (
          <Link
            to="/e/$env/settings/network"
            params={{ env }}
            className="underline underline-offset-2"
          >
            Open network settings
          </Link>
        )}
      </span>
    </p>
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

/** The current time, refreshed every minute, for text that counts down. */
function useNow(intervalMs = 60_000) {
  const [now, setNow] = useState(() => Date.now())
  useEffect(() => {
    const id = setInterval(() => setNow(Date.now()), intervalMs)
    return () => clearInterval(id)
  }, [intervalMs])
  return now
}

/** When a rule stops matching, counted down; nothing for a rule that never expires. */
function ExpiryNote({ rule, now }: { rule: Rule; now: number }) {
  const text = expiryText(rule, now)
  return text ? <span className="block">{text}</span> : null
}

/** A small monospace label on a rule: its action, origin or a flag. */
function RuleChip({ children }: { children: ReactNode }) {
  return (
    <span className="inline-flex h-[1.125rem] shrink-0 items-center rounded-[3px] bg-muted px-1 font-mono text-[0.6875rem] text-foreground/80 ring-1 ring-border ring-inset">
      {children}
    </span>
  )
}

/** A rule's action, then its proxy headers or Caddyfile summary; secret values stay hidden. */
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
    <div className="flex min-w-0 items-center gap-2">
      <RuleChip>{rule.action}</RuleChip>
      {names && (
        <span className="block max-w-40 truncate" title={names}>
          <Ident>{names}</Ident>
        </span>
      )}
      {rule.action === "caddy" && (
        <>
          <span className="block max-w-48 truncate" title={caddySummary}>
            {caddySummary ? <Ident>{caddySummary}</Ident> : "Empty Caddyfile"}
          </span>
          <span className="shrink-0 text-muted-foreground">
            {caddyLineCount} {caddyLineCount === 1 ? "line" : "lines"}
          </span>
        </>
      )}
    </div>
  )
}

/** Deletes the unused rules shown by the filters, after a confirmation. */
function DeleteUnused({ env, rules }: { env: string; rules: Rule[] }) {
  const queryClient = useQueryClient()
  const remove = $api.useMutation(
    "delete",
    "/api/environments/{env}/rules/{id}"
  )
  const [working, setWorking] = useState(false)
  const count = `${rules.length} unused ${rules.length === 1 ? "rule" : "rules"}`

  const deleteAll = async () => {
    setWorking(true)
    let failed = 0
    for (const r of rules) {
      try {
        await remove.mutateAsync({ params: { path: { env, id: r.id } } })
      } catch {
        failed++
      }
    }
    setWorking(false)
    void queryClient.invalidateQueries({
      queryKey: ["get", "/api/environments/{env}/rules"],
    })
    if (failed) {
      toast.error(`Could not delete ${failed} of the ${count}`)
    } else {
      toast.success(`Deleted ${count}`)
    }
  }

  return (
    <AlertDialog>
      <AlertDialogTrigger render={<Button variant="outline" size="sm" />}>
        <TrashIcon />
        Delete unused
      </AlertDialogTrigger>
      <AlertDialogContent>
        <AlertDialogHeader>
          <AlertDialogTitle>Delete {count}?</AlertDialogTitle>
          <AlertDialogDescription>
            Connections they cover wait for approval again.
          </AlertDialogDescription>
        </AlertDialogHeader>
        <AlertDialogFooter>
          <AlertDialogCancel>Cancel</AlertDialogCancel>
          <AlertDialogAction
            variant="destructive"
            disabled={working}
            onClick={() => void deleteAll()}
          >
            {working && <Spinner />}
            Delete
          </AlertDialogAction>
        </AlertDialogFooter>
      </AlertDialogContent>
    </AlertDialog>
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
