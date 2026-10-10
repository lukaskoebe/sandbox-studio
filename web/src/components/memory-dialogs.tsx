import { useState, type FormEvent } from "react"
import { useQueryClient, type QueryClient } from "@tanstack/react-query"
import { Button } from "@/components/ui/button"
import { Checkbox } from "@/components/ui/checkbox"
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog"
import {
  Field,
  FieldDescription,
  FieldError,
  FieldLabel,
} from "@/components/ui/field"
import { Input } from "@/components/ui/input"
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select"
import { Spinner } from "@/components/ui/spinner"
import { errorMessage, fetchClient } from "@/lib/api/client"
import type { components } from "@/lib/api/schema.gen"

export type MemoryFact = components["schemas"]["Fact"]
export type MemoryPage = components["schemas"]["Page"]
export type MemoryScope = components["schemas"]["Scope"]
export type MemoryConflict = components["schemas"]["Conflict"]
export type MemoryHit = components["schemas"]["Hit"]

export const factKinds: MemoryFact["kind"][] = [
  "preference",
  "decision",
  "fact",
  "procedure",
  "event",
]
export const pageKinds: MemoryPage["kind"][] = [
  "topic",
  "project",
  "person",
  "procedure",
  "persona-self",
]

/** Refetches every memory query of the environment after a change. */
export function invalidateMemory(queryClient: QueryClient) {
  return queryClient.invalidateQueries({
    predicate: (q) =>
      typeof q.queryKey[1] === "string" &&
      q.queryKey[1].startsWith("/api/environments/{env}/memory"),
  })
}

export const textareaClass =
  "w-full min-w-0 resize-y rounded-md border border-input bg-input/20 px-2 py-1 font-mono text-sm outline-none placeholder:text-muted-foreground focus-visible:border-ring focus-visible:ring-2 focus-visible:ring-ring/30 md:text-xs/relaxed dark:bg-input/30"

function KindSelect<T extends string>({
  id,
  value,
  kinds,
  onChange,
}: {
  id: string
  value: T
  kinds: T[]
  onChange: (kind: T) => void
}) {
  return (
    <Select
      value={value}
      onValueChange={(v) => v && onChange(v)}
      items={Object.fromEntries(kinds.map((k) => [k, k]))}
    >
      <SelectTrigger id={id} className="w-full">
        <SelectValue />
      </SelectTrigger>
      <SelectContent>
        {kinds.map((k) => (
          <SelectItem key={k} value={k}>
            {k}
          </SelectItem>
        ))}
      </SelectContent>
    </Select>
  )
}

export function FactDialog({
  env,
  scope,
  fact,
  open,
  onOpenChange,
}: {
  env: string
  /** The scope a new fact goes into. */
  scope: string
  /** The fact to edit; absent for a new one. */
  fact?: MemoryFact
  open: boolean
  onOpenChange: (open: boolean) => void
}) {
  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent>
        <FactForm
          key={fact?.id ?? "new"}
          env={env}
          scope={scope}
          fact={fact}
          onDone={() => onOpenChange(false)}
        />
      </DialogContent>
    </Dialog>
  )
}

function FactForm({
  env,
  scope,
  fact,
  onDone,
}: {
  env: string
  scope: string
  fact?: MemoryFact
  onDone: () => void
}) {
  const queryClient = useQueryClient()
  const [kind, setKind] = useState<MemoryFact["kind"]>(fact?.kind ?? "fact")
  const [text, setText] = useState(fact?.text ?? "")
  const [attribute, setAttribute] = useState(fact?.attribute ?? "")
  const [error, setError] = useState<string>()
  const [saving, setSaving] = useState(false)

  const save = async (e: FormEvent) => {
    e.preventDefault()
    if (!text.trim() || saving) return
    setSaving(true)
    setError(undefined)
    const body = {
      kind,
      text: text.trim(),
      attribute: attribute.trim() || undefined,
    }
    try {
      const result = fact
        ? await fetchClient.PUT("/api/environments/{env}/memory/facts/{id}", {
            params: { path: { env, id: fact.id } },
            body,
          })
        : await fetchClient.POST("/api/environments/{env}/memory/facts", {
            params: { path: { env } },
            body: { ...body, scope },
          })
      if (result.error) {
        setError(errorMessage(result.error))
        return
      }
      await invalidateMemory(queryClient)
      onDone()
    } catch (err) {
      setError(errorMessage(err))
    } finally {
      setSaving(false)
    }
  }

  return (
    <form onSubmit={save} className="space-y-4">
      <DialogHeader>
        <DialogTitle>{fact ? "Edit fact" : "Add fact"}</DialogTitle>
        <DialogDescription>
          {fact
            ? "Your edit makes this a user-tier fact, which ranks above inferred ones."
            : `A user-tier fact in ${scope}.`}
        </DialogDescription>
      </DialogHeader>
      <Field>
        <FieldLabel htmlFor="fact-kind">Kind</FieldLabel>
        <KindSelect
          id="fact-kind"
          value={kind}
          kinds={factKinds}
          onChange={setKind}
        />
      </Field>
      <Field>
        <FieldLabel htmlFor="fact-text">Text</FieldLabel>
        <textarea
          id="fact-text"
          rows={4}
          maxLength={2000}
          className={textareaClass}
          value={text}
          onChange={(e) => setText(e.target.value)}
        />
      </Field>
      <Field>
        <FieldLabel htmlFor="fact-attribute">Attribute</FieldLabel>
        <Input
          id="fact-attribute"
          maxLength={128}
          placeholder="deploy.command"
          value={attribute}
          onChange={(e) => setAttribute(e.target.value)}
        />
        <FieldDescription>
          Optional key that newer facts about the same thing share.
        </FieldDescription>
      </Field>
      {error && <FieldError>{error}</FieldError>}
      <DialogFooter>
        <Button type="button" variant="outline" onClick={onDone}>
          Cancel
        </Button>
        <Button type="submit" disabled={!text.trim() || saving}>
          {saving && <Spinner />}
          Save
        </Button>
      </DialogFooter>
    </form>
  )
}

