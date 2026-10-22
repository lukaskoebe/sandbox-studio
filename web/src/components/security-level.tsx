import { useId, type ReactNode } from "react"
import { useQueryClient } from "@tanstack/react-query"
import { Link } from "@tanstack/react-router"
import { toast } from "sonner"
import { ShieldCheckIcon } from "@phosphor-icons/react"
import { SectionHeader } from "@/components/section-header"
import { Badge } from "@/components/ui/badge"
import { $api, errorMessage, type Teammate } from "@/lib/api/client"

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
function LevelExamples({ env, level }: { env: string; level: SecurityLevel }) {
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
  const examples = {
    locked: [
      "Only hosts your rules allow.",
      "Every other host: even npm install asks.",
    ],
    guarded: [
      <>
        npm, PyPI, Go, crates, Docker Hub, GitHub reads and the rest of the{" "}
        {curated}.
      </>,
      "Every other host.",
    ],
    assisted: [
      <>The {curated}, and the hosts an AI reviewer allows.</>,
      "The hosts the reviewer is unsure about.",
    ],
    open: [
      "Any public host, logged in Activity.",
      "No public host. Your deny rules and the tracker list still apply.",
    ],
  }[level]
  return (
    <dl className="grid gap-1">
      <div>
        <dt className="text-muted-foreground">Connects without asking</dt>
        <dd>{examples[0]}</dd>
      </div>
      <div>
        <dt className="text-muted-foreground">Asks you</dt>
        <dd>{examples[1]}</dd>
      </div>
    </dl>
  )
}

/** Who reviews at Assisted: the environment's utility model, or nobody, so it asks like Guarded. */
function ReviewerLine({ env }: { env: string }) {
  const reviewer = $api.useQuery("get", "/api/environments/{env}/reviewer", {
    params: { path: { env } },
  })
  if (!reviewer.data) return null
  return (
    <p className="text-muted-foreground">
      {reviewer.data.available
        ? `Reviewer: ${reviewer.data.model} on ${reviewer.data.provider}. Its decisions show in Activity, where you can revert them.`
        : "No provider has a utility model yet, so it asks like Guarded."}
    </p>
  )
}

function LevelCard({
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
    <label className="grid cursor-pointer content-start gap-2 rounded-lg p-3 text-xs/relaxed ring-1 ring-foreground/10 transition-colors hover:bg-muted/40 has-checked:bg-primary/5 has-checked:ring-2 has-checked:ring-primary has-focus-visible:outline-2 has-focus-visible:outline-ring has-disabled:cursor-default">
      <span className="flex items-center gap-2">
        <input
          type="radio"
          name={name}
          value={value}
          checked={checked}
          onChange={onSelect}
          aria-labelledby={id}
          aria-describedby={`${id}-text`}
          className="size-3.5 accent-primary outline-none"
        />
        <span id={id} className="text-sm font-medium">
          {title}
        </span>
        {badge && <Badge variant="secondary">{badge}</Badge>}
      </span>
      <div id={`${id}-text`} className="grid gap-2 pl-5.5">
        {children}
      </div>
    </label>
  )
}

/**
 * The four levels as radio cards, two by two where there is room, each saying what connects
 * without asking and what asks. A teammate's picker adds the environment's default before them.
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
      {environmentDefault && (
        <LevelCard
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
          <p className="text-muted-foreground">
            Follows the environment's level, set in Settings → Network.
          </p>
        </LevelCard>
      )}
      <div className="grid gap-2 @lg:grid-cols-2">
        {securityLevels.map((l) => (
          <LevelCard
            key={l.value}
            name={name}
            value={l.value}
            checked={value === l.value}
            onSelect={() => onChange(l.value)}
            title={l.label}
            badge={l.value === "guarded" ? "Recommended" : undefined}
          >
            <LevelExamples env={env} level={l.value} />
            {l.value === "assisted" && <ReviewerLine env={env} />}
          </LevelCard>
        ))}
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
    <section className="space-y-2 rounded-lg p-3 ring-1 ring-foreground/10">
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
