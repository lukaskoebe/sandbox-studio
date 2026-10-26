import { useState } from "react"
import { useQueryClient } from "@tanstack/react-query"
import { toast } from "sonner"
import { PackageIcon } from "@phosphor-icons/react"
import { Ident } from "@/components/ident"
import { SectionHeader } from "@/components/section-header"
import { useEnvironmentLevel } from "@/components/security-level"
import { Input } from "@/components/ui/input"
import {
  Popover,
  PopoverContent,
  PopoverTitle,
  PopoverTrigger,
} from "@/components/ui/popover"
import { Switch } from "@/components/ui/switch"
import { $api, errorMessage } from "@/lib/api/client"
import { cn } from "@/lib/utils"

/**
 * The curated list of development infrastructure that Studio ships, with a switch per entry for
 * this environment, in columns by group; an entry's hosts open from its count. The guarded, assisted and open levels allow an entry's hosts when no rule
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
  const off = entries.filter((e) => !e.enabled).length
  const { level } = useEnvironmentLevel(env)
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
    <section id="curated-list" className="grid scroll-mt-4 gap-2">
      <SectionHeader
        title="Curated list"
        icon={PackageIcon}
        area="network"
        count={
          curated.data
            ? off
              ? `${entries.length - off} of ${entries.length}`
              : entries.length
            : undefined
        }
        actions={
          <Input
            type="search"
            aria-label="Search the curated list"
            placeholder="Search the curated list"
            autoComplete="off"
            className="h-6 w-44 sm:w-56"
            value={query}
            onChange={(e) => setQuery(e.target.value)}
          />
        }
      />
      <p className="text-xs/relaxed text-muted-foreground">
        Development infrastructure that Studio ships, version{" "}
        {curated.data?.version ?? "…"}, read-only. Guarded, assisted and open
        allow these hosts when no rule matches; switch an entry off to be asked
        about its hosts instead.
        {level === "locked" &&
          " The environment is Locked, so they ask too unless a rule allows them."}
      </p>
      {curated.data && (
        <div className="@container divide-y overflow-hidden rounded-(--radius) border bg-card text-xs">
          {groups.map((g) => (
            <div
              key={g.group}
              className="grid gap-x-4 gap-y-1 px-3 py-2 @2xl:grid-cols-[9rem_minmax(0,1fr)]"
            >
              <h3 className="pt-1 section-label text-muted-foreground">
                {g.group}
              </h3>
              <ul className="grid gap-x-6 @lg:grid-cols-2 @4xl:grid-cols-3">
                {g.entries.map((e) => (
                  <li
                    key={e.id}
                    className="flex h-7 min-w-0 items-center gap-2"
                  >
                    <Switch
                      aria-label={`Allow ${e.name}`}
                      checked={e.enabled}
                      disabled={save.isPending}
                      onCheckedChange={(on) => toggle(e.id, on)}
                    />
                    <span
                      className={cn(
                        "min-w-0 truncate",
                        !e.enabled && "text-muted-foreground"
                      )}
                      title={e.name}
                    >
                      {e.name}
                    </span>
                    <Popover>
                      <PopoverTrigger className="shrink-0 rounded-sm text-muted-foreground underline-offset-4 outline-none hover:text-foreground hover:underline focus-visible:ring-2 focus-visible:ring-ring/30">
                        {e.hosts.length}{" "}
                        {e.hosts.length === 1 ? "host" : "hosts"}
                      </PopoverTrigger>
                      <PopoverContent className="grid w-auto max-w-80 gap-2">
                        <PopoverTitle>
                          {e.name}
                          <span className="font-normal text-muted-foreground">
                            ,{" "}
                            {e.ports.includes(80) ? "http and https" : "https"}
                          </span>
                        </PopoverTitle>
                        <ul className="grid gap-0.5">
                          {e.hosts.map((h) => (
                            <li key={h} className="min-w-0">
                              <Ident>{h}</Ident>
                            </li>
                          ))}
                        </ul>
                      </PopoverContent>
                    </Popover>
                  </li>
                ))}
              </ul>
            </div>
          ))}
          {!groups.length && (
            <p className="px-3 py-2.5 text-muted-foreground">
              No entries match “{query.trim()}”.
            </p>
          )}
        </div>
      )}
    </section>
  )
}
