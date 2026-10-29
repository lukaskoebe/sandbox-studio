import { useEffect, useRef, useState } from "react"
import { useQueries } from "@tanstack/react-query"
import {
  CheckCircleIcon,
  CircleIcon,
  LockSimpleIcon,
  WarningCircleIcon,
} from "@phosphor-icons/react"
import { Button } from "@/components/ui/button"
import { Checkbox } from "@/components/ui/checkbox"
import { FieldDescription, FieldLabel } from "@/components/ui/field"
import { Input } from "@/components/ui/input"
import { Spinner } from "@/components/ui/spinner"
import { phase } from "@/components/status-badge"
import { $api, errorMessage, type Forge, type Sandbox } from "@/lib/api/client"
import { canListRepositories } from "@/lib/forges"

/** A repository of a forge, as a clone names it. */
export type RepoChoice = { forgeId: string; forge: string; path: string }

const key = (r: RepoChoice) => `${r.forgeId}/${r.path}`

/** The environment's forges that can list their repositories. */
function useListableForges(env: string): Forge[] {
  const forges = $api.useQuery("get", "/api/environments/{env}/forges", {
    params: { path: { env } },
  })
  return (forges.data ?? []).filter(canListRepositories)
}

/**
 * A searchable checklist of the repositories of the environment's forges that have a list:
 * the API forges and folders on this computer. It renders nothing without such a forge.
 */
export function RepositoryChecklist({
  env,
  value,
  onChange,
  disabled,
}: {
  env: string
  value: RepoChoice[]
  onChange: (repos: RepoChoice[]) => void
  disabled?: boolean
}) {
  const forges = useListableForges(env)
  const lists = useQueries({
    queries: forges.map((f) =>
      $api.queryOptions(
        "get",
        "/api/environments/{env}/forges/{id}/repositories",
        { params: { path: { env, id: f.id } } },
        { staleTime: 60_000, retry: false }
      )
    ),
  })
  const [search, setSearch] = useState("")
  if (!forges.length) return null

  const several = forges.length > 1
  const needle = search.trim().toLowerCase()
  const rows = forges.flatMap((f, i) =>
    (lists[i]?.data ?? []).map((r) => ({
      choice: { forgeId: f.id, forge: f.name, path: r.path },
      label: several ? `${f.name}/${r.path}` : r.path,
      repo: r,
    }))
  )
  const shown = rows.filter(
    (r) =>
      !needle ||
      r.label.toLowerCase().includes(needle) ||
      r.repo.description?.toLowerCase().includes(needle)
  )
  const chosen = new Set(value.map(key))
  const loading = lists.some((l) => l.isPending)
  const toggle = (c: RepoChoice, on: boolean) =>
    onChange(on ? [...value, c] : value.filter((v) => key(v) !== key(c)))

  return (
    <div className="grid gap-2">
      <FieldLabel htmlFor="clone-search">Clone repositories</FieldLabel>
      <Input
        id="clone-search"
        autoComplete="off"
        placeholder="Search repositories"
        value={search}
        disabled={disabled}
        onChange={(e) => setSearch(e.target.value)}
      />
      <div
        role="group"
        aria-label="Repositories"
        className="max-h-56 overflow-y-auto rounded-md border"
      >
        {loading && !rows.length && (
          <p className="flex items-center gap-2 p-2 text-xs text-muted-foreground">
            <Spinner /> Listing repositories…
          </p>
        )}
        {lists.map(
          (l, i) =>
            l.error && (
              <p key={forges[i].id} className="p-2 text-xs text-destructive">
                {forges[i].name}: {errorMessage(l.error)}
              </p>
            )
        )}
        {!loading && !shown.length && (
          <p className="p-2 text-xs text-muted-foreground">
            {rows.length ? "No repository matches." : "No repositories."}
          </p>
        )}
        {shown.map((r) => (
          <label
            key={key(r.choice)}
            className="flex cursor-pointer items-center gap-2 px-2 py-1.5 text-xs hover:bg-muted"
          >
            <Checkbox
              checked={chosen.has(key(r.choice))}
              disabled={disabled}
              onCheckedChange={(on) => toggle(r.choice, on)}
            />
            <span className="min-w-0 truncate font-mono">{r.label}</span>
            {r.repo.private && (
              <LockSimpleIcon
                aria-label="Private"
                className="size-3 shrink-0 text-muted-foreground"
              />
            )}
            {r.repo.description && (
              <span className="min-w-0 truncate text-muted-foreground">
                {r.repo.description}
              </span>
            )}
          </label>
        ))}
      </div>
      <FieldDescription>
        {value.length ? `${value.length} selected. ` : ""}Each is cloned into
        /workspace through Studio, so its pushes come to you for review.
      </FieldDescription>
    </div>
  )
}

