import {
  useEffect,
  useRef,
  useState,
  type FormEvent,
  type ReactNode,
} from "react"
import { useQueryClient } from "@tanstack/react-query"
import { Link } from "@tanstack/react-router"
import { toast } from "sonner"
import {
  ArrowLeftIcon,
  BookOpenIcon,
  BrainIcon,
  CaretRightIcon,
  ChatCircleDotsIcon,
  ClockCounterClockwiseIcon,
  ClockIcon,
  CoinsIcon,
  DotsThreeIcon,
  DownloadSimpleIcon,
  FileDashedIcon,
  LightbulbIcon,
  LinkBreakIcon,
  MagnifyingGlassIcon,
  MoonStarsIcon,
  NotebookIcon,
  PencilSimpleIcon,
  PlusIcon,
  ProhibitIcon,
  PushPinIcon,
  ScalesIcon,
  ShareNetworkIcon,
  TrashIcon,
  UploadSimpleIcon,
  WarningIcon,
  XIcon,
  type Icon,
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
} from "@/components/ui/alert-dialog"
import { Button } from "@/components/ui/button"
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog"
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuSeparator,
  DropdownMenuTrigger,
} from "@/components/ui/dropdown-menu"
import { Input } from "@/components/ui/input"
import { Spinner } from "@/components/ui/spinner"
import { Ident } from "@/components/ident"
import { Markdown, WikiLinks } from "@/components/chat/markdown"
import { SectionHeader } from "@/components/section-header"
import {
  FactDialog,
  PageDialog,
  factKinds,
  invalidateMemory,
  type MemoryFact,
  type MemoryHit,
  type MemoryPage,
} from "@/components/memory-dialogs"
import { ConflictSheet, DreamPanel } from "@/components/memory-conflict"
import { MemorySessionsTab, MemoryUsage } from "@/components/memory-sessions"
import { useMemoryFile } from "@/components/memory-file"
import { $api, errorMessage, fetchClient } from "@/lib/api/client"
import type { components } from "@/lib/api/schema.gen"
import { cn, formatAge } from "@/lib/utils"

type Backlink = components["schemas"]["Backlink"]
type LintFinding = components["schemas"]["LintFinding"]
type Suggestion = components["schemas"]["Suggestion"]

/** The scope name of shared memory; a teammate's private memory is scoped "teammate:<id>". */
export const sharedScope = "shared"

const docsURL = "https://lukaskoebe.github.io/sandbox-studio/memory/"

/** How many rows a list shows before "Show all". */
const listLimit = 30

/**
 * What the reader shows, in the URL as ?open=: what needs the user, what changed this week,
 * the sessions, one page, or the facts of a kind that sit on no page.
 */
type View =
  | { kind: "needs" }
  | { kind: "week" }
  | { kind: "sessions" }
  | { kind: "page"; id: string }
  | { kind: "facts"; group: FactGroup }

const factGroups = [
  { key: "preference", title: "Preferences", kinds: ["preference"] },
  { key: "decision", title: "Decisions", kinds: ["decision"] },
  { key: "procedure", title: "Procedures", kinds: ["procedure"] },
  { key: "other", title: "Facts and events", kinds: ["fact", "event"] },
] as const satisfies readonly {
  key: string
  title: string
  kinds: readonly MemoryFact["kind"][]
}[]
type FactGroup = (typeof factGroups)[number]["key"]

const pageFolds: { kind: MemoryPage["kind"]; title: string }[] = [
  { kind: "project", title: "Projects" },
  { kind: "person", title: "People" },
  { kind: "topic", title: "Topics" },
  { kind: "procedure", title: "Procedures" },
  { kind: "teammate-self", title: "About itself" },
]

function parseView(open?: string): View | undefined {
  if (open === "needs" || open === "week" || open === "sessions")
    return { kind: open }
  if (open?.startsWith("page:") && open.length > 5)
    return { kind: "page", id: open.slice(5) }
  const group = factGroups.find((g) => open === `facts:${g.key}`)
  return group ? { kind: "facts", group: group.key } : undefined
}

function viewKey(v: View) {
  switch (v.kind) {
    case "page":
      return `page:${v.id}`
    case "facts":
      return `facts:${v.group}`
    default:
      return v.kind
  }
}

const week = 7 * 24 * 60 * 60_000

/** Whether a fact still holds: neither retracted nor replaced. */
function holds(f: MemoryFact) {
  return f.status === "active" || f.status === "disputed"
}

/**
 * One scope's memory as an index and a reader, like the Files tab: on the left what needs the
 * user, this week, the pages folded by kind and the facts that sit on no page; on the right
 * the one picked, a page with its [[links]], its facts, what links to it and its timeline.
 * The filter narrows the index; Enter searches. The open view is `open`, which the host keeps
 * in its URL; on a narrow screen the index comes first and a view opens over it.
 */
export function MemoryPanel({
  env,
  scope,
  open,
  onOpen,
}: {
  env: string
  scope: string
  /** The open view, as the URL holds it. */
  open?: string
  onOpen: (open: string | undefined) => void
}) {
  const [pageDialog, setPageDialog] = useState<{
    page?: MemoryPage
    slug?: string
  } | null>(null)
  const [factDialog, setFactDialog] = useState<{ fact?: MemoryFact } | null>(
    null
  )
  const [confirm, setConfirm] = useState<Confirm | null>(null)
  const [conflict, setConflict] = useState<string>()
  const [draft, setDraft] = useState("")
  const [query, setQuery] = useState("")
  const teammates = $api.useQuery("get", "/api/environments/{env}/teammates", {
    params: { path: { env } },
  })
  const names = new Map(teammates.data?.map((p) => [p.id, p.name]))
  const teammate = scope.startsWith("teammate:")
    ? scope.slice("teammate:".length)
    : undefined
  const view = parseView(open)
  const searching = query !== "" && query === draft.trim()
  const show = (v: View | undefined) => {
    setQuery("")
    onOpen(v && viewKey(v))
  }

  const ctx: PanelContext = {
    env,
    scope,
    teammate,
    names,
    view,
    open: (id) => show({ kind: "page", id }),
    show,
    back: () => {
      if (searching) setQuery("")
      else onOpen(undefined)
    },
    newPage: (slug) => setPageDialog({ slug }),
    editPage: (page) => setPageDialog({ page }),
    editFact: (fact) => setFactDialog({ fact }),
    newFact: () => setFactDialog({}),
    confirm: setConfirm,
    resolve: setConflict,
  }
  // Something is open: on a narrow screen the reader takes the place of the index.
  const reading = searching || view !== undefined

  return (
    <div className="@container min-w-0 space-y-4">
      <PanelHeader ctx={ctx} />
      <div className="@2xl:grid @2xl:grid-cols-[15rem_minmax(0,1fr)] @2xl:items-start @2xl:gap-6">
        <Index
          ctx={ctx}
          draft={draft}
          setDraft={setDraft}
          onSearch={() => setQuery(draft.trim())}
          onClear={() => {
            setDraft("")
            setQuery("")
          }}
          className={cn(reading && "hidden @2xl:grid")}
        />
        <div className={cn("min-w-0", !reading && "hidden @2xl:block")}>
          {searching ? (
            <SearchResults ctx={ctx} query={query} />
          ) : (
            <Reader ctx={ctx} />
          )}
        </div>
      </div>
      <PageDialog
        env={env}
        scope={scope}
        page={pageDialog?.page}
        slug={pageDialog?.slug}
        open={pageDialog !== null}
        onOpenChange={(on) => {
          if (!on) setPageDialog(null)
        }}
        onSaved={(page) => show({ kind: "page", id: page.id })}
      />
      <FactDialog
        env={env}
        scope={scope}
        fact={factDialog?.fact}
        open={factDialog !== null}
        onOpenChange={(on) => {
          if (!on) setFactDialog(null)
        }}
      />
      <ConfirmDialog confirm={confirm} onClose={() => setConfirm(null)} />
      <ConflictSheet
        env={env}
        conflictId={conflict}
        onOpenChange={(o) => {
          if (!o) setConflict(undefined)
        }}
      />
    </div>
  )
}

