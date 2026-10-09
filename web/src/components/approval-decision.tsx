import { useState } from "react"
import { useQueryClient } from "@tanstack/react-query"
import { toast } from "sonner"
import { CaretDownIcon } from "@phosphor-icons/react"
import { Button } from "@/components/ui/button"
import { Checkbox } from "@/components/ui/checkbox"
import { Spinner } from "@/components/ui/spinner"
import { ToggleGroup, ToggleGroupItem } from "@/components/ui/toggle-group"
import {
  Tooltip,
  TooltipContent,
  TooltipTrigger,
} from "@/components/ui/tooltip"
import {
  $api,
  errorMessage,
  errorStatus,
  type Approval,
} from "@/lib/api/client"
import { cn } from "@/lib/utils"

type Action = "allow" | "deny" | "dismiss"

/** The ports a request is decided for by default: HTTP and HTTPS together, else the requested port. */
function defaultPorts(port: number) {
  return port === 80 || port === 443 ? "ports 80 and 443" : `port ${port}`
}

/** Allow, deny or dismiss an approval; refreshes the lists the decision changes. */
function useDecideApproval() {
  const queryClient = useQueryClient()
  return $api.useMutation(
    "post",
    "/api/environments/{env}/approvals/{id}/decide",
    {
      onSettled: () => {
        queryClient.invalidateQueries({ queryKey: ["get", "/api/approvals"] })
        queryClient.invalidateQueries({
          queryKey: ["get", "/api/environments/{env}/approvals"],
        })
        queryClient.invalidateQueries({
          queryKey: ["get", "/api/environments/{env}/rules"],
        })
      },
      onError: (err, vars) => {
        // 409: the approval was already decided; the refetch shows that.
        if (errorStatus(err) === 409) return
        toast.error(`Could not ${vars.body.action} the request`, {
          description: errorMessage(err),
        })
      },
    }
  )
}

/**
 * Decides one network approval: the host pattern to allow or deny, any port or the default
 * ports, and whether the rule covers the whole environment or only the requesting sandbox.
 * Compact keeps the pattern and the buttons visible and folds the port and scope choices
 * behind an Options disclosure.
 */
export function ApprovalDecision({
  approval,
  compact = false,
}: {
  approval: Approval
  compact?: boolean
}) {
  const net = approval.network
  // Without patterns from the server, the requested host itself is the one choice.
  const patterns = net?.patterns?.length ? net.patterns : net ? [net.host] : []
  const [pattern, setPattern] = useState(patterns[0] ?? "")
  const [anyPort, setAnyPort] = useState(false)
  const [scope, setScope] = useState<"environment" | "sandbox">("environment")
  const [optionsOpen, setOptionsOpen] = useState(false)
  const decide = useDecideApproval()
  const busy = (action: Action) =>
    decide.isPending && decide.variables.body.action === action
  // A folded choice that differs from the default still shows on the Options button.
  const changed = anyPort || scope === "sandbox"

  const act = (action: Action) =>
    decide.mutate({
      params: { path: { env: approval.environmentId, id: approval.id } },
      body:
        action === "dismiss"
          ? { action, scope }
          : { action, scope, host: pattern, ports: anyPort ? [] : undefined },
    })

  return (
    <div className="grid gap-2">
      {patterns.length > 1 && (
        <ToggleGroup
          aria-label="Host pattern"
          variant="outline"
          value={[pattern]}
          onValueChange={(v) => {
            if (v[0]) setPattern(v[0])
          }}
          className="w-full"
        >
          {patterns.map((p) => (
            <ToggleGroupItem
              key={p}
              value={p}
              size="sm"
              className="min-w-0 flex-1 font-mono"
            >
              {p}
            </ToggleGroupItem>
          ))}
        </ToggleGroup>
      )}
      {(!compact || optionsOpen) && (
        <>
          <label className="flex items-center gap-2 text-xs">
            <Checkbox checked={anyPort} onCheckedChange={setAnyPort} />
            Any port
            {!anyPort && net && (
              <span className="ml-auto text-muted-foreground">
                Default: {defaultPorts(net.port)}
              </span>
            )}
          </label>
          <ToggleGroup
            aria-label="Scope"
            variant="outline"
            value={[scope]}
            onValueChange={(v) => {
              if (v[0]) setScope(v[0] === "sandbox" ? "sandbox" : "environment")
            }}
            className="w-full"
          >
            <ToggleGroupItem
              value="environment"
              size="sm"
              className="min-w-0 flex-1"
            >
              Environment
            </ToggleGroupItem>
            <ToggleGroupItem
              value="sandbox"
              size="sm"
              className="min-w-0 flex-1"
            >
              <span className="truncate">
                Only {net?.sandboxName ?? "this sandbox"}
              </span>
            </ToggleGroupItem>
          </ToggleGroup>
        </>
      )}
      <div className="flex items-center gap-1.5">
        <Tooltip>
          <TooltipTrigger
            render={
              <Button
                variant="ghost"
                size="xs"
                disabled={decide.isPending}
                onClick={() => act("dismiss")}
              />
            }
          >
            {busy("dismiss") && <Spinner className="size-3" />}
            Ignore
          </TooltipTrigger>
          <TooltipContent>
            Refuse this time without creating a rule
          </TooltipContent>
        </Tooltip>
        {compact && (
          <Button
            variant="ghost"
            size="xs"
            className="ml-auto"
            aria-expanded={optionsOpen}
            onClick={() => setOptionsOpen(!optionsOpen)}
          >
            Options
            {changed && (
              <span
                className="size-1.5 rounded-full bg-primary"
                title="Changed from the default"
              />
            )}
            <CaretDownIcon
              className={cn(
                "transition-transform",
                optionsOpen && "rotate-180"
              )}
            />
          </Button>
        )}
        <Button
          variant="outline"
          size="sm"
          className={cn(!compact && "ml-auto")}
          disabled={decide.isPending}
          onClick={() => act("deny")}
        >
          {busy("deny") && <Spinner />}
          Deny
        </Button>
        <Button
          size="sm"
          disabled={decide.isPending}
          onClick={() => act("allow")}
        >
          {busy("allow") && <Spinner />}
          Allow
        </Button>
      </div>
    </div>
  )
}
