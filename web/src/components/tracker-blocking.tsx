import { useQueryClient } from "@tanstack/react-query"
import { toast } from "sonner"
import { EyeSlashIcon } from "@phosphor-icons/react"
import { Ident } from "@/components/ident"
import { SectionHeader } from "@/components/section-header"
import {
  Collapsible,
  CollapsibleContent,
  CollapsibleTrigger,
} from "@/components/ui/collapsible"
import { Switch } from "@/components/ui/switch"
import { $api, errorMessage } from "@/lib/api/client"

/**
 * The built-in tracker list, read-only, and the environment's switch for it: a browser's requests
 * to the listed domains are denied without asking, unless a rule allows the host.
 */
export function TrackerBlocking({ env }: { env: string }) {
  const queryClient = useQueryClient()
  const trackers = $api.useQuery("get", "/api/environments/{env}/trackers", {
    params: { path: { env } },
  })
  const setBlock = $api.useMutation("put", "/api/environments/{env}/trackers", {
    onSuccess: () =>
      queryClient.invalidateQueries({
        queryKey: ["get", "/api/environments/{env}/trackers"],
      }),
    onError: (err) =>
      toast.error("Could not change tracker blocking", {
        description: errorMessage(err),
      }),
  })
  const data = trackers.data
  const domains = data?.domains ?? []

  return (
    <section className="space-y-2 rounded-lg p-3 ring-1 ring-foreground/10">
      <SectionHeader
        title="Trackers"
        icon={EyeSlashIcon}
        area="network"
        count={data ? domains.length : undefined}
      />
      <div className="flex flex-wrap items-center justify-between gap-2 text-xs">
        <label className="flex items-center gap-2">
          <Switch
            checked={data?.block ?? false}
            disabled={!data || setBlock.isPending}
            onCheckedChange={(block) =>
              setBlock.mutate({
                params: { path: { env } },
                body: { block },
              })
            }
          />
          Block trackers in the browser
        </label>
        {data && (
          <span className="text-muted-foreground">
            Built-in list {data.version}, read-only
          </span>
        )}
      </div>
      {data && (
        <Collapsible>
          <CollapsibleTrigger className="text-xs text-muted-foreground underline-offset-4 hover:text-foreground hover:underline">
            Show the {domains.length} listed domains
          </CollapsibleTrigger>
          <CollapsibleContent>
            <ul className="mt-2 grid grid-cols-2 gap-x-4 gap-y-0.5 font-mono text-[0.6875rem] sm:grid-cols-3">
              {domains.map((d) => (
                <li key={d} className="min-w-0">
                  <Ident>{d}</Ident>
                </li>
              ))}
            </ul>
          </CollapsibleContent>
        </Collapsible>
      )}
    </section>
  )
}
