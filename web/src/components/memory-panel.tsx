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
  BookOpenIcon,
  BrainIcon,
  CaretRightIcon,
  ChatCircleDotsIcon,
  ClockCounterClockwiseIcon,
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

type View =
  { kind: "list" } | { kind: "page"; id: string } | { kind: "sessions" }

/**
 * One scope's memory in one place: what needs the user, the core pages, the pages, the facts
 * and the sessions that used them, every row alike, with one search on top. A page opens in
 * place, with its [[links]], what links to it and its timeline.
 */
export function MemoryPanel({ env, scope }: { env: string; scope: string }) {
  const [view, setView] = useState<View>({ kind: "list" })
  const [pageDialog, setPageDialog] = useState<{
    page?: MemoryPage
    slug?: string
  } | null>(null)
  const [factDialog, setFactDialog] = useState<{ fact?: MemoryFact } | null>(
    null
  )
  const [confirm, setConfirm] = useState<Confirm | null>(null)
  const [conflict, setConflict] = useState<string>()
  const teammates = $api.useQuery("get", "/api/environments/{env}/teammates", {
    params: { path: { env } },
  })
  const names = new Map(teammates.data?.map((p) => [p.id, p.name]))
  const teammate = scope.startsWith("teammate:")
    ? scope.slice("teammate:".length)
    : undefined

  const ctx: PanelContext = {
    env,
    scope,
    teammate,
    names,
    open: (id) => setView({ kind: "page", id }),
    back: () => setView({ kind: "list" }),
    newPage: (slug) => setPageDialog({ slug }),
    editPage: (page) => setPageDialog({ page }),
    editFact: (fact) => setFactDialog({ fact }),
    newFact: () => setFactDialog({}),
    confirm: setConfirm,
    resolve: setConflict,
    sessions: () => setView({ kind: "sessions" }),
  }

  return (
    <div className="min-w-0 space-y-5">
      {view.kind === "page" ? (
        <PageView key={view.id} ctx={ctx} id={view.id} />
      ) : view.kind === "sessions" ? (
        <div className="space-y-4">
          <Crumbs ctx={ctx} here="Sessions" />
          <MemorySessionsTab env={env} teammate={teammate} names={names} />
        </div>
      ) : (
        <Overview ctx={ctx} />
      )}
      <PageDialog
        env={env}
        scope={scope}
        page={pageDialog?.page}
        slug={pageDialog?.slug}
        open={pageDialog !== null}
        onOpenChange={(open) => {
          if (!open) setPageDialog(null)
        }}
        onSaved={(page) => setView({ kind: "page", id: page.id })}
      />
      <FactDialog
        env={env}
        scope={scope}
        fact={factDialog?.fact}
        open={factDialog !== null}
        onOpenChange={(open) => {
          if (!open) setFactDialog(null)
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
  open: (pageId: string) => void
  back: () => void
  newPage: (slug?: string) => void
  editPage: (page: MemoryPage) => void
  newFact: () => void
  editFact: (fact: MemoryFact) => void
  confirm: (c: Confirm) => void
  resolve: (conflictId: string) => void
  sessions: () => void
}

// --- the overview -------------------------------------------------------------------------

function Overview({ ctx }: { ctx: PanelContext }) {
  const { env, scope, teammate } = ctx
  const [draft, setDraft] = useState("")
  const [query, setQuery] = useState("")
  const search = useRef<HTMLInputElement>(null)
  const pages = $api.useQuery("get", "/api/environments/{env}/memory/pages", {
    params: { path: { env }, query: { scope } },
  })
  const facts = $api.useQuery("get", "/api/environments/{env}/memory/facts", {
    params: { path: { env }, query: { scope } },
  })

  // "/" jumps to the search, as in the rest of Studio's lists.
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
  const searching = query !== "" && query === draft.trim()
  const allPages = pages.data ?? []
  const shownPages = allPages.filter(
    (p) =>
      !filter ||
      p.title.toLowerCase().includes(filter) ||
      p.slug.includes(filter) ||
      p.compiled.toLowerCase().includes(filter)
  )
  const shownFacts = (facts.data ?? []).filter(
    (f) => !filter || f.text.toLowerCase().includes(filter)
  )
  const slugs = new Map(allPages.map((p) => [p.slug, p.id]))

  return (
    <>
      <PanelHeader ctx={ctx} />
      <form
        className="relative"
        onSubmit={(e) => {
          e.preventDefault()
          setQuery(draft.trim())
        }}
      >
        <MagnifyingGlassIcon className="pointer-events-none absolute top-1/2 left-2.5 size-3.5 -translate-y-1/2 text-muted-foreground" />
        <Input
          ref={search}
          className="pr-8 pl-8"
          placeholder={
            teammate
              ? "Filter, or press Enter to search this memory and shared memory"
              : "Filter, or press Enter to search shared memory"
          }
          aria-label="Search memory"
          maxLength={500}
          value={draft}
          onChange={(e) => setDraft(e.target.value)}
          onKeyDown={(e) => {
            if (e.key === "Escape" && draft) {
              e.stopPropagation()
              setDraft("")
              setQuery("")
            }
          }}
        />
        {draft ? (
          <button
            type="button"
            className="absolute top-1/2 right-2 -translate-y-1/2 text-muted-foreground hover:text-foreground"
            aria-label="Clear the search"
            onClick={() => {
              setDraft("")
              setQuery("")
            }}
          >
            <XIcon className="size-3.5" />
          </button>
        ) : (
          <kbd className="pointer-events-none absolute top-1/2 right-2 -translate-y-1/2 font-mono text-meta text-muted-foreground">
            /
          </kbd>
        )}
      </form>

      {searching ? (
        <SearchResults ctx={ctx} query={query} />
      ) : (
        <>
          {!filter && <NeedsYou ctx={ctx} />}
          {!filter && <LooseEnds ctx={ctx} />}
          <PageSections
            ctx={ctx}
            pending={pages.isPending}
            pages={shownPages}
            filtered={filter !== ""}
          />
          <FactSection
            ctx={ctx}
            pending={facts.isPending}
            facts={shownFacts}
            slugs={slugs}
            filtered={filter !== ""}
          />
          {!filter && <SessionsRow ctx={ctx} />}
        </>
      )}
    </>
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

/** Core pages, then the other pages. */
function PageSections({
  ctx,
  pending,
  pages,
  filtered,
}: {
  ctx: PanelContext
  pending: boolean
  pages: MemoryPage[]
  filtered: boolean
}) {
  const [all, setAll] = useState(false)
  const core = pages.filter((p) => p.alwaysLoad)
  const rest = pages.filter((p) => !p.alwaysLoad)
  const shown = all || filtered ? rest : rest.slice(0, listLimit)
  return (
    <>
      {core.length > 0 && (
        <section className="space-y-2">
          <SectionHeader
            title="Core"
            icon={PushPinIcon}
            area="memory"
            count={core.length}
          />
          <Rows>
            {core.map((p) => (
              <PageRow key={p.id} ctx={ctx} page={p} />
            ))}
          </Rows>
        </section>
      )}
      <section className="space-y-2">
        <SectionHeader
          title="Pages"
          icon={NotebookIcon}
          area="memory"
          count={pending ? undefined : rest.length}
          actions={
            <Button size="sm" variant="outline" onClick={() => ctx.newPage()}>
              <PlusIcon />
              New page
            </Button>
          }
        />
        {pending ? (
          <Spinner className="mx-auto block" />
        ) : (
          <Rows more={rest.length - shown.length} onMore={() => setAll(true)}>
            {shown.length === 0 && (
              <EmptyRow>
                {filtered
                  ? "No page matches. Press Enter to search."
                  : "No pages yet. A page holds what's known about a project, a person or a topic, links to other pages with [[slug]], and keeps a timeline."}
              </EmptyRow>
            )}
            {shown.map((p) => (
              <PageRow key={p.id} ctx={ctx} page={p} />
            ))}
          </Rows>
        )}
      </section>
    </>
  )
}

function PageRow({ ctx, page: p }: { ctx: PanelContext; page: MemoryPage }) {
  const summary = firstSentence(p.compiled)
  const meta = [
    p.kind,
    p.counts && p.counts.facts > 0 && plural(p.counts.facts, "fact"),
    p.counts && p.counts.linkedFrom > 0 && `linked from ${p.counts.linkedFrom}`,
    authorOf(p, ctx.names),
    `updated ${formatAge(p.updatedAt)}`,
  ].filter(Boolean)
  return (
    <Row
      icon={p.alwaysLoad ? PushPinIcon : NotebookIcon}
      onOpen={() => ctx.open(p.id)}
      text={
        <>
          <span className="font-medium">{p.title}</span>
          {summary && <span className="text-foreground/80">: {summary}</span>}
        </>
      }
      meta={
        <>
          <Ident>{p.slug}</Ident> · {meta.join(" · ")}
        </>
      }
      actions={
        <>
          <Button
            variant="ghost"
            size="icon-sm"
            title="Edit page"
            onClick={() => ctx.editPage(p)}
          >
            <PencilSimpleIcon />
          </Button>
          <PageMenu ctx={ctx} page={p} />
        </>
      }
    />
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

function FactSection({
  ctx,
  pending,
  facts,
  slugs,
  filtered,
}: {
  ctx: PanelContext
  pending: boolean
  facts: MemoryFact[]
  slugs: Map<string, string>
  filtered: boolean
}) {
  const [all, setAll] = useState(false)
  const [old, setOld] = useState(false)
  const current = facts.filter(
    (f) => f.status === "active" || f.status === "disputed"
  )
  const past = facts.length - current.length
  const list = old ? facts : current
  const shown = all || filtered ? list : list.slice(0, listLimit)
  return (
    <section className="space-y-2">
      <SectionHeader
        title="Facts"
        icon={BrainIcon}
        area="memory"
        count={pending ? undefined : current.length}
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
      {pending ? (
        <Spinner className="mx-auto block" />
      ) : (
        <Rows more={list.length - shown.length} onMore={() => setAll(true)}>
          {shown.length === 0 && (
            <EmptyRow>
              {filtered
                ? "No fact matches. Press Enter to search."
                : "No facts yet. Facts are short statements: preferences, decisions, procedures and events."}
            </EmptyRow>
          )}
          {shown.map((f) => (
            <FactRow key={f.id} ctx={ctx} fact={f} slugs={slugs} />
          ))}
        </Rows>
      )}
    </section>
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

function SessionsRow({ ctx }: { ctx: PanelContext }) {
  const sessions = $api.useQuery(
    "get",
    "/api/environments/{env}/memory/sessions",
    { params: { path: { env: ctx.env }, query: { teammate: ctx.teammate } } }
  )
  return (
    <Rows>
      <Row
        icon={ChatCircleDotsIcon}
        onOpen={ctx.sessions}
        text={
          <span className="font-medium">
            Sessions{" "}
            <span className="font-mono text-caption font-normal text-muted-foreground">
              {sessions.data?.length}
            </span>
          </span>
        }
        meta="What memory gave each session, and what it learned"
        actions={<CaretRightIcon className="size-3.5 text-muted-foreground" />}
      />
    </Rows>
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
    <section className="space-y-2">
      <SectionHeader
        title="Search results"
        icon={MagnifyingGlassIcon}
        area="memory"
        count={hits.data?.length}
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

function Crumbs({
  ctx,
  here,
  actions,
}: {
  ctx: PanelContext
  here: ReactNode
  actions?: ReactNode
}) {
  return (
    <div className="flex min-h-7 items-center gap-2 text-xs">
      <button
        type="button"
        className="text-muted-foreground hover:text-foreground"
        onClick={ctx.back}
      >
        Memory
      </button>
      <span className="text-muted-foreground">/</span>
      <span className="min-w-0 truncate">{here}</span>
      {actions && (
        <div className="ml-auto flex items-center gap-1">{actions}</div>
      )}
    </div>
  )
}

function PageView({ ctx, id }: { ctx: PanelContext; id: string }) {
  const { env } = ctx
  const queryClient = useQueryClient()
  const [entry, setEntry] = useState("")
  const [adding, setAdding] = useState(false)
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
        <Crumbs ctx={ctx} here="Page" />
        <p className="text-destructive">{errorMessage(page.error)}</p>
      </div>
    )
  if (!page.data) return <Spinner className="mx-auto block" />
  const p = page.data
  const links = new Map((p.links ?? []).map((l) => [l.target, l]))
  const own = (facts.data ?? []).filter(
    (f) =>
      factPageSlug(f) === p.slug &&
      (f.status === "active" || f.status === "disputed")
  )
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
    `${p.kind} page`,
    p.scope !== ctx.scope && scopeName(p.scope, ctx.names),
    authorOf(p, ctx.names),
    own.length > 0 && plural(own.length, "fact"),
    `updated ${formatAge(p.updatedAt)}`,
    p.alwaysLoad && "core: loads into every session",
  ].filter(Boolean)
  const owner =
    p.scope === sharedScope
      ? "every teammate's sandboxes"
      : `${ctx.names.get(p.scope.slice("teammate:".length)) ?? "the teammate"}'s sandboxes`
  const dir = p.scope === sharedScope ? "~/memory/shared" : "~/memory"

  return (
    <WikiLinks.Provider value={renderLink}>
      <article className="space-y-5">
        <Crumbs
          ctx={ctx}
          here={<Ident>{p.slug}</Ident>}
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
        <header className="space-y-1">
          <h2 className="font-heading text-lg font-semibold">{p.title}</h2>
          <p className="text-caption text-muted-foreground">
            {meta.join(" · ")}
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

        {own.length > 0 && (
          <section className="space-y-2">
            <SectionHeader
              title="Facts"
              icon={BrainIcon}
              area="memory"
              count={own.length}
            />
            <Rows>
              {own.map((f) => (
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

/** The first sentence of a page's compiled truth, without markdown marks and [[ ]]. */
function firstSentence(md: string) {
  for (const raw of md.split("\n")) {
    const line = raw
      .trim()
      .replace(/^(#+|[-*+]|\d+[.)]|>)\s+/, "")
      .replace(/\[\[([^[\]|\n]+)(?:\|([^[\]\n]+))?\]\]/g, (_, t, l) => l ?? t)
      .replace(/[*_`]/g, "")
    if (!line || raw.trim().startsWith("#")) continue
    const end = line.search(/[.!?](\s|$)/)
    return end < 0 ? line : line.slice(0, end + 1)
  }
  return ""
}
