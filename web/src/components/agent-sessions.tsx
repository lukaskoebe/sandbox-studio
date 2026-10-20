import { useQueryClient } from "@tanstack/react-query"
import { toast } from "sonner"
import { StopIcon, TerminalWindowIcon } from "@phosphor-icons/react"
import { CountPill } from "@/components/count-pill"
import { Button } from "@/components/ui/button"
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuGroup,
  DropdownMenuItem,
  DropdownMenuLabel,
  DropdownMenuTrigger,
} from "@/components/ui/dropdown-menu"
import { $api, errorMessage, type Harness } from "@/lib/api/client"
import { harnessLabels } from "@/lib/teammates"

/**
 * Starts the teammate's harness TUI in a tmux session of its sandbox: the harness config is
 * written into the guest, and the session opens as a terminal tab.
 */
export function useStartAgentSession(
  env: string,
  id: string,
  onAttach: (name: string) => void
) {
  const queryClient = useQueryClient()
  const start = $api.useMutation(
    "post",
    "/api/environments/{env}/sandboxes/{id}/sessions",
    {
      onSuccess: (s) => onAttach(s.name),
      onSettled: () => {
        for (const p of [
          "/api/environments/{env}/sandboxes/{id}/sessions",
          "/api/environments/{env}/sandboxes/{id}/terminals",
        ])
          void queryClient.invalidateQueries({ queryKey: ["get", p] })
      },
      onError: (err) =>
        // A harness that exits right away brings its last output lines.
        toast.error("Could not start the session", {
          description: (
            <span className="break-words whitespace-pre-wrap">
              {errorMessage(err)}
            </span>
          ),
        }),
    }
  )
  return {
    start: () => start.mutate({ params: { path: { env, id } } }),
    starting: start.isPending,
  }
}

/** The agent sessions running in a sandbox: attaching to one selects its tab, and each can stop. */
export function AgentSessions({
  env,
  id,
  onAttach,
}: {
  env: string
  id: string
  onAttach: (name: string) => void
}) {
  const queryClient = useQueryClient()
  const path = { params: { path: { env, id } } }
  const sessions = $api.useQuery(
    "get",
    "/api/environments/{env}/sandboxes/{id}/sessions",
    path,
    { refetchInterval: 5000 }
  )
  const refresh = () => {
    for (const p of [
      "/api/environments/{env}/sandboxes/{id}/sessions",
      "/api/environments/{env}/sandboxes/{id}/terminals",
    ])
      queryClient.invalidateQueries({ queryKey: ["get", p] })
  }
  const stop = $api.useMutation(
    "delete",
    "/api/environments/{env}/sandboxes/{id}/sessions/{name}",
    {
      onSettled: refresh,
      onError: (err) =>
        toast.error("Could not stop the session", {
          description: errorMessage(err),
        }),
    }
  )
  const list = sessions.data ?? []

  return (
    <>
      {list.length > 0 && (
        <DropdownMenu>
          <DropdownMenuTrigger render={<Button variant="ghost" size="sm" />}>
            <TerminalWindowIcon />
            <span className="max-md:sr-only">Sessions</span>
            <CountPill count={list.length} />
          </DropdownMenuTrigger>
          <DropdownMenuContent align="end" className="w-64">
            <DropdownMenuGroup>
              <DropdownMenuLabel>Agent sessions</DropdownMenuLabel>
              {list.map((s) => (
                <div key={s.name} className="flex items-center gap-1">
                  <DropdownMenuItem
                    className="flex-1"
                    title="Open in a terminal tab"
                    onClick={() => onAttach(s.name)}
                  >
                    <span className="font-mono">{s.name}</span>
                    <span className="ml-auto text-muted-foreground">
                      {s.harness && s.harness in harnessLabels
                        ? harnessLabels[s.harness as Harness]
                        : s.harness}
                    </span>
                  </DropdownMenuItem>
                  <Button
                    variant="ghost"
                    size="icon-xs"
                    title={`Stop ${s.name} (ends the harness; its history is kept)`}
                    disabled={stop.isPending}
                    onClick={() =>
                      stop.mutate({
                        params: { path: { env, id, name: s.name } },
                      })
                    }
                  >
                    <StopIcon />
                  </Button>
                </div>
              ))}
            </DropdownMenuGroup>
          </DropdownMenuContent>
        </DropdownMenu>
      )}
    </>
  )
}