type Confirm = {
  title: string
  description: string
  run: () => void
}

type PanelContext = {
  env: string
  scope: string
  teammate?: string
  names: Map<string, string>
  /** The open view; none shows the first that has something in it. */
  view?: View
  open: (pageId: string) => void
  show: (view: View | undefined) => void
  /** Back to the index: closes the search, or the open view. */
  back: () => void
  newPage: (slug?: string) => void
  editPage: (page: MemoryPage) => void
  newFact: () => void
  editFact: (fact: MemoryFact) => void
  confirm: (c: Confirm) => void
  resolve: (conflictId: string) => void
}

/** What waits for the user: open conflicts and suggestions. */
function useNeedsYou(env: string, scope: string) {
  const conflicts = $api.useQuery(
    "get",
    "/api/environments/{env}/memory/conflicts",
    { params: { path: { env }, query: { scope } } }
  )
  const suggestions = $api.useQuery(
    "get",
    "/api/environments/{env}/memory/suggestions",
    { params: { path: { env }, query: { scope } } }
  )
  return {
    conflicts: (conflicts.data ?? []).filter((c) => c.status === "open"),
    suggestions: suggestions.data ?? [],
  }
}

/** The scope's pages and facts, and which facts sit on no page. */
function useScopeMemory(env: string, scope: string) {
  const pages = $api.useQuery("get", "/api/environments/{env}/memory/pages", {
    params: { path: { env }, query: { scope } },
  })
  const facts = $api.useQuery("get", "/api/environments/{env}/memory/facts", {
    params: { path: { env }, query: { scope } },
  })
  const allPages = pages.data ?? []
  const slugs = new Map(allPages.map((p) => [p.slug, p.id]))
  const allFacts = facts.data ?? []
  const loose = allFacts.filter((f) => {
    const slug = factPageSlug(f)
    return !slug || !slugs.has(slug)
  })
  return {
    pending: pages.isPending || facts.isPending,
    pages: allPages,
    facts: allFacts,
    loose,
    slugs,
  }
}

// --- the index ---------------------------------------------------------------------------

function Index({
  ctx,
  draft,
  setDraft,
  onSearch,
  onClear,
  className,
}: {
  ctx: PanelContext
  draft: string
  setDraft: (s: string) => void
  onSearch: () => void
  onClear: () => void
  className?: string
}) {
  const { env, scope, teammate, view } = ctx
  const search = useRef<HTMLInputElement>(null)
  const [folds, setFolds] = useState<Record<string, boolean>>({})
  const mem = useScopeMemory(env, scope)
  const needs = useNeedsYou(env, scope)
  const sessions = $api.useQuery(
    "get",
    "/api/environments/{env}/memory/sessions",
    { params: { path: { env }, query: { teammate } } }
  )

  // "/" jumps to the filter, as in the rest of Studio's lists.
  useEffect(() => {
    const onKey = (e: KeyboardEvent) => {
      const t = e.target as HTMLElement | null
      if (
        e.key !== "/" ||
        e.metaKey ||
        e.ctrlKey ||
        e.altKey ||
        t?.closest("input, textarea, select, [contenteditable=true]")
      )
        return
      e.preventDefault()
      search.current?.focus()
    }
    document.addEventListener("keydown", onKey)
    return () => document.removeEventListener("keydown", onKey)
  }, [])

  const filter = draft.trim().toLowerCase()
  const pages = mem.pages.filter(
    (p) =>
      !filter ||
      p.title.toLowerCase().includes(filter) ||
      p.slug.includes(filter) ||
      p.compiled.toLowerCase().includes(filter)
  )
  const loose = mem.loose.filter(
    (f) => holds(f) && (!filter || f.text.toLowerCase().includes(filter))
  )
  const since = Date.now() - week
  const thisWeek = mem.facts.filter(
    (f) => holds(f) && new Date(f.createdAt).getTime() >= since
  ).length
  const waiting = needs.conflicts.length + needs.suggestions.length
  const openPage =
    view?.kind === "page" ? mem.pages.find((p) => p.id === view.id) : undefined
  // Few pages show unfolded; the kind of the open page always does.
  const unfolded = (kind: string) =>
    filter !== "" ||
    (folds[kind] ?? (mem.pages.length <= 12 || openPage?.kind === kind))

  return (
    <nav
      aria-label="Memory index"
      className={cn(
        "grid min-w-0 gap-3 @2xl:sticky @2xl:top-0 @2xl:max-h-[calc(100dvh-10rem)] @2xl:overflow-y-auto",
        className
      )}
    >
      <form
        className="relative"
        onSubmit={(e) => {
          e.preventDefault()
          if (draft.trim()) onSearch()
        }}
      >
        <MagnifyingGlassIcon className="pointer-events-none absolute top-1/2 left-2.5 size-3.5 -translate-y-1/2 text-muted-foreground" />
        <Input
          ref={search}
          className="pr-8 pl-8"
          placeholder="Filter, Enter searches"
          title={
            teammate
              ? "Filter the index, or press Enter to search this memory and shared memory"
              : "Filter the index, or press Enter to search shared memory"
          }
          aria-label="Search memory"
          maxLength={500}
          value={draft}
          onChange={(e) => setDraft(e.target.value)}
          onKeyDown={(e) => {
            if (e.key === "Escape" && draft) {
              e.stopPropagation()
              onClear()
            }
          }}
        />
        {draft ? (
          <button
            type="button"
            className="absolute top-1/2 right-2 -translate-y-1/2 text-muted-foreground hover:text-foreground"
            aria-label="Clear the search"
            onClick={onClear}
          >
            <XIcon className="size-3.5" />
          </button>
        ) : (
          <kbd className="pointer-events-none absolute top-1/2 right-2 -translate-y-1/2 font-mono text-meta text-muted-foreground">
            /
          </kbd>
        )}
      </form>

      {!filter && (
        <IndexList>
          <IndexRow
            icon={WarningIcon}
            label="Needs you"
            count={waiting}
            warn={waiting > 0}
            active={view?.kind === "needs"}
            onClick={() => ctx.show({ kind: "needs" })}
          />
          <IndexRow
            icon={ClockIcon}
            label="This week"
            count={thisWeek}
            active={view?.kind === "week"}
            onClick={() => ctx.show({ kind: "week" })}
          />
          <IndexRow
            icon={ChatCircleDotsIcon}
            label="Sessions"
            count={sessions.data?.length}
            active={view?.kind === "sessions"}
            onClick={() => ctx.show({ kind: "sessions" })}
          />
        </IndexList>
      )}

      <section aria-label="Pages" className="grid gap-0.5">
        <IndexLabel
          title="Pages"
          count={mem.pending ? undefined : pages.length}
          action={
            <Button
              variant="ghost"
              size="icon-xs"
              aria-label="New page"
              title="New page"
              onClick={() => ctx.newPage()}
            >
              <PlusIcon />
            </Button>
          }
        />
        {mem.pending ? (
          <Spinner className="mx-auto my-2 block" />
        ) : pages.length === 0 ? (
          <p className="px-2 text-caption text-muted-foreground">
            {filter
              ? "No page matches. Enter searches."
              : "No pages yet. A page holds what's known about a project, a person or a topic."}
          </p>
        ) : (
          pageFolds.map(({ kind, title }) => {
            const list = pages.filter((p) => p.kind === kind)
            if (list.length === 0) return null
            const on = unfolded(kind)
            return (
              <div key={kind} className="grid gap-0.5">
                <IndexRow
                  fold={on}
                  label={title}
                  count={list.length}
                  onClick={() => setFolds({ ...folds, [kind]: !on })}
                />
                {on &&
                  list.map((p) => (
                    <IndexRow
                      key={p.id}
                      indent
                      icon={p.alwaysLoad ? PushPinIcon : undefined}
                      label={p.title}
                      title={p.slug}
                      count={p.counts?.facts}
                      active={view?.kind === "page" && view.id === p.id}
                      onClick={() => ctx.open(p.id)}
                    />
                  ))}
              </div>
            )
          })
        )}
      </section>

      <section aria-label="Facts on no page" className="grid gap-0.5">
        <IndexLabel
          title="Facts on no page"
          count={mem.pending ? undefined : loose.length}
          action={
            <Button
              variant="ghost"
              size="icon-xs"
              aria-label="Add fact"
              title="Add fact"
              onClick={ctx.newFact}
            >
              <PlusIcon />
            </Button>
          }
        />
        {factGroups.map((g) => {
          const n = loose.filter((f) =>
            (g.kinds as readonly string[]).includes(f.kind)
          ).length
          // Procedures mostly have pages of their own; the group shows when some don't.
          if (g.key === "procedure" && n === 0) return null
          return (
            <IndexRow
              key={g.key}
              label={g.title}
              count={n}
              active={view?.kind === "facts" && view.group === g.key}
              onClick={() => ctx.show({ kind: "facts", group: g.key })}
            />
          )
        })}
      </section>
    </nav>
  )
}