export function PageDialog({
  env,
  scope,
  page,
  open,
  onOpenChange,
  onSaved,
}: {
  env: string
  scope: string
  page?: MemoryPage
  open: boolean
  onOpenChange: (open: boolean) => void
  onSaved?: (page: MemoryPage) => void
}) {
  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent className="sm:max-w-2xl">
        <PageForm
          key={page?.id ?? "new"}
          env={env}
          scope={scope}
          page={page}
          onDone={(saved) => {
            if (saved) onSaved?.(saved)
            onOpenChange(false)
          }}
        />
      </DialogContent>
    </Dialog>
  )
}

function PageForm({
  env,
  scope,
  page,
  onDone,
}: {
  env: string
  scope: string
  page?: MemoryPage
  onDone: (saved?: MemoryPage) => void
}) {
  const queryClient = useQueryClient()
  const [slug, setSlug] = useState(page?.slug ?? "")
  const [title, setTitle] = useState(page?.title ?? "")
  const [kind, setKind] = useState<MemoryPage["kind"]>(page?.kind ?? "topic")
  const [compiled, setCompiled] = useState(page?.compiled ?? "")
  const [alwaysLoad, setAlwaysLoad] = useState(page?.alwaysLoad ?? false)
  const [error, setError] = useState<string>()
  const [saving, setSaving] = useState(false)
  const canSave = title.trim() !== "" && (page !== undefined || slug !== "")

  const save = async (e: FormEvent) => {
    e.preventDefault()
    if (!canSave || saving) return
    setSaving(true)
    setError(undefined)
    const body = { title: title.trim(), kind, compiled, alwaysLoad }
    try {
      const result = page
        ? await fetchClient.PUT("/api/environments/{env}/memory/pages/{id}", {
            params: { path: { env, id: page.id } },
            body,
          })
        : await fetchClient.POST("/api/environments/{env}/memory/pages", {
            params: { path: { env } },
            body: { ...body, scope, slug: slug.trim() },
          })
      if (result.error) {
        setError(errorMessage(result.error))
        return
      }
      await invalidateMemory(queryClient)
      onDone(result.data)
    } catch (err) {
      setError(errorMessage(err))
    } finally {
      setSaving(false)
    }
  }

  return (
    <form onSubmit={save} className="space-y-4">
      <DialogHeader>
        <DialogTitle>{page ? `Edit ${page.slug}` : "New page"}</DialogTitle>
        <DialogDescription>
          The compiled truth is the current synthesis; the timeline below it
          keeps the history and cannot be edited.
        </DialogDescription>
      </DialogHeader>
      <div className="grid gap-4 sm:grid-cols-2">
        {!page && (
          <Field>
            <FieldLabel htmlFor="page-slug">Slug</FieldLabel>
            <Input
              id="page-slug"
              placeholder="project/sandbox-studio"
              value={slug}
              onChange={(e) => setSlug(e.target.value.toLowerCase())}
            />
          </Field>
        )}
        <Field>
          <FieldLabel htmlFor="page-title">Title</FieldLabel>
          <Input
            id="page-title"
            maxLength={200}
            value={title}
            onChange={(e) => setTitle(e.target.value)}
          />
        </Field>
        <Field>
          <FieldLabel htmlFor="page-kind">Kind</FieldLabel>
          <KindSelect
            id="page-kind"
            value={kind}
            kinds={pageKinds}
            onChange={setKind}
          />
        </Field>
      </div>
      <Field>
        <FieldLabel htmlFor="page-compiled">Compiled truth</FieldLabel>
        <textarea
          id="page-compiled"
          rows={12}
          className={textareaClass}
          value={compiled}
          onChange={(e) => setCompiled(e.target.value)}
        />
      </Field>
      <Field orientation="horizontal">
        <Checkbox
          id="page-core"
          checked={alwaysLoad}
          onCheckedChange={setAlwaysLoad}
        />
        <FieldLabel htmlFor="page-core">
          Core page: load into every session of this scope
        </FieldLabel>
      </Field>
      {error && <FieldError>{error}</FieldError>}
      <DialogFooter>
        <Button type="button" variant="outline" onClick={() => onDone()}>
          Cancel
        </Button>
        <Button type="submit" disabled={!canSave || saving}>
          {saving && <Spinner />}
          Save
        </Button>
      </DialogFooter>
    </form>
  )
}
