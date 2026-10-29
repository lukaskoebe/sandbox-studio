import { Fragment, useEffect, useState, type ReactNode } from "react"
import { createFileRoute } from "@tanstack/react-router"
import { SettingsPage } from "@/components/settings/page"
import { SettingsLink } from "@/components/settings/link"
import { useQueryClient } from "@tanstack/react-query"
import { toast } from "sonner"
import {
  ArrowsLeftRightIcon,
  CheckIcon,
  CubeIcon,
  FileCodeIcon,
  GlobeHemisphereWestIcon,
  ListChecksIcon,
  PencilSimpleIcon,
  PlusIcon,
  ProhibitIcon,
  TrashIcon,
  WarningIcon,
  type Icon,
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
import { TeammateAvatar } from "@/components/teammate-avatar"
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
  type RuleOrigin,
} from "@/lib/network-rules"
import { cn, formatAge } from "@/lib/utils"

export const Route = createFileRoute("/e/$env/settings/network")({
  component: NetworkSettings,
  validateSearch: (
    s: Record<string, unknown>
  ): RuleFilter & { rule?: string } => ({
    q: typeof s.q === "string" && s.q !== "" ? s.q : undefined,
    scope: ruleScopes.find((v) => v === s.scope),
    action: ruleActions.find((v) => v === s.action),
    origin: ruleOrigins.find((v) => v === s.origin),
    // The URL holds 1; JSON parsing reads it back as a number, so accept the string too.
    unused: s.unused === 1 || s.unused === "1" ? 1 : undefined,
    // A rule to open, as the browser's notice of a page a deny rule blocks links to it.
    rule: typeof s.rule === "string" && s.rule !== "" ? s.rule : undefined,
  }),
})

/** What sandboxes may reach: the environment's security level and its rules. */
function NetworkSettings() {
  return (
    <SettingsPage
      title="Network"
      intro={
        <>
          Sandboxes reach the internet only through Studio, and connections that
          no rule allows wait for your approval. A teammate can tighten the
          level in its own settings.{" "}
          <a
            href="https://lukaskoebe.github.io/sandbox-studio/network/"
            target="_blank"
            rel="noreferrer"
            className="underline underline-offset-4"
          >
            Learn more
          </a>
        </>
      }
    >
      <RulesTab />
    </SettingsPage>
  )
}