function IndexList({ children }: { children: ReactNode }) {
  return <div className="grid gap-0.5">{children}</div>
}

function IndexLabel({
  title,
  count,
  action,
}: {
  title: string
  count?: number
  action?: ReactNode
}) {
  return (
    <h3 className="flex h-6 items-center gap-1.5 px-2 section-label text-muted-foreground">
      {title}{" "}
      {count !== undefined && (
        <span className="font-mono text-meta font-normal tracking-normal">
          {count}
        </span>
      )}
      {action && <span className="ml-auto">{action}</span>}
    </h3>
  )
}

/** One row of the index: a view, a page, or a fold of pages of one kind. */
function IndexRow({
  icon: IconComp,
  label,
  title,
  count,
  warn,
  active,
  indent,
  fold,
  onClick,
}: {
  icon?: Icon
  label: string
  title?: string
  count?: number
  warn?: boolean
  active?: boolean
  indent?: boolean
  /** A fold: whether it is open. */
  fold?: boolean
  onClick: () => void
}) {
  return (
    <button
      type="button"
      title={title}
      aria-current={active ? "true" : undefined}
      aria-expanded={fold}
      onClick={onClick}
      className={cn(
        "flex min-h-7 min-w-0 items-center gap-2 rounded-md px-2 text-left text-xs hover:bg-muted",
        indent && "ml-4",
        active && "bg-muted font-medium"
      )}
    >
      {fold !== undefined ? (
        <CaretRightIcon
          aria-hidden
          className={cn(
            "size-3 shrink-0 text-muted-foreground transition-transform motion-reduce:transition-none",
            fold && "rotate-90"
          )}
        />
      ) : (
        IconComp && (
          <IconComp
            aria-hidden
            className={cn(
              "size-3.5 shrink-0",
              warn
                ? "text-amber-600 dark:text-amber-400"
                : "text-muted-foreground"
            )}
          />
        )
      )}
      <span className="min-w-0 flex-1 truncate">{label}</span>{" "}
      {count !== undefined && (
        <span
          className={cn(
            "shrink-0 font-mono text-meta",
            warn
              ? "bg-amber-500/15 px-1 text-amber-700 dark:text-amber-400"
              : "text-muted-foreground"
          )}
        >
          {count}
        </span>
      )}
    </button>
  )
}

// --- the reader --------------------------------------------------------------------------

/** The open view; with none, what needs the user, else this week. */
function Reader({ ctx }: { ctx: PanelContext }) {
  const needs = useNeedsYou(ctx.env, ctx.scope)
  const view =
    ctx.view ??
    ({
      kind:
        needs.conflicts.length + needs.suggestions.length > 0
          ? "needs"
          : "week",
    } as const)
  switch (view.kind) {
    case "page":
      return <PageView key={view.id} ctx={ctx} id={view.id} />
    case "facts":
      return <LooseFacts key={view.group} ctx={ctx} group={view.group} />
    case "sessions":
      return (
        <div className="space-y-4">
          <ReaderHeader ctx={ctx} title="Sessions" />
          <MemorySessionsTab
            env={ctx.env}
            teammate={ctx.teammate}
            names={ctx.names}
          />
        </div>
      )
    case "week":
      return <ThisWeek ctx={ctx} />
    default:
      return (
        <div className="space-y-5">
          <ReaderHeader ctx={ctx} title="Needs you" />
          <NeedsYou ctx={ctx} />
          <LooseEnds ctx={ctx} />
        </div>
      )
  }
}

/** The reader's title, with the way back to the index on a narrow screen. */
function ReaderHeader({
  ctx,
  title,
  icon: IconComp,
  chips,
  actions,
}: {
  ctx: PanelContext
  title: ReactNode
  icon?: Icon
  chips?: ReactNode
  actions?: ReactNode
}) {
  return (
    <div className="flex min-h-7 min-w-0 flex-wrap items-center gap-2">
      <Button
        variant="ghost"
        size="icon-sm"
        className="-ml-1.5 @2xl:hidden"
        aria-label="Back to the memory index"
        onClick={ctx.back}
      >
        <ArrowLeftIcon />
      </Button>
      {IconComp && (
        <IconComp
          aria-hidden
          className="size-4 shrink-0 text-muted-foreground"
        />
      )}
      <h2 className="min-w-0 font-heading text-base font-semibold text-balance">
        {title}
      </h2>
      {chips}
      {actions && (
        <div className="ml-auto flex items-center gap-1">{actions}</div>
      )}
    </div>
  )
}

