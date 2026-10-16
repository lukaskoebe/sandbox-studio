import { useQueryClient } from "@tanstack/react-query"
import { toast } from "sonner"
import { RobotIcon, StopIcon, TerminalWindowIcon } from "@phosphor-icons/react"
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
import { Spinner } from "@/components/ui/spinner"
import { $api, errorMessage, type Harness } from "@/lib/api/client"
import { harnessLabels } from "@/lib/teammates"

/**
 * Agent sessions of a teammate's sandbox: its harness TUI in a tmux session. Starting one
 * writes the harness config into the guest and opens it as a terminal tab; attaching to a
 * running one selects its tab.
 */
export function AgentSessions({
  env,
  id,
  teammateId,
  onAttach,
}: {
  env: string
  id: string
  teammateId: string
  onAttach: (name: string) => void
}) {
  const queryClient = useQueryClient()
  const path = { params: { path: { env, id } } }
  const teammate = $api.useQuery(
    "get",
    "/api/environments/{env}/teammates/{id}",
    {
      params: { path: { env, id: teammateId } },
    }
  )
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
  const start = $api.useMutation(
    "post",
    "/api/environments/{env}/sandboxes/{id}/sessions",
    {
      onSuccess: (s) => onAttach(s.name),
      onSettled: refresh,
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
  const label = teammate.data ? harnessLabels[teammate.data.harness] : "agent"

  return (
    <>
      <Button
        variant="ghost"
        size="sm"
        disabled={start.isPending}
        title={`Start a ${label} session as ${teammate.data?.name ?? "the owner"}`}
        onClick={() => start.mutate(path)}
      >
        {start.isPending ? <Spinner /> : <RobotIcon />}
        <span className="max-md:sr-only">Start session</span>
      </Button>
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
