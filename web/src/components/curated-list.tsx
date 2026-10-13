import { useState } from "react"
import { useQueryClient } from "@tanstack/react-query"
import { toast } from "sonner"
import { PackageIcon } from "@phosphor-icons/react"
import { Ident } from "@/components/ident"
import { SectionHeader } from "@/components/section-header"
import {
  Collapsible,
  CollapsibleContent,
  CollapsibleTrigger,
} from "@/components/ui/collapsible"
import { Input } from "@/components/ui/input"
import { Switch } from "@/components/ui/switch"
import { $api, errorMessage } from "@/lib/api/client"

/**
 * The curated list of development infrastructure that Studio ships, with a switch per entry for
 * this environment. The guarded, assisted and open levels allow an entry's hosts when no rule
 * matches; a switched-off entry is asked about like any other host.
 */
export function CuratedList({ env }: { env: string }) {
  const queryClient = useQueryClient()
  const [query, setQuery] = useState("")
  const curated = $api.useQuery("get", "/api/environments/{env}/curated", {
    params: { path: { env } },
  })
  const save = $api.useMutation("put", "/api/environments/{env}/curated", {
    onSuccess: () =>
      queryClient.invalidateQueries({
        queryKey: ["get", "/api/environments/{env}/curated"],
      }),
    onError: (err) =>
      toast.error("Could not change the curated list", {
        description: errorMessage(err),
      }),
  })
  // Empty lists come back as null from the API.
  const entries = (curated.data?.entries ?? []).map((e) => ({
    ...e,
    hosts: e.hosts ?? [],
    ports: e.ports ?? [],
  }))
  const q = query.trim().toLowerCase()
  const shown = q
    ? entries.filter((e) =>
        [e.name, e.group, ...e.hosts].some((s) => s.toLowerCase().includes(q))
      )
    : entries
  const groups = [...new Set(shown.map((e) => e.group))].map((group) => ({
    group,
    entries: shown.filter((e) => e.group === group),
  }))

  // The API takes the whole list of switched-off entries, so the toggle sends it with this one changed.
  const toggle = (id: string, allow: boolean) =>
    save.mutate({
      params: { path: { env } },
      body: {
        disabled: entries
          .filter((e) => (e.id === id ? !allow : !e.enabled))
          .map((e) => e.id),
      },
    })

  return (
    <section className="space-y-3 rounded-lg p-3 ring-1 ring-foreground/10">
      <SectionHeader
        title="Curated list"
        icon={PackageIcon}
        area="network"
        count={curated.data ? entries.length : undefined}
      />
      <p className="text-xs text-muted-foreground">
        Development infrastructure that Studio ships, version{" "}
        {curated.data?.version ?? "…"}, read-only. Guarded, assisted and open
        allow these hosts when no rule matches. Switch an entry off to be asked
        about its hosts instead.
      </p>
      <Input
        type="search"
        aria-label="Search the curated list"
        placeholder="Search the curated list"
        autoComplete="off"
        className="h-6 sm:w-56"
        value={query}
        onChange={(e) => setQuery(e.target.value)}
      />
      {curated.data && (
        <div className="space-y-3">
          {groups.map((g) => (
            <div key={g.group} className="space-y-1">
              <h3 className="text-xs font-medium text-muted-foreground">
                {g.group}
              </h3>
              <ul className="divide-y rounded-md ring-1 ring-foreground/10">
                {g.entries.map((e) => (
                  <li
                    key={e.id}
                    className="flex flex-wrap items-center justify-between gap-2 px-2.5 py-1.5 text-xs"
                  >
                    <div className="min-w-0">
                      <span className="font-medium">{e.name}</span>
                      <Collapsible>
                        <CollapsibleTrigger className="block max-w-full truncate text-left text-muted-foreground underline-offset-4 hover:underline">
                          {e.hosts.length}{" "}
                          {e.hosts.length === 1 ? "host" : "hosts"}
                          {e.ports.includes(80)
                            ? ", http and https"
                            : ", https"}
                        </CollapsibleTrigger>
                        <CollapsibleContent>
                          <ul className="mt-1 space-y-0.5">
                            {e.hosts.map((h) => (
                              <li key={h} className="min-w-0">
                                <Ident>{h}</Ident>
                              </li>
                            ))}
                          </ul>
                        </CollapsibleContent>
                      </Collapsible>
                    </div>
                    <label className="flex items-center gap-2">
                      <Switch
                        aria-label={`Allow ${e.name}`}
                        checked={e.enabled}
                        disabled={save.isPending}
                        onCheckedChange={(on) => toggle(e.id, on)}
                      />
                      {e.enabled ? "Allowed" : "Asked"}
                    </label>
                  </li>
                ))}
              </ul>
            </div>
          ))}
          {!groups.length && (
            <p className="text-xs text-muted-foreground">
              No entries match “{query.trim()}”.
            </p>
          )}
        </div>
      )}
    </section>
  )
}