/** What changed in the last 7 days: pages updated and facts learned, newest first. */
function ThisWeek({ ctx }: { ctx: PanelContext }) {
  const mem = useScopeMemory(ctx.env, ctx.scope)
  const [all, setAll] = useState(false)
  const since = Date.now() - week
  const pages = mem.pages
    .filter((p) => new Date(p.updatedAt).getTime() >= since)
    .sort((a, b) => b.updatedAt.localeCompare(a.updatedAt))
  const facts = mem.facts
    .filter((f) => holds(f) && new Date(f.createdAt).getTime() >= since)
    .sort((a, b) => b.createdAt.localeCompare(a.createdAt))
  const shown = all ? facts : facts.slice(0, listLimit)
  return (
    <div className="space-y-5">
      <ReaderHeader ctx={ctx} title="This week" />
      {mem.pending ? (
        <Spinner className="mx-auto block" />
      ) : (
        <>
          {pages.length > 0 && (
            <section className="space-y-2">
              <SectionHeader
                title="Pages updated"
                icon={NotebookIcon}
                area="memory"
                count={pages.length}
              />
              <Rows>
                {pages.map((p) => (
                  <Row
                    key={p.id}
                    icon={p.alwaysLoad ? PushPinIcon : NotebookIcon}
                    onOpen={() => ctx.open(p.id)}
                    text={<span className="font-medium">{p.title}</span>}
                    meta={
                      <>
                        <Ident>{p.slug}</Ident> · {p.kind} · updated{" "}
                        {formatAge(p.updatedAt)}
                      </>
                    }
                  />
                ))}
              </Rows>
            </section>
          )}
          <section className="space-y-2">
            <SectionHeader
              title="New facts"
              icon={BrainIcon}
              area="memory"
              count={facts.length}
            />
            <Rows
              more={facts.length - shown.length}
              onMore={() => setAll(true)}
            >
              {facts.length === 0 && (
                <EmptyRow>
                  Nothing new this week. Facts are short statements:
                  preferences, decisions, procedures and events.
                </EmptyRow>
              )}
              {shown.map((f) => (
                <FactRow key={f.id} ctx={ctx} fact={f} slugs={mem.slugs} />
              ))}
            </Rows>
          </section>
        </>
      )}
    </div>
  )
}

/** The facts of one kind that sit on no page. */
function LooseFacts({ ctx, group }: { ctx: PanelContext; group: FactGroup }) {
  const mem = useScopeMemory(ctx.env, ctx.scope)
  const [all, setAll] = useState(false)
  const [old, setOld] = useState(false)
  const g = factGroups.find((x) => x.key === group)!
  const kinds: readonly string[] = g.kinds
  const facts = mem.loose
    .filter((f) => kinds.includes(f.kind))
    .sort((a, b) => b.observedAt.localeCompare(a.observedAt))
  const live = facts.filter(holds)
  const past = facts.length - live.length
  const list = old ? facts : live
  const shown = all ? list : list.slice(0, listLimit)
  return (
    <div className="space-y-4">
      <ReaderHeader
        ctx={ctx}
        title={g.title}
        icon={BrainIcon}
        actions={
          <>
            {past > 0 && (
              <Button size="sm" variant="ghost" onClick={() => setOld(!old)}>
                {old ? "Hide" : "Show"} {past} retracted or replaced
              </Button>
            )}
            <Button size="sm" variant="outline" onClick={ctx.newFact}>
              <PlusIcon />
              Add fact
            </Button>
          </>
        }
      />
      <p className="text-caption text-muted-foreground">
        Facts about no project, person or topic with a page. One whose subject
        gets a page later moves there.
      </p>
      {mem.pending ? (
        <Spinner className="mx-auto block" />
      ) : (
        <Rows more={list.length - shown.length} onMore={() => setAll(true)}>
          {shown.length === 0 && <EmptyRow>None.</EmptyRow>}
          {shown.map((f) => (
            <FactRow key={f.id} ctx={ctx} fact={f} slugs={mem.slugs} />
          ))}
        </Rows>
      )}
    </div>
  )
}

/** The scope's size, Dream now, and the rest in one menu. */
function PanelHeader({ ctx }: { ctx: PanelContext }) {
  const { env, scope } = ctx
  const queryClient = useQueryClient()
  const [dialog, setDialog] = useState<"dreams" | "usage" | null>(null)
  const file = useMemoryFile(env, scope)
  const scopes = $api.useQuery("get", "/api/environments/{env}/memory/scopes", {
    params: { path: { env } },
  })
  const current = scopes.data?.find((s) => s.scope === scope)
  const runs = $api.useQuery(
    "get",
    "/api/environments/{env}/memory/dreams",
    { params: { path: { env }, query: { scope, limit: 1 } } },
    {
      refetchInterval: (q) =>
        q.state.data?.some((r) => r.status === "running") ? 2000 : false,
    }
  )
  const dream = $api.useMutation(
    "post",
    "/api/environments/{env}/memory/dream",
    {
      onSettled: () => invalidateMemory(queryClient),
      onError: (err) =>
        toast.error("Could not start consolidation", {
          description: errorMessage(err),
        }),
    }
  )
  const dreaming =
    dream.isPending || (runs.data ?? []).some((r) => r.status === "running")

  return (
    <div className="flex flex-wrap items-center gap-2">
      <p className="font-mono text-caption text-muted-foreground">
        {current ? (
          <>
            {current.facts} facts · {current.pages} pages · core{" "}
            {current.coreChars.toLocaleString()} /{" "}
            {current.coreBudget.toLocaleString()}
          </>
        ) : (
          " "
        )}
      </p>
      <div className="ml-auto flex items-center gap-1">
        <Button
          size="sm"
          variant="outline"
          disabled={dreaming}
          title="Merge duplicates, retire outdated facts, find contradictions and compile pages. Runs nightly and after 25 new facts too."
          onClick={() =>
            dream.mutate({ params: { path: { env } }, body: { scope } })
          }
        >
          {dreaming ? <Spinner /> : <MoonStarsIcon />}
          {dreaming ? "Dreaming…" : "Dream now"}
        </Button>
        <DropdownMenu>
          <DropdownMenuTrigger
            render={
              <Button
                variant="ghost"
                size="icon-sm"
                aria-label="More memory actions"
                title="More"
              />
            }
          >
            <DotsThreeIcon weight="bold" />
          </DropdownMenuTrigger>
          <DropdownMenuContent align="end" className="w-52">
            <DropdownMenuItem onClick={file.exportMemory}>
              <DownloadSimpleIcon />
              Export
            </DropdownMenuItem>
            <DropdownMenuItem
              disabled={file.importing}
              onClick={file.pickImport}
            >
              {file.importing ? <Spinner /> : <UploadSimpleIcon />}
              Import
            </DropdownMenuItem>
            <DropdownMenuSeparator />
            <DropdownMenuItem onClick={() => setDialog("dreams")}>
              <ClockCounterClockwiseIcon />
              Dream history
            </DropdownMenuItem>
            <DropdownMenuItem onClick={() => setDialog("usage")}>
              <CoinsIcon />
              Extraction cost
            </DropdownMenuItem>
            <DropdownMenuItem
              render={<a href={docsURL} target="_blank" rel="noreferrer" />}
            >
              <BookOpenIcon />
              How memory works
            </DropdownMenuItem>
          </DropdownMenuContent>
        </DropdownMenu>
        {file.input}
      </div>
      <Dialog
        open={dialog !== null}
        onOpenChange={(o) => {
          if (!o) setDialog(null)
        }}
      >
        <DialogContent className="sm:max-w-2xl">
          <DialogHeader>
            <DialogTitle>
              {dialog === "usage" ? "Extraction cost" : "Dream history"}
            </DialogTitle>
            <DialogDescription>
              {dialog === "usage"
                ? "What learning from sessions cost today, and which models do it."
                : "Dreams merge duplicates, retire outdated facts, find contradictions and compile pages, nightly and after 25 new facts."}
            </DialogDescription>
          </DialogHeader>
          {dialog === "usage" ? (
            <div className="grid gap-1">
              <MemoryUsage env={env} shared={scope === sharedScope} />
            </div>
          ) : (
            <DreamPanel env={env} scope={scope} />
          )}
        </DialogContent>
      </Dialog>
    </div>
  )
}

