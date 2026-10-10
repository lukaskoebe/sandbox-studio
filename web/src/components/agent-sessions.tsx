import { useQueryClient } from "@tanstack/react-query"
import { toast } from "sonner"
import { RobotIcon, StopIcon } from "@phosphor-icons/react"
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
import { harnessLabels } from "@/lib/personas"

/**
 * Agent sessions of a persona's sandbox: its harness TUI in a tmux session. Starting one
 * writes the harness config into the guest and opens it as a terminal tab; attaching to a
 * running one selects its tab.
 */
export function AgentSessions({
  env,
  id,
  personaId,
  onAttach,
}: {
  env: string
  id: string
  personaId: string
  onAttach: (name: string) => void
}) {
  const queryClient = useQueryClient()
  const path = { params: { path: { env, id } } }
  const persona = $api.useQuery(
    "get",
    "/api/environments/{env}/personas/{id}",
    {
      params: { path: { env, id: personaId } },
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
        toast.error("Could not start the session", {
          description: errorMessage(err),
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
  const label = persona.data ? harnessLabels[persona.data.harness] : "agent"

  return (
    <>
      <Button
        variant="ghost"
        size="sm"
        disabled={start.isPending}
        title={`Start a ${label} session as ${persona.data?.name ?? "the owner"}`}
        onClick={() => start.mutate(path)}
      >
        {start.isPending ? <Spinner /> : <RobotIcon />}
        Start session
      </Button>
      {list.length > 0 && (
        <DropdownMenu>
          <DropdownMenuTrigger render={<Button variant="ghost" size="sm" />}>
            Sessions
            <span className="rounded-full bg-primary px-1.5 text-[0.625rem] text-primary-foreground">
              {list.length}
            </span>
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