/**
 * The rules in one card: the filters on top, then the table, sorted by the domain a host belongs
 * to. A domain with several rules gets a row of its own, with its rules indented under it.
 */
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
  const teammateList = new Map((teammates.data ?? []).map((p) => [p.id, p]))
  // A linked rule opens once the rules load; the URL then drops it, so closing it stays closed.
  useEffect(() => {
    if (!filter.rule || !rules.data) return
    const linked = rules.data.find((r) => r.id === filter.rule)
    if (linked) setDialog({ rule: linked })
    else toast.error("That rule no longer exists")
    void navigate({
      search: (prev) => ({ ...prev, rule: undefined }),
      replace: true,
    })
  }, [filter.rule, rules.data, navigate])
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
    <div className="grid gap-6">
      <LevelSection env={env} />
      <section aria-labelledby="rules-heading" className="grid gap-2">
        <SectionHeader
          title={<span id="rules-heading">Rules</span>}
          icon={ListChecksIcon}
          area="network"
          count={rules.data ? shown.length : undefined}
          actions={
            <Button size="sm" onClick={() => setDialog({})}>
              <PlusIcon />
              Add rule
            </Button>
          }
        />
        <p className="text-xs/relaxed text-muted-foreground">
          Rules come before the level: each allows, proxies or denies a host for
          the environment, a teammate or one computer. Approving a connection
          adds one.
        </p>
        <div className="overflow-hidden rounded-(--radius) border bg-card text-xs">
          {rules.isPending ? (
            <div className="grid h-(--wb-row) place-items-center">
              <Spinner />
            </div>
          ) : !rules.data?.length ? (
            <p className="px-3 py-2.5 text-muted-foreground">
              No rules yet. Approved connections become rules, so the same host
              is not asked about again.
            </p>
          ) : (
            <>
              <div className="flex flex-wrap items-center gap-2 border-b bg-muted/30 px-2 py-1.5">
                <Input
                  type="search"
                  aria-label="Search hosts"
                  placeholder="Search hosts"
                  autoComplete="off"
                  className="h-6 basis-full bg-background sm:w-56 sm:basis-auto"
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
                <label className="flex items-center gap-2 px-1">
                  <Checkbox
                    checked={filter.unused === 1}
                    onCheckedChange={(on) =>
                      setFilter({ unused: on ? 1 : undefined })
                    }
                  />
                  Unused
                </label>
                {filter.unused === 1 && unused.length > 0 && (
                  <DeleteUnused env={env} rules={unused} />
                )}
              </div>
              {!groups.length ? (
                <div className="flex flex-wrap items-center gap-2 px-3 py-2.5 text-muted-foreground">
                  No rules match these filters.
                  <Button variant="link" size="xs" onClick={clearFilters}>
                    Clear filters
                  </Button>
                </div>
              ) : (
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
                    {groups.map((group) => {
                      // One rule needs no heading: its host already names the domain.
                      const nested = group.rules.length > 1
                      return (
                        <Fragment key={group.domain}>
                          {nested && (
                            <TableRow className="hover:bg-transparent">
                              <TableCell colSpan={7} className="pt-2.5 pb-1">
                                <span className="inline-flex items-center gap-2 text-muted-foreground">
                                  <Ident>{group.domain}</Ident>
                                  <span className="font-mono">
                                    {group.rules.length}
                                  </span>
                                </span>
                              </TableCell>
                            </TableRow>
                          )}
                          {group.rules.map((r) => (
                            <TableRow key={r.id} className="group/row">
                              <TableCell className={cn(nested && "pl-6")}>
                                <span className="inline-flex max-w-full items-center gap-1.5">
                                  <Ident>{r.host}</Ident>
                                  {(r.method || r.pathPrefix) && (
                                    <RuleChip>
                                      {`${r.method || "any method"} ${r.pathPrefix || "any path"}`}
                                    </RuleChip>
                                  )}
                                  {isUnused(r, now) && (
                                    <RuleChip>unused</RuleChip>
                                  )}
                                </span>
                                {r.unreachable && (
                                  <Unreachable reason={r.unreachable} />
                                )}
                              </TableCell>
                              <TableCell>
                                {r.ports?.length ? (
                                  <Ident>{r.ports.join(", ")}</Ident>
                                ) : (
                                  <span className="text-muted-foreground">
                                    any
                                  </span>
                                )}
                              </TableCell>
                              <TableCell>
                                <RuleAction rule={r} />
                              </TableCell>
                              <TableCell>
                                <span className="flex items-center gap-1.5">
                                  <RuleScope
                                    rule={r}
                                    sandbox={
                                      r.sandboxId
                                        ? names.get(r.sandboxId)
                                        : undefined
                                    }
                                    teammate={
                                      r.teammateId
                                        ? teammateList.get(r.teammateId)
                                        : undefined
                                    }
                                  />
                                  {r.browserOnly && (
                                    <RuleChip title="Covers what the browsers load, not the sandboxes' own connections">
                                      browser only
                                    </RuleChip>
                                  )}
                                </span>
                              </TableCell>
                              <TableCell className="text-muted-foreground">
                                {r.note ? (
                                  <span
                                    className="block max-w-48 truncate"
                                    title={r.note}
                                  >
                                    {r.note}
                                  </span>
                                ) : (
                                  // Why a rule is there is easy to forget; a note says it
                                  // later. The button shows on the row's hover, or always
                                  // where there is no hover.
                                  <Button
                                    variant="ghost"
                                    size="xs"
                                    className="text-muted-foreground opacity-0 group-hover/row:opacity-100 focus-visible:opacity-100 any-pointer-coarse:opacity-100"
                                    onClick={() => setDialog({ rule: r })}
                                  >
                                    <PencilSimpleIcon />
                                    Add note
                                  </Button>
                                )}
                              </TableCell>
                              <TableCell className="text-muted-foreground">
                                <span title={originTitles[r.origin]}>
                                  {formatAge(r.createdAt)}
                                  {originWords[r.origin] &&
                                    `, ${originWords[r.origin]}`}
                                </span>
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
                      )
                    })}
                  </TableBody>
                </Table>
              )}
            </>
          )}
        </div>
      </section>
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
function Unreachable({ reason }: { reason: string }) {
  return (
    <p className="mt-1 flex max-w-96 gap-1.5 text-caption/relaxed whitespace-normal text-amber-700 dark:text-amber-400">
      <WarningIcon className="mt-0.5 size-3 shrink-0" aria-hidden />
      <span>
        {reason}{" "}
        {reason.includes("Settings → Ports and LAN") && (
          <SettingsLink
            section="ports"
            className="underline underline-offset-2"
          >
            Open Ports and LAN
          </SettingsLink>
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
        className="min-w-28 flex-1 sm:w-36 sm:flex-none"
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

/** A small monospace label on a rule: its method and path, or a flag. */
function RuleChip({
  children,
  title,
}: {
  children: ReactNode
  title?: string
}) {
  return (
    <span
      title={title}
      className="inline-flex h-[1.125rem] shrink-0 items-center rounded-sm bg-muted px-1 font-mono text-caption text-foreground/80 ring-1 ring-border ring-inset"
    >
      {children}
    </span>
  )
}

/** Each action's mark and word: the word carries it, the mark helps a long list scan. */
const actionLabels: Record<
  Rule["action"],
  { icon: Icon; word: string; className?: string }
> = {
  allow: { icon: CheckIcon, word: "Allow" },
  proxy: { icon: ArrowsLeftRightIcon, word: "Proxy" },
  caddy: { icon: FileCodeIcon, word: "Caddy" },
  deny: { icon: ProhibitIcon, word: "Deny", className: "text-destructive" },
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
  const label = actionLabels[rule.action]
  const ActionIcon = label.icon
  return (
    <div className="flex min-w-0 items-center gap-2">
      <span className="inline-flex shrink-0 items-center gap-1">
        <ActionIcon
          weight="bold"
          className={cn("size-3 text-muted-foreground", label.className)}
          aria-hidden
        />
        {label.word}
      </span>
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

/** Where a rule applies: the whole environment, a teammate's computers, or one computer. */
function RuleScope({
  rule,
  sandbox,
  teammate,
}: {
  rule: Rule
  sandbox?: string
  teammate?: { name: string; avatar?: string }
}) {
  if (rule.sandboxId)
    return (
      <span
        className="inline-flex min-w-0 items-center gap-1.5"
        title="One computer"
      >
        <CubeIcon
          className="size-3.5 shrink-0 text-muted-foreground"
          aria-hidden
        />
        <span className="truncate">{sandbox ?? "Other sandbox"}</span>
      </span>
    )
  if (rule.teammateId)
    return (
      <span
        className="inline-flex min-w-0 items-center gap-1.5"
        title="A teammate's computers"
      >
        <TeammateAvatar
          name={teammate?.name ?? "?"}
          avatar={teammate?.avatar}
          className="size-4 text-[0.5rem]"
        />
        <span className="truncate">{teammate?.name ?? "Other teammate"}</span>
      </span>
    )
  return (
    <span className="inline-flex items-center gap-1.5">
      <GlobeHemisphereWestIcon
        className="size-3.5 shrink-0 text-muted-foreground"
        aria-hidden
      />
      Environment
    </span>
  )
}

/** How a rule came to be, after its age; a rule written by hand says nothing more. */
const originWords: Record<RuleOrigin, string | undefined> = {
  manual: undefined,
  approval: "approved",
  curated: "curated",
  reviewer: "by the reviewer",
}

const originTitles: Record<RuleOrigin, string> = {
  manual: "Added by hand",
  approval: "Added by approving a connection",
  curated: "Added from the curated list",
  reviewer: "Added by the AI reviewer",
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