/** Open conflicts and suggestions; hidden when there are none. */
function NeedsYou({ ctx }: { ctx: PanelContext }) {
  const { env, scope } = ctx
  const queryClient = useQueryClient()
  const conflicts = $api.useQuery(
    "get",
    "/api/environments/{env}/memory/conflicts",
    { params: { path: { env }, query: { scope } } }
  )
  const suggestions = $api.useQuery(
    "get",
    "/api/environments/{env}/memory/suggestions",
    { params: { path: { env }, query: { scope } } }
  )
  const accept = $api.useMutation(
    "post",
    "/api/environments/{env}/memory/suggestions/{id}/accept",
    {
      onSuccess: (sg) =>
        sg.kind === "promote"
          ? toast.success("Proposed for shared memory", {
              description:
                "Approve it in the inbox to share it with every teammate.",
            })
          : toast.success("Core page set", {
              description: "Its page loads into every session of this scope.",
            }),
      onSettled: () => {
        invalidateMemory(queryClient)
        queryClient.invalidateQueries({ queryKey: ["get", "/api/approvals"] })
      },
      onError: (err) =>
        toast.error("Could not accept the suggestion", {
          description: errorMessage(err),
        }),
    }
  )
  const dismiss = $api.useMutation(
    "post",
    "/api/environments/{env}/memory/suggestions/{id}/dismiss",
    {
      onSettled: () => invalidateMemory(queryClient),
      onError: (err) =>
        toast.error("Could not dismiss the suggestion", {
          description: errorMessage(err),
        }),
    }
  )
  const open = (conflicts.data ?? []).filter((c) => c.status === "open")
  const list: Suggestion[] = suggestions.data ?? []
  if (open.length + list.length === 0) return null
  const busy = accept.isPending || dismiss.isPending

  return (
    <section className="space-y-2">
      <SectionHeader
        title="Needs you"
        icon={WarningIcon}
        area="memory"
        count={open.length + list.length}
      />
      <Rows>
        {open.map((c) => (
          <Row
            key={c.id}
            icon={ScalesIcon}
            text={
              <>
                “{c.factA.text}” {verdictWord(c.verdict)} “{c.factB.text}”
              </>
            }
            meta={
              <>
                Conflict · {formatAge(c.createdAt)} ·{" "}
                <Link
                  to="/e/$env/memory/conflicts/$id"
                  params={{ env, id: c.id }}
                  className="underline-offset-3 hover:text-foreground hover:underline"
                >
                  open as page
                </Link>
              </>
            }
            actions={
              <Button size="sm" onClick={() => ctx.resolve(c.id)}>
                Resolve…
              </Button>
            }
          />
        ))}
        {list.map((sg) => (
          <Row
            key={sg.id}
            icon={LightbulbIcon}
            text={sg.fact.text}
            meta={<>Suggestion · {sg.reason}</>}
            actions={
              <>
                <Button
                  size="sm"
                  variant="outline"
                  disabled={busy}
                  onClick={() =>
                    accept.mutate({ params: { path: { env, id: sg.id } } })
                  }
                >
                  {sg.kind === "promote" ? "Promote" : "Make core"}
                </Button>
                <Button
                  size="sm"
                  variant="ghost"
                  disabled={busy}
                  onClick={() =>
                    dismiss.mutate({ params: { path: { env, id: sg.id } } })
                  }
                >
                  Dismiss
                </Button>
              </>
            }
          />
        ))}
      </Rows>
    </section>
  )
}

function verdictWord(v: components["schemas"]["Conflict"]["verdict"]) {
  switch (v) {
    case "duplicate":
      return "repeats"
    case "temporal_supersession":
      return "may replace"
    case "context_dependent":
      return "depends on context with"
    default:
      return "contradicts"
  }
}

/** Wanted pages, orphans and entities without a page: what keeps the wiki connected. */
function LooseEnds({ ctx }: { ctx: PanelContext }) {
  const { env, scope } = ctx
  const [all, setAll] = useState(false)
  const lint = $api.useQuery("get", "/api/environments/{env}/memory/lint", {
    params: { path: { env }, query: { scope } },
  })
  const list = lint.data ?? []
  if (list.length === 0) return null
  const shown = all ? list : list.slice(0, 3)
  return (
    <section className="space-y-2">
      <SectionHeader
        title="Loose ends"
        icon={LinkBreakIcon}
        area="memory"
        count={list.length}
      />
      <Rows more={list.length - shown.length} onMore={() => setAll(true)}>
        {shown.map((l) => (
          <LintRow key={`${l.kind} ${l.slug}`} ctx={ctx} finding={l} />
        ))}
      </Rows>
    </section>
  )
}

function LintRow({
  ctx,
  finding: l,
}: {
  ctx: PanelContext
  finding: LintFinding
}) {
  if (l.kind === "orphan")
    return (
      <Row
        icon={FileDashedIcon}
        text={
          <>
            Nothing links to <span className="font-medium">{l.title}</span>, and
            it hasn't changed in 30 days.
          </>
        }
        meta={
          <>
            Orphan page · <Ident>{l.slug}</Ident>
          </>
        }
        onOpen={l.pageId ? () => ctx.open(l.pageId!) : undefined}
        actions={
          l.pageId && (
            <Button
              size="sm"
              variant="outline"
              onClick={() => ctx.open(l.pageId!)}
            >
              Open
            </Button>
          )
        }
      />
    )
  const from = (l.from ?? []).map((b) => b.title ?? b.slug ?? "a fact")
  return (
    <Row
      icon={LinkBreakIcon}
      text={
        l.kind === "wanted" ? (
          <>
            <WantedSlug slug={l.slug} /> is linked {plural(l.count, "time")} but
            has no page yet.
          </>
        ) : (
          <>
            {plural(l.count, "fact")} are about <WantedSlug slug={l.slug} />,
            which has no page.
          </>
        )
      }
      meta={
        l.kind === "wanted"
          ? `Wanted page${from.length ? ` · from ${from.join(", ")}` : ""}`
          : "Facts without a page"
      }
      actions={
        <Button size="sm" variant="outline" onClick={() => ctx.newPage(l.slug)}>
          Create page
        </Button>
      }
    />
  )
}

function WantedSlug({ slug }: { slug: string }) {
  return (
    <span className="font-mono text-xs text-muted-foreground underline decoration-dashed underline-offset-3">
      {slug}
    </span>
  )
}

function PageMenu({ ctx, page: p }: { ctx: PanelContext; page: MemoryPage }) {
  const { env } = ctx
  const queryClient = useQueryClient()
  const remove = $api.useMutation(
    "delete",
    "/api/environments/{env}/memory/pages/{id}",
    {
      onSuccess: () => ctx.back(),
      onSettled: () => invalidateMemory(queryClient),
      onError: (err) =>
        toast.error("Could not delete the page", {
          description: errorMessage(err),
        }),
    }
  )
  const update = $api.useMutation(
    "put",
    "/api/environments/{env}/memory/pages/{id}",
    {
      onSettled: () => invalidateMemory(queryClient),
      onError: (err) =>
        toast.error("Could not change the page", {
          description: errorMessage(err),
        }),
    }
  )
  return (
    <RowMenu label={`More for ${p.slug}`}>
      <DropdownMenuItem
        onClick={() =>
          update.mutate({
            params: { path: { env, id: p.id } },
            body: {
              title: p.title,
              kind: p.kind,
              compiled: p.compiled,
              alwaysLoad: !p.alwaysLoad,
            },
          })
        }
      >
        <PushPinIcon />
        {p.alwaysLoad ? "Stop loading it everywhere" : "Make it a core page"}
      </DropdownMenuItem>
      <DropdownMenuItem
        variant="destructive"
        onClick={() =>
          ctx.confirm({
            title: `Delete ${p.slug}?`,
            description:
              "The page and its timeline are removed for good. Its facts stay.",
            run: () => remove.mutate({ params: { path: { env, id: p.id } } }),
          })
        }
      >
        <TrashIcon />
        Delete page
      </DropdownMenuItem>
    </RowMenu>
  )
}