type CloneState = "waiting" | "cloning" | "done" | { error: string }

const stateWords = {
  waiting: "Waiting",
  cloning: "Cloning",
  done: "Cloned",
} as const

/**
 * Clones repositories into a sandbox once it runs, one after the other, and shows how each
 * went. queued are the repositories picked before the sandbox existed; they are cloned as
 * soon as it is ready. Until a clone starts, repositories can be picked here too.
 */
export function CloneRepositories({
  env,
  sandbox,
  queued,
}: {
  env: string
  sandbox: Sandbox
  queued: RepoChoice[]
}) {
  const ready = phase(sandbox) === "ready"
  const [picked, setPicked] = useState<RepoChoice[]>(queued)
  const [states, setStates] = useState<Record<string, CloneState>>()
  const clone = $api.useMutation(
    "post",
    "/api/environments/{env}/sandboxes/{id}/clone"
  )
  const forges = useListableForges(env)

  const start = async (repos: RepoChoice[]) => {
    setStates(Object.fromEntries(repos.map((r) => [key(r), "waiting"])))
    for (const r of repos) {
      setStates((s) => ({ ...s, [key(r)]: "cloning" }))
      try {
        await clone.mutateAsync({
          params: { path: { env, id: sandbox.id } },
          body: { forgeId: r.forgeId, repo: r.path },
        })
        setStates((s) => ({ ...s, [key(r)]: "done" }))
      } catch (err) {
        setStates((s) => ({ ...s, [key(r)]: { error: errorMessage(err) } }))
      }
    }
  }

  // The repositories picked with the sandbox are cloned once, when it is ready.
  const auto = useRef(false)
  useEffect(() => {
    if (auto.current || !ready || !queued.length) return
    auto.current = true
    void start(queued)
  }, [ready, queued])

  if (states) {
    return (
      <ul className="grid gap-1.5 text-xs" aria-label="Clones">
        {picked.map((r) => {
          const st = states[key(r)] ?? "waiting"
          return (
            <li key={key(r)} className="flex items-start gap-2">
              <CloneIcon state={st} />
              <span className="grid min-w-0 gap-0.5">
                <span className="truncate font-mono">
                  {forges.length > 1 ? `${r.forge}/` : ""}
                  {r.path}
                </span>
                {typeof st === "object" ? (
                  <span className="text-destructive">{st.error}</span>
                ) : (
                  <span className="sr-only">{stateWords[st]}</span>
                )}
              </span>
            </li>
          )
        })}
      </ul>
    )
  }
  if (!ready || !forges.length) return null
  return (
    <div className="grid gap-2">
      <RepositoryChecklist env={env} value={picked} onChange={setPicked} />
      <div>
        <Button
          size="sm"
          variant="outline"
          disabled={!picked.length}
          onClick={() => void start(picked)}
        >
          {picked.length > 1 ? `Clone ${picked.length} repositories` : "Clone"}
        </Button>
      </div>
    </div>
  )
}

function CloneIcon({ state }: { state: CloneState }) {
  if (state === "cloning") return <Spinner className="mt-px shrink-0" />
  if (state === "done")
    return (
      <CheckCircleIcon className="size-4 shrink-0 text-emerald-600 dark:text-emerald-400" />
    )
  if (typeof state === "object")
    return <WarningCircleIcon className="size-4 shrink-0 text-destructive" />
  return <CircleIcon className="size-4 shrink-0 text-muted-foreground/50" />
}
