import { useQueryClient } from "@tanstack/react-query"
import { toast } from "sonner"
import { LightbulbIcon } from "@phosphor-icons/react"
import { Button } from "@/components/ui/button"
import { SectionHeader } from "@/components/section-header"
import { invalidateMemory } from "@/components/memory-dialogs"
import { $api, errorMessage } from "@/lib/api/client"

/**
 * What consolidation suggests for a scope: a teammate fact to propose for shared memory
 * (Promote) or a page to make a core page (Accept). Nothing is applied until the user says so;
 * a promotion still waits for approval in the inbox. Hidden when there is nothing open.
 */
export function SuggestionsPanel({
  env,
  scope,
}: {
  env: string
  scope: string
}) {
  const queryClient = useQueryClient()
  const list = $api.useQuery(
    "get",
    "/api/environments/{env}/memory/suggestions",
    {
      params: { path: { env }, query: { scope } },
    }
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

  const items = list.data ?? []
  if (items.length === 0) return null
  const busy = accept.isPending || dismiss.isPending
  return (
    <div className="space-y-2">
      <SectionHeader
        title="Suggestions"
        icon={LightbulbIcon}
        area="memory"
        count={items.length}
      />
      <ul className="divide-y rounded-lg ring-1 ring-foreground/10">
        {items.map((sg) => (
          <li
            key={sg.id}
            className="flex flex-wrap items-center gap-x-3 gap-y-2 px-3 py-2 text-xs"
          >
            <span className="min-w-0 flex-1">
              <span className="block whitespace-pre-wrap">{sg.fact.text}</span>
              <span className="block text-muted-foreground">{sg.reason}</span>
            </span>
            <Button
              size="sm"
              variant="outline"
              disabled={busy}
              onClick={() =>
                accept.mutate({ params: { path: { env, id: sg.id } } })
              }
            >
              {sg.kind === "promote" ? "Promote" : "Accept"}
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
          </li>
        ))}
      </ul>
    </div>
  )
}