function FactRow({
  ctx,
  fact: f,
  slugs,
  hidePage,
}: {
  ctx: PanelContext
  fact: MemoryFact
  slugs?: Map<string, string>
  hidePage?: boolean
}) {
  const slug = factPageSlug(f)
  const pageId = slug ? slugs?.get(slug) : undefined
  return (
    <Row
      icon={BrainIcon}
      dim={f.status === "retracted" || f.status === "superseded"}
      text={f.text}
      meta={
        <>
          {[f.kind, factSource(f, ctx.names), formatAge(f.observedAt)].join(
            " · "
          )}
          {f.context && ` · only in ${f.context}`}
          {slug && !hidePage && (
            <>
              {" · in "}
              {pageId ? (
                <button
                  type="button"
                  className="font-mono text-primary hover:underline"
                  onClick={() => ctx.open(pageId)}
                >
                  {slug}
                </button>
              ) : (
                <Ident>{slug}</Ident>
              )}
            </>
          )}
          {f.status !== "active" && <Tag>{f.status}</Tag>}
        </>
      }
      actions={<FactMenu ctx={ctx} fact={f} />}
    />
  )
}

function FactMenu({ ctx, fact: f }: { ctx: PanelContext; fact: MemoryFact }) {
  const { env, scope } = ctx
  const queryClient = useQueryClient()
  const onSettled = () => invalidateMemory(queryClient)
  const retract = $api.useMutation(
    "post",
    "/api/environments/{env}/memory/facts/{id}/retract",
    {
      onSettled,
      onError: (err) =>
        toast.error("Could not retract the fact", {
          description: errorMessage(err),
        }),
    }
  )
  const promote = $api.useMutation(
    "post",
    "/api/environments/{env}/memory/facts/{id}/promote",
    {
      onSuccess: () =>
        toast.success("Proposed for shared memory", {
          description:
            "Approve it in the inbox to share it with every teammate.",
        }),
      onSettled: () =>
        queryClient.invalidateQueries({ queryKey: ["get", "/api/approvals"] }),
      onError: (err) =>
        toast.error("Could not promote the fact", {
          description: errorMessage(err),
        }),
    }
  )
  const remove = $api.useMutation(
    "delete",
    "/api/environments/{env}/memory/facts/{id}",
    {
      onSettled,
      onError: (err) =>
        toast.error("Could not delete the fact", {
          description: errorMessage(err),
        }),
    }
  )
  const params = { params: { path: { env, id: f.id } } }
  return (
    <RowMenu label="More for this fact">
      <DropdownMenuItem onClick={() => ctx.editFact(f)}>
        <PencilSimpleIcon />
        Edit
      </DropdownMenuItem>
      {scope !== sharedScope && f.scope === scope && f.status === "active" && (
        <DropdownMenuItem onClick={() => promote.mutate(params)}>
          <ShareNetworkIcon />
          Promote to shared
        </DropdownMenuItem>
      )}
      {f.status !== "retracted" && (
        <DropdownMenuItem onClick={() => retract.mutate(params)}>
          <ProhibitIcon />
          Retract: keep it on record, never recall it
        </DropdownMenuItem>
      )}
      <DropdownMenuItem
        variant="destructive"
        onClick={() =>
          ctx.confirm({
            title: "Delete this fact?",
            description:
              "It is removed for good. Retract it instead to keep it on record.",
            run: () => remove.mutate(params),
          })
        }
      >
        <TrashIcon />
        Delete
      </DropdownMenuItem>
    </RowMenu>
  )
}

function SearchResults({ ctx, query }: { ctx: PanelContext; query: string }) {
  const [includeSuperseded, setIncludeSuperseded] = useState(false)
  const hits = $api.useQuery("get", "/api/environments/{env}/memory/search", {
    params: {
      path: { env: ctx.env },
      query: {
        q: query,
        teammate: ctx.teammate,
        includeSuperseded,
        limit: 20,
      },
    },
  })
  return (
    <section className="space-y-3">
      <ReaderHeader
        ctx={ctx}
        title="Search results"
        icon={MagnifyingGlassIcon}
        chips={
          hits.data && (
            <span className="font-mono text-meta text-muted-foreground">
              {hits.data.length}
            </span>
          )
        }
        actions={
          <Button
            size="sm"
            variant="ghost"
            onClick={() => setIncludeSuperseded(!includeSuperseded)}
          >
            {includeSuperseded ? "Hide" : "Include"} replaced facts
          </Button>
        }
      />
      {hits.isPending ? (
        <Spinner className="mx-auto block" />
      ) : hits.error ? (
        <p className="text-destructive">{errorMessage(hits.error)}</p>
      ) : (
        <Rows>
          {(hits.data ?? []).length === 0 && (
            <EmptyRow>Nothing found.</EmptyRow>
          )}
          {(hits.data ?? []).map((h) => (
            <HitRow key={`${h.type}-${h.id}`} ctx={ctx} hit={h} />
          ))}
        </Rows>
      )}
    </section>
  )
}

function HitRow({ ctx, hit: h }: { ctx: PanelContext; hit: MemoryHit }) {
  const flags = [
    h.disputed && "disputed",
    h.superseded && "replaced",
    h.scope !== ctx.scope && scopeName(h.scope, ctx.names),
  ].filter(Boolean)
  return (
    <Row
      icon={h.type === "page" ? NotebookIcon : BrainIcon}
      onOpen={h.type === "page" ? () => ctx.open(h.id) : undefined}
      text={
        <>
          {h.page && <span className="font-medium">{h.page.title}: </span>}
          <span className="whitespace-pre-wrap">{h.snippet}</span>
        </>
      }
      meta={
        <>
          {h.why.summary}
          {flags.map((f) => (
            <Tag key={String(f)}>{f}</Tag>
          ))}
        </>
      }
    />
  )
}

// --- a page --------------------------------------------------------------------------------

/** How many of a page's facts show before "Show N more". */
const pageFactLimit = 10

