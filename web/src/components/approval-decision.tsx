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
type Scope = "environment" | "teammate" | "sandbox"

/** The ports a request is decided for by default: HTTP and HTTPS together, else the requested port. */
function defaultPorts(port: number) {
  return port === 80 || port === 443 ? "ports 80 and 443" : `port ${port}`
}

/** The level of a host pattern the server suggests: the wildcard of a domain, or the host itself. */
function patternLevel(pattern: string) {
  return pattern.startsWith("*.") ? "wildcard" : "exact"
}

/** Allow, deny or dismiss an approval; refreshes the lists the decision changes. */
export function useDecideApproval() {
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
  const owner = approval.teammate
  // Without patterns from the server, the requested host itself is the one choice. The level
  // the owner chose last is preselected.
  const patterns = net?.patterns?.length ? net.patterns : net ? [net.host] : []
  const preferred =
    patterns.find((p) => patternLevel(p) === owner?.patternLevel) ?? patterns[0]
  const [pattern, setPattern] = useState(preferred)
  const [anyPort, setAnyPort] = useState(false)
  // A teammate's sandbox starts with the scope its teammate sets, the environment otherwise.
  const defaultScope: Scope = owner?.approvalScope ?? "environment"
  const scopes: Scope[] = owner
    ? ["environment", "teammate", "sandbox"]
    : ["environment", "sandbox"]
  const [scope, setScope] = useState<Scope>(defaultScope)
  const [optionsOpen, setOptionsOpen] = useState(false)
  const decide = useDecideApproval()
  const busy = (action: Action) =>
    decide.isPending && decide.variables.body.action === action
  // A folded choice that differs from the default still shows on the Options button.
  const changed = anyPort || scope !== defaultScope

  const act = (action: Action) =>
    decide.mutate({
      params: { path: { env: approval.environmentId, id: approval.id } },
      body:
        action === "dismiss"
          ? { action, scope }
          : { action, scope, host: pattern, ports: anyPort ? [] : undefined },
    })

  return (
    <div className="grid grid-cols-1 gap-2">
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
              title={p}
              className="min-w-0 flex-1 shrink font-mono"
            >
              <span className="truncate">{p}</span>
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
              const next = scopes.find((s) => s === v[0])
              if (next) setScope(next)
            }}
            className="w-full"
          >
            <ToggleGroupItem
              value="environment"
              size="sm"
              className="min-w-0 flex-1 shrink"
            >
              Environment
            </ToggleGroupItem>
            {owner && (
              <ToggleGroupItem
                value="teammate"
                size="sm"
                title={`${owner.name}'s sandboxes`}
                className="min-w-0 flex-1 shrink"
              >
                <span className="truncate">{owner.name}&apos;s sandboxes</span>
              </ToggleGroupItem>
            )}
            <ToggleGroupItem
              value="sandbox"
              size="sm"
              title={`Only ${net?.sandboxName ?? "this sandbox"}`}
              className="min-w-0 flex-1 shrink"
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
