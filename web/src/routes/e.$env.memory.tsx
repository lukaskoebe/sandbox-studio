import { useState, type FormEvent } from "react"
import { createFileRoute } from "@tanstack/react-router"
import { useQueryClient } from "@tanstack/react-query"
import { toast } from "sonner"
import {
  MagnifyingGlassIcon,
  PencilSimpleIcon,
  PlusIcon,
  ProhibitIcon,
  ShareNetworkIcon,
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
import { Checkbox } from "@/components/ui/checkbox"
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
import { Tabs, TabsContent, TabsList, TabsTrigger } from "@/components/ui/tabs"
import { PageHeader } from "@/components/page-header"
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
import { $api, errorMessage, fetchClient } from "@/lib/api/client"
import { formatAge } from "@/lib/utils"

export const Route = createFileRoute("/e/$env/memory")({
  component: MemoryPage,
})

const shared = "shared"

const explanation =
  "What personas remember about you and your work. Shared memory is visible to every persona; a persona's own scope only to that persona. Edits you make here are user-tier: they outrank anything inferred."

function MemoryPage() {
  const { env } = Route.useParams()
  const [scope, setScope] = useState(shared)
  const scopes = $api.useQuery("get", "/api/environments/{env}/memory/scopes", {
    params: { path: { env } },
  })
  const personas = $api.useQuery("get", "/api/environments/{env}/personas", {
    params: { path: { env } },
  })
  const names = new Map(personas.data?.map((p) => [p.id, p.name]))
  const label = (s: string) => {
    if (s === shared) return "Shared"
    const id = s.slice("persona:".length)
    return names.has(id) ? `${names.get(id)} (${s})` : s
  }
  // Shared, every scope with data, and the personas that have none yet.
  const choices = [
    ...new Set([
      shared,
      ...(scopes.data ?? []).map((s) => s.scope),
      ...(personas.data ?? []).map((p) => `persona:${p.id}`),
    ]),
  ]
  const current = scopes.data?.find((s) => s.scope === scope)

  return (
    <>
      <PageHeader>
        <h1 className="text-sm font-medium">Memory</h1>
        <div className="ml-auto w-64">
          <Select
            value={scope}
            onValueChange={(v) => v && setScope(v)}
            items={Object.fromEntries(choices.map((s) => [s, label(s)]))}
          >
            <SelectTrigger size="sm" className="w-full" aria-label="Scope">
              <SelectValue />
            </SelectTrigger>
            <SelectContent>
              {choices.map((s) => (
                <SelectItem key={s} value={s}>
                  {label(s)}
                </SelectItem>
              ))}
            </SelectContent>
          </Select>
        </div>
      </PageHeader>
      <div className="flex-1 space-y-4 overflow-auto p-4">
        <p className="max-w-2xl text-xs/relaxed text-muted-foreground">
          {explanation}
        </p>
        {current && (
          <p className="text-xs text-muted-foreground">
            {current.facts} facts, {current.pages} pages; core pages use{" "}
            {current.coreChars} of {current.coreBudget} characters.
          </p>
        )}
        <MemoryUsage env={env} />
        <Tabs defaultValue="pages">
          <TabsList>
            <TabsTrigger value="pages">Pages</TabsTrigger>
            <TabsTrigger value="facts">Facts</TabsTrigger>
            <TabsTrigger value="conflicts">Conflicts</TabsTrigger>
            <TabsTrigger value="search">Search</TabsTrigger>
            <TabsTrigger value="sessions">Sessions</TabsTrigger>
          </TabsList>
          <TabsContent value="pages">
            <PagesTab key={scope} env={env} scope={scope} />
          </TabsContent>
          <TabsContent value="facts">
            <FactsTab env={env} scope={scope} />
          </TabsContent>
          <TabsContent value="conflicts">
            <ConflictsTab env={env} scope={scope} />
          </TabsContent>
          <TabsContent value="search">
            <SearchTab env={env} scope={scope} />
          </TabsContent>
          <TabsContent value="sessions">
            <MemorySessionsTab
              env={env}
              persona={
                scope === shared ? undefined : scope.slice("persona:".length)
              }
              names={names}
            />
          </TabsContent>
        </Tabs>
      </div>
    </>
  )
}

// --- pages -------------------------------------------------------------------------------

function PagesTab({ env, scope }: { env: string; scope: string }) {
  const [selected, setSelected] = useState<string>()
  const [dialog, setDialog] = useState<{ page?: MemoryPage } | null>(null)
  const pages = $api.useQuery("get", "/api/environments/{env}/memory/pages", {
    params: { path: { env }, query: { scope } },
  })
  const list = pages.data ?? []
  const active = list.find((p) => p.id === selected) ?? list.at(0)

  return (
    <div className="space-y-3">
      <Button size="sm" variant="outline" onClick={() => setDialog({})}>
        <PlusIcon />
        New page
      </Button>
      {pages.isPending ? (
        <Spinner className="mx-auto block" />
      ) : list.length === 0 ? (
        <Empty>
          <EmptyHeader>
            <EmptyTitle>No pages in this scope</EmptyTitle>
            <EmptyDescription>
              Pages hold a compiled synthesis about a project, person or topic,
              with the timeline of what led to it.
            </EmptyDescription>
          </EmptyHeader>
        </Empty>
      ) : (
        <div className="grid gap-4 md:grid-cols-[14rem_1fr]">
          <ul className="space-y-0.5">
            {list.map((p) => (
              <li key={p.id}>
                <button
                  type="button"
                  className={`w-full rounded-md px-2 py-1 text-left hover:bg-muted ${p.id === active?.id ? "bg-muted font-medium" : ""}`}
                  onClick={() => setSelected(p.id)}
                >
                  <span className="block truncate">{p.title}</span>
                  <span className="block truncate font-mono text-[0.625rem] text-muted-foreground">
                    {p.slug}
                    {p.alwaysLoad && " · core"}
                  </span>
                </button>
              </li>
            ))}
          </ul>
          {active && (
            <PageView
              key={active.id}
              env={env}
              id={active.id}
              onEdit={(page) => setDialog({ page })}
            />
          )}
        </div>
      )}
      <PageDialog
        env={env}
        scope={scope}
        page={dialog?.page}
        open={dialog !== null}
        onOpenChange={(open) => {
          if (!open) setDialog(null)
        }}
        onSaved={(page) => setSelected(page.id)}
      />
    </div>
  )
}

function PageView({
  env,
  id,
  onEdit,
}: {
  env: string
  id: string
  onEdit: (page: MemoryPage) => void
}) {
  const queryClient = useQueryClient()
  const [entry, setEntry] = useState("")
  const [adding, setAdding] = useState(false)
  const page = $api.useQuery(
    "get",
    "/api/environments/{env}/memory/pages/{id}",
    { params: { path: { env, id } } }
  )
  const remove = $api.useMutation(
    "delete",
    "/api/environments/{env}/memory/pages/{id}",
    {
      onSettled: () => invalidateMemory(queryClient),
      onError: (err) =>
        toast.error("Could not delete the page", {
          description: errorMessage(err),
        }),
    }
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

  if (!page.data) return <Spinner className="mx-auto block" />
  const p = page.data
  return (
    <div className="min-w-0 space-y-4">
      <div className="flex flex-wrap items-center gap-1.5">
        <h2 className="mr-1 text-sm font-medium">{p.title}</h2>
        <Badge variant="outline">{p.kind}</Badge>
        <TierBadge tier={p.tier} />
        {p.alwaysLoad && <Badge>core</Badge>}
        {p.authorPersona && (
          <Badge variant="secondary">by persona:{p.authorPersona}</Badge>
        )}
        <span className="text-muted-foreground">
          updated {formatAge(p.updatedAt)}
        </span>
        <div className="ml-auto flex gap-0.5">
          <Button
            variant="ghost"
            size="icon-sm"
            title="Edit page"
            onClick={() => onEdit(p)}
          >
            <PencilSimpleIcon />
          </Button>
          <ConfirmButton
            title={`Delete ${p.slug}?`}
            description="The page and its timeline are removed for good."
            label="Delete page"
            onConfirm={() =>
              remove.mutate({ params: { path: { env, id: p.id } } })
            }
          />
        </div>
      </div>
      <pre className="rounded-lg bg-muted/50 p-3 font-mono text-xs/relaxed whitespace-pre-wrap">
        {p.compiled || "No compiled truth yet."}
      </pre>
      <div className="space-y-2">
        <h3 className="text-xs font-medium">Timeline</h3>
        <ol className="space-y-1.5 border-l pl-3">
          {(p.timeline ?? []).map((t) => (
            <li key={t.id}>
              <span className="text-muted-foreground">
                {new Date(t.at).toLocaleString()}
                {t.authorPersona && ` · persona:${t.authorPersona}`}
              </span>
              <pre className="font-sans whitespace-pre-wrap">{t.text}</pre>
            </li>
          ))}
        </ol>
        <form onSubmit={append} className="flex gap-2">
          <Input
            placeholder="Add a timeline entry"
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
      </div>
    </div>
  )
}

// --- facts -------------------------------------------------------------------------------

function TierBadge({ tier }: { tier: MemoryFact["tier"] }) {
  return <Badge variant={tier === "user" ? "default" : "outline"}>{tier}</Badge>
}

function StatusBadge({ status }: { status: MemoryFact["status"] }) {
  const variant =
    status === "active"
      ? "secondary"
      : status === "disputed"
        ? "destructive"
        : "outline"
  return <Badge variant={variant}>{status}</Badge>
}

function FactsTab({ env, scope }: { env: string; scope: string }) {
  const queryClient = useQueryClient()
  const [dialog, setDialog] = useState<{ fact?: MemoryFact } | null>(null)
  const facts = $api.useQuery("get", "/api/environments/{env}/memory/facts", {
    params: { path: { env }, query: { scope } },
  })
  const list = facts.data ?? []
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
            "Approve it in the inbox to share it with every persona.",
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

  return (
    <div className="space-y-3">
      <Button size="sm" variant="outline" onClick={() => setDialog({})}>
        <PlusIcon />
        Add fact
      </Button>
      {facts.isPending ? (
        <Spinner className="mx-auto block" />
      ) : list.length === 0 ? (
        <Empty>
          <EmptyHeader>
            <EmptyTitle>No facts in this scope</EmptyTitle>
            <EmptyDescription>
              Facts are short statements: preferences, decisions, procedures and
              events.
            </EmptyDescription>
          </EmptyHeader>
        </Empty>
      ) : (
        <div className="rounded-lg ring-1 ring-foreground/10">
          <Table>
            <TableHeader>
              <TableRow>
                <TableHead>Fact</TableHead>
                <TableHead>Kind</TableHead>
                <TableHead>Tier</TableHead>
                <TableHead>Status</TableHead>
                <TableHead>Observed</TableHead>
                <TableHead>
                  <span className="sr-only">Actions</span>
                </TableHead>
              </TableRow>
            </TableHeader>
            <TableBody>
              {list.map((f) => (
                <TableRow key={f.id}>
                  <TableCell className="max-w-md">
                    <span className="block whitespace-pre-wrap">{f.text}</span>
                    {(f.attribute || f.authorPersona) && (
                      <span className="block font-mono text-[0.625rem] text-muted-foreground">
                        {f.attribute}
                        {f.attribute && f.authorPersona && " · "}
                        {f.authorPersona && `by persona:${f.authorPersona}`}
                      </span>
                    )}
                  </TableCell>
                  <TableCell>{f.kind}</TableCell>
                  <TableCell>
                    <TierBadge tier={f.tier} />
                  </TableCell>
                  <TableCell>
                    <StatusBadge status={f.status} />
                  </TableCell>
                  <TableCell className="text-muted-foreground">
                    {formatAge(f.observedAt)}
                  </TableCell>
                  <TableCell>
                    <div className="flex justify-end gap-0.5">
                      <Button
                        variant="ghost"
                        size="icon-sm"
                        title="Edit fact"
                        onClick={() => setDialog({ fact: f })}
                      >
                        <PencilSimpleIcon />
                      </Button>
                      {scope !== shared && f.status === "active" && (
                        <Button
                          variant="ghost"
                          size="icon-sm"
                          title="Promote to shared: propose it for every persona"
                          disabled={promote.isPending}
                          onClick={() =>
                            promote.mutate({
                              params: { path: { env, id: f.id } },
                            })
                          }
                        >
                          <ShareNetworkIcon />
                        </Button>
                      )}
                      {f.status !== "retracted" && (
                        <Button
                          variant="ghost"
                          size="icon-sm"
                          title="Retract: keep it on record, never recall it"
                          onClick={() =>
                            retract.mutate({
                              params: { path: { env, id: f.id } },
                            })
                          }
                        >
                          <ProhibitIcon />
                        </Button>
                      )}
                      <ConfirmButton
                        title="Delete this fact?"
                        description="It is removed for good. Retract it instead to keep it on record."
                        label="Delete fact"
                        onConfirm={() =>
                          remove.mutate({
                            params: { path: { env, id: f.id } },
                          })
                        }
                      />
                    </div>
                  </TableCell>
                </TableRow>
              ))}
            </TableBody>
          </Table>
        </div>
      )}
      <FactDialog
        env={env}
        scope={scope}
        fact={dialog?.fact}
        open={dialog !== null}
        onOpenChange={(open) => {
          if (!open) setDialog(null)
        }}
      />
    </div>
  )
}

// --- conflicts ---------------------------------------------------------------------------

function ConflictsTab({ env, scope }: { env: string; scope: string }) {
  const [open, setOpen] = useState<string>()
  const conflicts = $api.useQuery(
    "get",
    "/api/environments/{env}/memory/conflicts",
    { params: { path: { env }, query: { scope } } }
  )
  const list = conflicts.data ?? []

  return (
    <div className="space-y-3">
      <DreamPanel env={env} scope={scope} />
      {conflicts.isPending ? (
        <Spinner className="mx-auto block" />
      ) : list.length === 0 ? (
        <Empty>
          <EmptyHeader>
            <EmptyTitle>No conflicts</EmptyTitle>
            <EmptyDescription>
              Facts that contradict each other show up here. Until you resolve
              one, a shared fact wins over a persona's.
            </EmptyDescription>
          </EmptyHeader>
        </Empty>
      ) : (
        list.map((c) => (
          <div
            key={c.id}
            className="space-y-2 rounded-lg p-3 ring-1 ring-foreground/10"
          >
            <div className="flex flex-wrap items-center gap-1.5">
              <Badge variant="outline">{c.verdict.replaceAll("_", " ")}</Badge>
              <Badge
                variant={c.status === "open" ? "destructive" : "secondary"}
              >
                {c.status === "open" ? "open" : `resolved: ${c.resolution}`}
              </Badge>
              <span className="text-muted-foreground">
                {formatAge(c.createdAt)}
              </span>
              <Button
                size="sm"
                variant={c.status === "open" ? "default" : "outline"}
                className="ml-auto"
                onClick={() => setOpen(c.id)}
              >
                {c.status === "open" ? "Resolve…" : "Details"}
              </Button>
            </div>
            {c.reason && <p className="text-muted-foreground">{c.reason}</p>}
            {c.note && <p className="text-muted-foreground">{c.note}</p>}
            <div className="grid gap-2 md:grid-cols-2">
              {(
                [
                  ["A", c.factA],
                  ["B", c.factB],
                ] as const
              ).map(([name, fact]) => (
                <div key={name} className="rounded-md bg-muted/50 p-2">
                  <span className="font-medium">{name}</span>{" "}
                  <span className="font-mono text-[0.625rem] text-muted-foreground">
                    {fact.scope}
                  </span>
                  <p className="whitespace-pre-wrap">{fact.text}</p>
                  <div className="mt-1 flex gap-1">
                    <TierBadge tier={fact.tier} />
                    <StatusBadge status={fact.status} />
                  </div>
                </div>
              ))}
            </div>
          </div>
        ))
      )}
      <ConflictSheet
        env={env}
        conflictId={open}
        onOpenChange={(o) => {
          if (!o) setOpen(undefined)
        }}
      />
    </div>
  )
}

// --- search ------------------------------------------------------------------------------

function SearchTab({ env, scope }: { env: string; scope: string }) {
  const [draft, setDraft] = useState("")
  const [query, setQuery] = useState("")
  const [includeSuperseded, setIncludeSuperseded] = useState(false)
  const persona = scope.startsWith("persona:")
    ? scope.slice("persona:".length)
    : undefined
  const hits = $api.useQuery(
    "get",
    "/api/environments/{env}/memory/search",
    {
      params: {
        path: { env },
        query: { q: query, persona, includeSuperseded, limit: 20 },
      },
    },
    { enabled: query !== "" }
  )

  return (
    <div className="space-y-3">
      <form
        className="flex flex-wrap items-center gap-3"
        onSubmit={(e) => {
          e.preventDefault()
          setQuery(draft.trim())
        }}
      >
        <Input
          className="max-w-md"
          placeholder={
            persona
              ? "Search this persona's memory and shared memory"
              : "Search shared memory"
          }
          maxLength={500}
          value={draft}
          onChange={(e) => setDraft(e.target.value)}
        />
        <Button type="submit" size="sm" disabled={!draft.trim()}>
          <MagnifyingGlassIcon />
          Search
        </Button>
        <label className="flex items-center gap-2">
          <Checkbox
            checked={includeSuperseded}
            onCheckedChange={setIncludeSuperseded}
          />
          Include superseded
        </label>
      </form>
      {hits.isFetching && <Spinner className="mx-auto block" />}
      {hits.error && (
        <p className="text-destructive">{errorMessage(hits.error)}</p>
      )}
      {hits.data &&
        (hits.data.length === 0 ? (
          <p className="text-muted-foreground">Nothing found.</p>
        ) : (
          <ol className="space-y-2">
            {hits.data.map((h) => (
              <SearchHit key={`${h.type}-${h.id}`} hit={h} />
            ))}
          </ol>
        ))}
    </div>
  )
}

function SearchHit({ hit }: { hit: MemoryHit }) {
  const title = hit.page
    ? hit.page.title
    : hit.fact
      ? `${hit.fact.kind}${hit.fact.attribute ? ` · ${hit.fact.attribute}` : ""}`
      : hit.id
  return (
    <li className="space-y-1 rounded-lg p-3 ring-1 ring-foreground/10">
      <div className="flex flex-wrap items-center gap-1.5">
        <Badge variant="outline">{hit.type}</Badge>
        <span className="font-medium">{title}</span>
        <span className="font-mono text-[0.625rem] text-muted-foreground">
          {hit.scope}
        </span>
        {hit.disputed && <Badge variant="destructive">disputed</Badge>}
        {hit.superseded && <Badge variant="outline">superseded</Badge>}
        <span className="ml-auto font-mono text-muted-foreground">
          {hit.score.toFixed(4)}
        </span>
      </div>
      <p className="whitespace-pre-wrap">{hit.snippet}</p>
      <p className="text-muted-foreground">
        <span className="font-medium">Why: </span>
        {hit.why.summary}
      </p>
    </li>
  )
}

// --- shared ------------------------------------------------------------------------------

function ConfirmButton({
  title,
  description,
  label,
  onConfirm,
}: {
  title: string
  description: string
  label: string
  onConfirm: () => void
}) {
  return (
    <AlertDialog>
      <AlertDialogTrigger
        render={<Button variant="ghost" size="icon-sm" title={label} />}
      >
        <TrashIcon />
      </AlertDialogTrigger>
      <AlertDialogContent>
        <AlertDialogHeader>
          <AlertDialogTitle>{title}</AlertDialogTitle>
          <AlertDialogDescription>{description}</AlertDialogDescription>
        </AlertDialogHeader>
        <AlertDialogFooter>
          <AlertDialogCancel>Cancel</AlertDialogCancel>
          <AlertDialogAction variant="destructive" onClick={onConfirm}>
            Delete
          </AlertDialogAction>
        </AlertDialogFooter>
      </AlertDialogContent>
    </AlertDialog>
  )
}