function PageView({ ctx, id }: { ctx: PanelContext; id: string }) {
  const { env } = ctx
  const queryClient = useQueryClient()
  const [entry, setEntry] = useState("")
  const [adding, setAdding] = useState(false)
  const [chip, setChip] = useState<MemoryFact["kind"] | "all" | "past">("all")
  const [all, setAll] = useState(false)
  const page = $api.useQuery(
    "get",
    "/api/environments/{env}/memory/pages/{id}",
    {
      params: { path: { env, id } },
    }
  )
  const scope = page.data?.scope ?? ctx.scope
  const facts = $api.useQuery(
    "get",
    "/api/environments/{env}/memory/facts",
    { params: { path: { env }, query: { scope } } },
    { enabled: page.data !== undefined }
  )

  const append = async (e: FormEvent) => {
    e.preventDefault()
    if (!entry.trim() || adding) return
    setAdding(true)
    try {
      const result = await fetchClient.POST(
        "/api/environments/{env}/memory/pages/{id}/timeline",
        { params: { path: { env, id } }, body: { text: entry.trim() } }
      )
      if (result.error) {
        toast.error("Could not add the entry", {
          description: errorMessage(result.error),
        })
        return
      }
      setEntry("")
      await invalidateMemory(queryClient)
    } finally {
      setAdding(false)
    }
  }

  if (page.error)
    return (
      <div className="space-y-4">
        <ReaderHeader ctx={ctx} title="Page" />
        <p className="text-destructive">{errorMessage(page.error)}</p>
      </div>
    )
  if (!page.data) return <Spinner className="mx-auto block" />
  const p = page.data
  const links = new Map((p.links ?? []).map((l) => [l.target, l]))
  const onPage = (facts.data ?? [])
    .filter((f) => factPageSlug(f) === p.slug)
    .sort((a, b) => b.observedAt.localeCompare(a.observedAt))
  const live = onPage.filter(holds)
  const past = onPage.filter((f) => !holds(f))
  const byKind = factKinds
    .map((k) => ({ kind: k, n: live.filter((f) => f.kind === k).length }))
    .filter((k) => k.n > 0)
  const list =
    chip === "all"
      ? live
      : chip === "past"
        ? past
        : live.filter((f) => f.kind === chip)
  const shown = all ? list : list.slice(0, pageFactLimit)
  const linkedFrom = p.linkedFrom ?? []
  const renderLink = (target: string, label: string) => {
    const l = links.get(target)
    if (l?.exists && l.pageId)
      return (
        <button
          type="button"
          className="font-mono text-[0.9em] text-primary underline decoration-dotted underline-offset-3 hover:decoration-solid"
          title={l.summary ? `${l.title}: ${l.summary}` : l.title}
          onClick={() => ctx.open(l.pageId!)}
        >
          {label}
        </button>
      )
    if (l)
      return (
        <>
          <button
            type="button"
            className="font-mono text-[0.9em] text-muted-foreground underline decoration-dashed underline-offset-3 hover:text-foreground"
            title={`${target} has no page yet: create it`}
            onClick={() => ctx.newPage(target)}
          >
            {label}
          </button>
          <span className="text-muted-foreground"> (no page yet)</span>
        </>
      )
    return <span className="font-mono text-[0.9em]">{label}</span>
  }
  const meta = [
    p.scope !== ctx.scope && scopeName(p.scope, ctx.names),
    authorOf(p, ctx.names),
    `updated ${formatAge(p.updatedAt)}`,
    p.alwaysLoad && "loads into every session",
  ].filter(Boolean)
  const owner =
    p.scope === sharedScope
      ? "every teammate's sandboxes"
      : `${ctx.names.get(p.scope.slice("teammate:".length)) ?? "the teammate"}'s sandboxes`
  const dir = p.scope === sharedScope ? "~/memory/shared" : "~/memory"

  return (
    <WikiLinks.Provider value={renderLink}>
      <article className="space-y-5">
        <header className="space-y-1">
          <ReaderHeader
            ctx={ctx}
            title={p.title}
            icon={p.alwaysLoad ? PushPinIcon : NotebookIcon}
            chips={
              <>
                <Tag>{p.kind}</Tag>
                {p.alwaysLoad && <Tag>core</Tag>}
              </>
            }
            actions={
              <>
                <Button
                  size="sm"
                  variant="outline"
                  onClick={() => ctx.editPage(p)}
                >
                  <PencilSimpleIcon />
                  Edit
                </Button>
                <PageMenu ctx={ctx} page={p} />
              </>
            }
          />
          <p className="text-caption text-muted-foreground">
            <Ident>{p.slug}</Ident> · {meta.join(" · ")}
          </p>
        </header>
        {p.compiled.trim() ? (
          <Markdown text={p.compiled} className="text-xs/relaxed" />
        ) : (
          <p className="text-muted-foreground">
            No compiled truth yet. Edit the page to write it, or let the next
            dream compile it from the page's facts.
          </p>
        )}

        {onPage.length > 0 && (
          <section className="space-y-2">
            <SectionHeader
              title="Facts"
              icon={BrainIcon}
              area="memory"
              count={live.length}
            />
            {(byKind.length > 1 || past.length > 0) && (
              <div
                role="group"
                aria-label="Facts by kind"
                className="flex flex-wrap gap-1.5"
              >
                <KindChip
                  on={chip === "all"}
                  label="All"
                  n={live.length}
                  onClick={() => setChip("all")}
                />
                {byKind.map((k) => (
                  <KindChip
                    key={k.kind}
                    on={chip === k.kind}
                    label={factKindLabels[k.kind]}
                    n={k.n}
                    onClick={() => setChip(k.kind)}
                  />
                ))}
                {past.length > 0 && (
                  <KindChip
                    on={chip === "past"}
                    label="Retracted or replaced"
                    n={past.length}
                    onClick={() => setChip("past")}
                  />
                )}
              </div>
            )}
            <Rows more={list.length - shown.length} onMore={() => setAll(true)}>
              {shown.length === 0 && <EmptyRow>None.</EmptyRow>}
              {shown.map((f) => (
                <FactRow key={f.id} ctx={ctx} fact={f} hidePage />
              ))}
            </Rows>
          </section>
        )}

        <section className="space-y-2">
          <SectionHeader
            title="Linked from"
            icon={LinkBreakIcon}
            area="memory"
            count={linkedFrom.length}
          />
          <Rows>
            {linkedFrom.length === 0 && (
              <EmptyRow>
                Nothing links here yet. Pages and facts link a page with [[
                {p.slug}]].
              </EmptyRow>
            )}
            {linkedFrom.map((b) => (
              <BacklinkRow key={`${b.type}-${b.id}`} ctx={ctx} link={b} />
            ))}
          </Rows>
        </section>

        <section className="space-y-2">
          <SectionHeader
            title="Timeline"
            icon={ClockCounterClockwiseIcon}
            area="memory"
            count={p.timeline?.length ?? 0}
          />
          {(p.timeline ?? []).length > 0 && (
            <ol className="space-y-2 border-l pl-3">
              {[...(p.timeline ?? [])].reverse().map((t) => (
                <li key={t.id} className="space-y-0.5">
                  <span className="block text-caption text-muted-foreground">
                    {new Date(t.at).toLocaleString(undefined, {
                      dateStyle: "medium",
                      timeStyle: "short",
                    })}
                    {" · "}
                    {t.authorTeammate
                      ? (ctx.names.get(t.authorTeammate) ?? "a teammate")
                      : t.factId || t.sourceId
                        ? "memory"
                        : "you"}
                  </span>
                  <Markdown text={t.text} className="text-xs/relaxed" />
                </li>
              ))}
            </ol>
          )}
          <form onSubmit={append} className="flex gap-2">
            <Input
              placeholder="Add a timeline entry; [[slug]] links a page"
              maxLength={2000}
              value={entry}
              onChange={(e) => setEntry(e.target.value)}
            />
            <Button
              type="submit"
              size="sm"
              variant="outline"
              disabled={!entry.trim() || adding}
            >
              {adding && <Spinner />}
              Add
            </Button>
          </form>
        </section>

        <p className="border-t pt-3 text-caption text-muted-foreground">
          In {owner}:{" "}
          <span className="font-mono">
            {dir}/pages/{p.slug}.md
          </span>
          , listed in <span className="font-mono">{dir}/index.md</span>
        </p>
      </article>
    </WikiLinks.Provider>
  )
}

