import { useId, type ReactNode } from "react"
import { useQueryClient } from "@tanstack/react-query"
import { Link } from "@tanstack/react-router"
import { toast } from "sonner"
import { ShieldCheckIcon } from "@phosphor-icons/react"
import { SectionHeader } from "@/components/section-header"
import { Badge } from "@/components/ui/badge"
import { $api, errorMessage, type Teammate } from "@/lib/api/client"
import { cn } from "@/lib/utils"

/** The four security levels, from the most closed to the most open (docs/network.md). */
export const securityLevels = [
  { value: "locked", label: "Locked" },
  { value: "guarded", label: "Guarded" },
  { value: "assisted", label: "Assisted" },
  { value: "open", label: "Open" },
] as const

export type SecurityLevel = (typeof securityLevels)[number]["value"]

/** The environment's level, read from the environment list, and a way to change it. */
export function useEnvironmentLevel(env: string) {
  const queryClient = useQueryClient()
  const environments = $api.useQuery("get", "/api/environments")
  const set = $api.useMutation(
    "put",
    "/api/environments/{env}/security-level",
    {
      onSuccess: () =>
        queryClient.invalidateQueries({
          queryKey: ["get", "/api/environments"],
        }),
      onError: (err) =>
        toast.error("Could not change the security level", {
          description: errorMessage(err),
        }),
    }
  )
  const level = environments.data?.find((e) => e.id === env)?.securityLevel
  return {
    level,
    saving: set.isPending,
    setLevel: (value: SecurityLevel) =>
      set.mutate({ params: { path: { env } }, body: { level: value } }),
  }
}

/** What a level lets through and what it asks about, with examples. */
function levelExamples(
  env: string,
  level: SecurityLevel
): [ReactNode, ReactNode] {
  const curated = (
    <Link
      to="/e/$env/settings/network"
      params={{ env }}
      hash="curated-list"
      className="underline underline-offset-4"
    >
      curated list
    </Link>
  )
  return {
    locked: [
      "Only hosts your rules allow.",
      "Every other host: even npm install asks.",
    ] as [ReactNode, ReactNode],
    guarded: [
      <>
        npm, PyPI, Go, crates, Docker Hub, GitHub reads and the rest of the{" "}
        {curated}.
      </>,
      "Every other host.",
    ] as [ReactNode, ReactNode],
    assisted: [
      <>The {curated}, and the hosts an AI reviewer allows.</>,
      "The hosts the reviewer is unsure about.",
    ] as [ReactNode, ReactNode],
    open: [
      "Any public host, logged in Activity.",
      "No public host. Your deny rules and the tracker list still apply.",
    ] as [ReactNode, ReactNode],
  }[level]
}

/** Who reviews at Assisted: the environment's utility model, or nobody, so it asks like Guarded. */
function ReviewerLine({ env }: { env: string }) {
  const reviewer = $api.useQuery("get", "/api/environments/{env}/reviewer", {
    params: { path: { env } },
  })
  if (!reviewer.data) return null
  return (
    <p className="text-muted-foreground @xl:col-span-2">
      {reviewer.data.available
        ? `Reviewer: ${reviewer.data.model} on ${reviewer.data.provider}. Its decisions show in Activity, where you can revert them.`
        : "No provider has a utility model yet, so it asks like Guarded."}
    </p>
  )
}

/** The grid every row of the picker shares, so the columns line up under their heads. */
const levelColumns = "@xl:grid-cols-[11rem_minmax(0,1fr)_minmax(0,1fr)]"

/**
 * One level as a row of the picker: the radio and the level's name, then what it lets through
 * and what it asks about. Where the picker is wide these sit in columns under one head each;
 * where it is narrow they stack, each with its own label.
 */
function LevelRow({
  name,
  value,
  checked,
  onSelect,
  title,
  badge,
  children,
}: {
  name: string
  value: string
  checked: boolean
  onSelect: () => void
  title: string
  badge?: string
  children: ReactNode
}) {
  const id = `${name}-${value || "default"}`
  return (
    <label
      className={cn(
        "grid cursor-pointer gap-x-4 gap-y-1.5 px-3 py-2.5 text-xs/relaxed transition-colors hover:bg-muted/40 has-checked:bg-primary/5 has-checked:shadow-[inset_2px_0_0_var(--color-primary)] has-focus-visible:outline-2 has-focus-visible:-outline-offset-2 has-focus-visible:outline-ring has-disabled:cursor-default",
        levelColumns
      )}
    >
      <span className="flex items-start gap-2 self-start">
        <input
          type="radio"
          name={name}
          value={value}
          checked={checked}
          onChange={onSelect}
          aria-labelledby={id}
          aria-describedby={`${id}-text`}
          className="mt-[0.1875rem] size-3.5 shrink-0 appearance-none rounded-full border border-input bg-card outline-none checked:border-primary checked:bg-primary checked:shadow-[inset_0_0_0_3px_var(--color-card)] disabled:opacity-50"
        />
        <span className="flex flex-wrap items-center gap-x-2 gap-y-1">
          <span id={id} className="text-[0.8125rem] font-semibold">
            {title}
          </span>
          {badge && <Badge variant="secondary">{badge}</Badge>}
        </span>
      </span>
      <div
        id={`${id}-text`}
        className="grid gap-x-4 gap-y-1.5 @max-xl:pl-5.5 @xl:col-span-2 @xl:grid-cols-subgrid"
      >
        {children}
      </div>
    </label>
  )
}

