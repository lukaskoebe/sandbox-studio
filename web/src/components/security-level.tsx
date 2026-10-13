import { useQueryClient } from "@tanstack/react-query"
import { toast } from "sonner"
import { ShieldCheckIcon } from "@phosphor-icons/react"
import { SectionHeader } from "@/components/section-header"
import { ToggleGroup, ToggleGroupItem } from "@/components/ui/toggle-group"
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select"
import { $api, errorMessage, type Teammate } from "@/lib/api/client"

/** The four security levels, from the most closed to the most open (docs/network.md). */
export const securityLevels = [
  {
    value: "locked",
    label: "Locked",
    text: "Ask for every host that no rule covers, as Studio did before levels.",
  },
  {
    value: "guarded",
    label: "Guarded",
    text: "Allow the curated list of development infrastructure and ask for every other host.",
  },
  {
    value: "assisted",
    label: "Assisted",
    text: "Allow the curated list, let an AI reviewer decide the other hosts, and ask when it is unsure.",
  },
  {
    value: "open",
    label: "Open",
    text: "Allow every public host and log it in Activity. Deny rules and the tracker list still apply.",
  },
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

/** The four levels as a segmented control. The sentence of the selected level is shown beneath it. */
export function SecurityLevelPicker({
  value,
  onChange,
  disabled,
}: {
  value?: string
  onChange: (value: SecurityLevel) => void
  disabled?: boolean
}) {
  const current = securityLevels.find((l) => l.value === value)
  return (
    <div className="space-y-2">
      <ToggleGroup
        aria-label="Security level"
        variant="outline"
        value={value ? [value] : []}
        disabled={disabled || !value}
        onValueChange={(v) => {
          const next = securityLevels.find((l) => l.value === v[0])
          if (next) onChange(next.value)
        }}
        className="w-full"
      >
        {securityLevels.map((l) => (
          <ToggleGroupItem key={l.value} value={l.value} className="flex-1">
            {l.label}
          </ToggleGroupItem>
        ))}
      </ToggleGroup>
      {current && (
        <p className="text-xs text-muted-foreground">{current.text}</p>
      )}
    </div>
  )
}

/**
 * The Rules page's security level: what happens to a host no rule covers, and whether Assisted can
 * ask a reviewer or, without a utility model, asks the user like Guarded.
 */
export function LevelSection({ env }: { env: string }) {
  const { level, saving, setLevel } = useEnvironmentLevel(env)
  const reviewer = $api.useQuery("get", "/api/environments/{env}/reviewer", {
    params: { path: { env } },
  })
  return (
    <section className="space-y-2 rounded-lg p-3 ring-1 ring-foreground/10">
      <SectionHeader
        title="Security level"
        icon={ShieldCheckIcon}
        area="network"
      />
      <p className="text-xs text-muted-foreground">
        Sets what happens when no rule matches. Explicit rules always win, and
        secrets only reach the hosts they are bound to at every level.
      </p>
      <SecurityLevelPicker
        value={level}
        onChange={setLevel}
        disabled={saving}
      />
      {level === "assisted" && reviewer.data && (
        <p className="text-xs text-muted-foreground">
          {reviewer.data.available
            ? `The reviewer is ${reviewer.data.model} on ${reviewer.data.provider}. Its decisions show in Activity, where you can revert them.`
            : "No provider of this environment has a utility model, so Assisted asks like Guarded until one does."}
        </p>
      )}
    </section>
  )
}

/** A teammate's override of the environment's level, or the environment's level ("Environment default"). */
export function TeammateLevelSelect({
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
  const defaultValue = "environment"
  const value = teammate.securityLevel || defaultValue
  const items = {
    [defaultValue]: environment.level
      ? `Environment default (${environment.level})`
      : "Environment default",
    ...Object.fromEntries(securityLevels.map((l) => [l.value, l.label])),
  }
  return (
    <Select
      value={value}
      disabled={set.isPending}
      items={items}
      onValueChange={(v) => {
        if (!v) return
        set.mutate({
          params: { path: { env, id: teammate.id } },
          body: { level: v === defaultValue ? "" : v },
        })
      }}
    >
      <SelectTrigger id="teammate-security-level" className="w-full">
        <SelectValue />
      </SelectTrigger>
      <SelectContent>
        <SelectItem value={defaultValue}>
          Environment default
          <span className="text-muted-foreground">
            {environment.level ?? ""}
          </span>
        </SelectItem>
        {securityLevels.map((l) => (
          <SelectItem key={l.value} value={l.value}>
            {l.label}
          </SelectItem>
        ))}
      </SelectContent>
    </Select>
  )
}