const factKindLabels: Record<MemoryFact["kind"], string> = {
  preference: "Preferences",
  decision: "Decisions",
  fact: "Facts",
  procedure: "Procedures",
  event: "Events",
}

function KindChip({
  on,
  label,
  n,
  onClick,
}: {
  on: boolean
  label: string
  n: number
  onClick: () => void
}) {
  return (
    <button
      type="button"
      aria-pressed={on}
      onClick={onClick}
      className={cn(
        "inline-flex h-6 items-center gap-1.5 rounded-full border px-2.5 text-caption text-muted-foreground hover:text-foreground",
        on && "border-foreground/20 bg-muted font-medium text-foreground"
      )}
    >
      {label} <span className="font-mono text-meta">{n}</span>
    </button>
  )
}

function BacklinkRow({ ctx, link: b }: { ctx: PanelContext; link: Backlink }) {
  const where = b.scope !== ctx.scope && scopeName(b.scope, ctx.names)
  if (b.type === "page")
    return (
      <Row
        icon={NotebookIcon}
        onOpen={() => ctx.open(b.id)}
        text={
          <>
            <span className="font-medium">{b.title}</span>{" "}
            <Ident>{b.slug ?? ""}</Ident>
          </>
        }
        meta={
          <>
            <Snippet text={b.text} />
            {where && <Tag>{where}</Tag>}
          </>
        }
      />
    )
  return (
    <Row
      icon={BrainIcon}
      text={<Snippet text={b.text} plain />}
      meta={
        <>
          fact ·{" "}
          {b.via === "entity"
            ? "it is about this page's entity"
            : "it links here"}
          {where && <Tag>{where}</Tag>}
        </>
      }
    />
  )
}

/** A backlink's text with its [[links]] as the page renders them. */
function Snippet({ text, plain }: { text: string; plain?: boolean }) {
  const one = text.replace(/\s+/g, " ").trim()
  return (
    <Markdown
      text={plain ? one : `“${one}”`}
      className={plain ? "text-xs/relaxed" : "inline-grid text-caption"}
    />
  )
}

// --- rows ----------------------------------------------------------------------------------

function Rows({
  children,
  more = 0,
  onMore,
}: {
  children: ReactNode
  more?: number
  onMore?: () => void
}) {
  return (
    <ul className="divide-y overflow-hidden rounded-lg bg-card ring-1 ring-foreground/10">
      {children}
      {more > 0 && (
        <li>
          <button
            type="button"
            className="w-full px-3 py-2 text-left text-caption text-muted-foreground hover:bg-muted/50 hover:text-foreground"
            onClick={onMore}
          >
            Show {more} more
          </button>
        </li>
      )}
    </ul>
  )
}

/** One row of the panel: an icon, the text, a meta line and actions on the right. */
function Row({
  icon: IconComp,
  text,
  meta,
  actions,
  onOpen,
  dim,
}: {
  icon: Icon
  text: ReactNode
  meta?: ReactNode
  actions?: ReactNode
  onOpen?: () => void
  dim?: boolean
}) {
  const body = (
    <>
      <span className="block text-xs/relaxed wrap-anywhere">{text}</span>
      {meta && (
        <span className="mt-0.5 block text-caption text-muted-foreground">
          {meta}
        </span>
      )}
    </>
  )
  return (
    <li
      className={cn(
        "flex items-start gap-3 px-3 py-2.5",
        onOpen && "hover:bg-muted/40",
        dim && "opacity-60"
      )}
    >
      <IconComp className="mt-1 size-3.5 shrink-0 text-muted-foreground" />
      {onOpen ? (
        <button
          type="button"
          className="min-w-0 flex-1 cursor-pointer text-left"
          onClick={onOpen}
        >
          {body}
        </button>
      ) : (
        <div className="min-w-0 flex-1">{body}</div>
      )}
      {actions && (
        <div className="flex shrink-0 items-center gap-1">{actions}</div>
      )}
    </li>
  )
}

function EmptyRow({ children }: { children: ReactNode }) {
  return (
    <li className="px-3 py-2.5 text-xs text-muted-foreground">{children}</li>
  )
}

function Tag({ children }: { children: ReactNode }) {
  return (
    <span className="ml-1.5 border px-1 font-mono text-meta text-muted-foreground">
      {children}
    </span>
  )
}

function RowMenu({ label, children }: { label: string; children: ReactNode }) {
  return (
    <DropdownMenu>
      <DropdownMenuTrigger
        render={
          <Button
            variant="ghost"
            size="icon-sm"
            aria-label={label}
            title="More"
          />
        }
      >
        <DotsThreeIcon weight="bold" />
      </DropdownMenuTrigger>
      <DropdownMenuContent align="end" className="w-64">
        {children}
      </DropdownMenuContent>
    </DropdownMenu>
  )
}

function ConfirmDialog({
  confirm,
  onClose,
}: {
  confirm: Confirm | null
  onClose: () => void
}) {
  return (
    <AlertDialog
      open={confirm !== null}
      onOpenChange={(o) => {
        if (!o) onClose()
      }}
    >
      <AlertDialogContent>
        <AlertDialogHeader>
          <AlertDialogTitle>{confirm?.title}</AlertDialogTitle>
          <AlertDialogDescription>
            {confirm?.description}
          </AlertDialogDescription>
        </AlertDialogHeader>
        <AlertDialogFooter>
          <AlertDialogCancel>Cancel</AlertDialogCancel>
          <AlertDialogAction
            variant="destructive"
            onClick={() => {
              confirm?.run()
              onClose()
            }}
          >
            Delete
          </AlertDialogAction>
        </AlertDialogFooter>
      </AlertDialogContent>
    </AlertDialog>
  )
}

// --- helpers -------------------------------------------------------------------------------

function plural(n: number, word: string) {
  return `${n} ${word}${n === 1 ? "" : "s"}`
}

function scopeName(scope: string, names: Map<string, string>) {
  if (scope === sharedScope) return "shared"
  const id = scope.slice("teammate:".length)
  return `${names.get(id) ?? "a teammate"}'s memory`
}

function authorOf(p: MemoryPage, names: Map<string, string>) {
  if (p.tier === "user") return "written by you"
  if (p.authorTeammate)
    return `by ${names.get(p.authorTeammate) ?? "a teammate"}`
  return "compiled by memory"
}

function factSource(f: MemoryFact, names: Map<string, string>) {
  if (f.tier === "user") return "from you"
  if (f.tier === "document") return "imported"
  if (f.authorTeammate)
    return `learned by ${names.get(f.authorTeammate) ?? "a teammate"}`
  return "learned"
}

const slugPattern = /^[a-z0-9][a-z0-9._-]*(\/[a-z0-9][a-z0-9._-]*)*$/

/** The page slug an entity names, as memory derives it: topic/ when it has no kind. */
function entitySlug(entity: string) {
  let e = entity.trim().toLowerCase()
  if (!e.includes("/")) e = `topic/${e}`
  return slugPattern.test(e) && e.length <= 128 ? e : ""
}

/** The slug of the page that records a fact: its first entity's, else its attribute's. */
function factPageSlug(f: MemoryFact) {
  for (const e of f.entityIds ?? []) {
    const s = entitySlug(e)
    if (s) return s
  }
  if (f.attribute) return entitySlug(f.attribute.split(".")[0])
  return ""
}