/** A level's two cells; the labels show where there are no column heads to say them. */
function LevelCells({ allows, asks }: { allows: ReactNode; asks: ReactNode }) {
  return (
    <>
      <p>
        <span className="block text-muted-foreground @xl:sr-only">
          Connects without asking
        </span>
        {allows}
      </p>
      <p>
        <span className="block text-muted-foreground @xl:sr-only">
          Asks you
        </span>
        {asks}
      </p>
    </>
  )
}

/**
 * The four levels as radio rows in one card, from the most closed to the most open. Each says
 * what connects without asking and what asks, in two columns where there is room, so the levels
 * compare at a glance. A teammate's picker adds the environment's default before them.
 */
export function SecurityLevelPicker({
  env,
  value,
  onChange,
  disabled,
  environmentDefault,
}: {
  env: string
  value?: string
  /** The level chosen, or "" for the environment's default. */
  onChange: (value: SecurityLevel | "") => void
  disabled?: boolean
  /** For a teammate: offers "Environment default", the environment's level, as "". */
  environmentDefault?: { level?: string }
}) {
  const name = useId()
  const inherited = securityLevels.find(
    (l) => l.value === environmentDefault?.level
  )
  return (
    <fieldset
      disabled={disabled || value === undefined}
      className="@container grid min-w-0 gap-2"
    >
      <legend className="sr-only">Security level</legend>
      <div className="divide-y overflow-hidden rounded-(--radius) border bg-card">
        <div
          aria-hidden
          className={cn(
            "hidden gap-x-4 bg-muted/30 px-3 py-1.5 text-[0.6875rem] font-medium text-muted-foreground @xl:grid",
            levelColumns
          )}
        >
          <span>Level</span>
          <span>Connects without asking</span>
          <span>Asks you</span>
        </div>
        {environmentDefault && (
          <LevelRow
            name={name}
            value=""
            checked={value === ""}
            onSelect={() => onChange("")}
            title={
              inherited
                ? `Environment default (${inherited.label})`
                : "Environment default"
            }
          >
            <p className="text-muted-foreground @xl:col-span-2">
              Follows the environment's level, set in Settings → Network.
            </p>
          </LevelRow>
        )}
        {securityLevels.map((l) => {
          const [allows, asks] = levelExamples(env, l.value)
          return (
            <LevelRow
              key={l.value}
              name={name}
              value={l.value}
              checked={value === l.value}
              onSelect={() => onChange(l.value)}
              title={l.label}
              badge={l.value === "guarded" ? "Recommended" : undefined}
            >
              <LevelCells allows={allows} asks={asks} />
              {l.value === "assisted" && <ReviewerLine env={env} />}
            </LevelRow>
          )
        })}
      </div>
      <p className="text-xs text-muted-foreground">
        At every level your rules come first, and no level lets through the
        local network, this computer, or a secret sent to a host it isn't bound
        to.
      </p>
    </fieldset>
  )
}

/** Settings → Network's security level: what happens to a host no rule covers. */
export function LevelSection({ env }: { env: string }) {
  const { level, saving, setLevel } = useEnvironmentLevel(env)
  return (
    <section className="grid gap-2">
      <SectionHeader
        title="Security level"
        icon={ShieldCheckIcon}
        area="network"
      />
      <p className="text-xs text-muted-foreground">
        What happens when a sandbox connects to a host that no rule covers.
      </p>
      <SecurityLevelPicker
        env={env}
        value={level}
        onChange={(v) => {
          if (v) setLevel(v)
        }}
        disabled={saving}
      />
    </section>
  )
}

/** A teammate's override of the environment's level, or "Environment default", which follows it. */
export function TeammateLevelPicker({
  env,
  teammate,
}: {
  env: string
  teammate: Teammate
}) {
  const queryClient = useQueryClient()
  const environment = useEnvironmentLevel(env)
  const set = $api.useMutation(
    "put",
    "/api/environments/{env}/teammates/{id}/security-level",
    {
      onSuccess: () =>
        queryClient.invalidateQueries({
          queryKey: ["get", "/api/environments/{env}/teammates"],
        }),
      onError: (err) =>
        toast.error("Could not change the security level", {
          description: errorMessage(err),
        }),
    }
  )
  return (
    <SecurityLevelPicker
      env={env}
      value={teammate.securityLevel}
      disabled={set.isPending}
      environmentDefault={{ level: environment.level }}
      onChange={(level) =>
        set.mutate({
          params: { path: { env, id: teammate.id } },
          body: { level },
        })
      }
    />
  )
}
