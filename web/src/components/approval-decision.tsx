import { useState } from "react"
import { useQueryClient, type QueryClient } from "@tanstack/react-query"
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

/**
 * The buttons: an action; an allow whose rule expires after an hour; an allow or deny for the
 * one connection, without a rule; or, for a browser's request, an allow of its site or its host
 * pattern for the browser session, without a rule.
 */
type Choice =
  Action | "allowHour" | "allowOnce" | "denyOnce" | "allowSite" | "allowSession"

const hour = 60 * 60 * 1000

/** The host-pattern toggle's value for a rule that names a request's method and path. */
const REQUEST_LEVEL = "request"

/** The ports a request is decided for by default: HTTP and HTTPS together, else the requested port. */
function defaultPorts(port: number) {
  return port === 80 || port === 443 ? "ports 80 and 443" : `port ${port}`
}

/** Refetches what a decision changes: the approvals, and the rules a decision may create. */
export function refreshDecisions(queryClient: QueryClient) {
  queryClient.invalidateQueries({ queryKey: ["get", "/api/approvals"] })
  queryClient.invalidateQueries({
    queryKey: ["get", "/api/environments/{env}/approvals"],
  })
  queryClient.invalidateQueries({
    queryKey: ["get", "/api/environments/{env}/rules"],
  })
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
      onSettled: () => refreshDecisions(queryClient),
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

/** Where a browser request's rule applies: the browsers only, or the sandboxes too. */
export function WhereToggle({
  browserOnly,
  onChange,
}: {
  browserOnly: boolean
  onChange: (browserOnly: boolean) => void
}) {
  return (
    <ToggleGroup
      aria-label="Where"
      variant="outline"
      value={[browserOnly ? "browser" : "everywhere"]}
      onValueChange={(v) => {
        if (v[0]) onChange(v[0] === "browser")
      }}
      className="w-full"
    >
      <ToggleGroupItem
        value="browser"
        size="sm"
        title="The rule covers what the browsers load, not the sandboxes' own connections"
        className="min-w-0 flex-1 shrink"
      >
        <span className="truncate">Only in the browser</span>
      </ToggleGroupItem>
      <ToggleGroupItem
        value="everywhere"
        size="sm"
        title="The rule covers the sandboxes' own connections too"
        className="min-w-0 flex-1 shrink"
      >
        <span className="truncate">Browser and sandboxes</span>
      </ToggleGroupItem>
    </ToggleGroup>
  )
}

/** Who a rule covers: the whole environment, or only the sandbox that asked. */
export type DecisionScope = "environment" | "teammate" | "sandbox"

/** The scope choice of a network decision, as two toggles. */
export function ScopeToggle({
  value,
  onChange,
  sandboxName,
  owner,
}: {
  value: DecisionScope
  onChange: (scope: DecisionScope) => void
  sandboxName?: string
  /** The teammate that owns the sandbox, which adds its sandboxes as a scope. */
  owner?: string
}) {
  const scopes: DecisionScope[] = owner
    ? ["environment", "teammate", "sandbox"]
    : ["environment", "sandbox"]
  return (
    <ToggleGroup
      aria-label="Scope"
      variant="outline"
      value={[value]}
      onValueChange={(v) => {
        const next = scopes.find((s) => s === v[0])
        if (next) onChange(next)
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
          title={`${owner}'s sandboxes`}
          className="min-w-0 flex-1 shrink"
        >
          <span className="truncate">{owner}&apos;s sandboxes</span>
        </ToggleGroupItem>
      )}
      <ToggleGroupItem
        value="sandbox"
        size="sm"
        title={`Only ${sandboxName ?? "this sandbox"}`}
        className="min-w-0 flex-1 shrink"
      >
        <span className="truncate">Only {sandboxName ?? "this sandbox"}</span>
      </ToggleGroupItem>
    </ToggleGroup>
  )
}

/**
 * Decides one network approval: the host pattern to allow or deny, any port or the default
 * ports, and whether the rule covers the whole environment or only the requesting sandbox.
 * A browser's request makes a rule for the browsers only, unless the sandboxes are picked too.
 * Compact keeps the pattern and the buttons visible and folds the port, scope, where and
 * lifetime choices (an hour, the browser session, or this connection only) behind an Options
 * disclosure.
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
  // A browser request can also be allowed by its method and path, such as GET /repos/*.
  const requestPattern =
    net?.method && net.pathPattern
      ? `${net.method} ${net.pathPattern}`
      : undefined
  const [requestLevel, setRequestLevel] = useState(false)
  const [anyPort, setAnyPort] = useState(false)
  // A teammate's sandbox starts with the scope its teammate sets, the environment otherwise.
  const defaultScope: DecisionScope = owner?.approvalScope ?? "environment"
  const [scope, setScope] = useState<DecisionScope>(defaultScope)
  // Only a browser's requests are named by their method; their rule starts out browser-only.
  const browser = !!net?.method
  const [browserOnly, setBrowserOnly] = useState(browser)
  const [optionsOpen, setOptionsOpen] = useState(false)
  const decide = useDecideApproval()
  const [chosen, setChosen] = useState<Choice>()
  const busy = (choice: Choice) => decide.isPending && chosen === choice
  // A folded choice that differs from the default still shows on the Options button.
  const changed = anyPort || scope !== defaultScope || browserOnly !== browser

  const act = (choice: Choice) => {
    setChosen(choice)
    const params = {
      params: { path: { env: approval.environmentId, id: approval.id } },
    }
    const rule = {
      scope,
      host: pattern,
      ports: anyPort ? [] : undefined,
      ...(browserOnly ? { browserOnly } : {}),
      ...(requestLevel && net?.method && net.pathPattern
        ? { method: net.method, pathPrefix: net.pathPattern }
        : {}),
    }
    switch (choice) {
      case "dismiss":
        return decide.mutate({ ...params, body: { action: choice, scope } })
      case "allowSite":
        return decide.mutate({
          ...params,
          body: { action: "allow_site", scope },
        })
      // The session allow covers the host pattern in this teammate's browser until it stops.
      case "allowSession":
        return decide.mutate({
          ...params,
          body: { action: "allow_session", host: pattern, scope },
        })
      // A once decision answers this connection; the server ignores the rule's fields.
      case "allowOnce":
        return decide.mutate({
          ...params,
          body: { action: "allow_once", scope },
        })
      case "denyOnce":
        return decide.mutate({
          ...params,
          body: { action: "deny_once", scope },
        })
      case "allowHour":
        return decide.mutate({
          ...params,
          body: {
            ...rule,
            action: "allow",
            expiresAt: new Date(Date.now() + hour).toISOString(),
          },
        })
      default:
        return decide.mutate({ ...params, body: { ...rule, action: choice } })
    }
  }

  return (
    <div className="grid grid-cols-1 gap-2">
      {net?.method && (
        <div className="grid gap-0.5 text-xs">
          <p className="truncate font-mono" title={`${net.method} ${net.path}`}>
            {net.method} {net.path}
          </p>
          {net.initiator && (
            <p className="truncate text-muted-foreground" title={net.initiator}>
              From {net.initiator}
            </p>
          )}
        </div>
      )}
      {(patterns.length > 1 || requestPattern) && (
        <ToggleGroup
          aria-label="Host pattern"
          variant="outline"
          value={[requestLevel ? REQUEST_LEVEL : pattern]}
          onValueChange={(v) => {
            if (v[0] === REQUEST_LEVEL) setRequestLevel(true)
            else if (v[0]) {
              setPattern(v[0])
              setRequestLevel(false)
            }
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
          {requestPattern && (
            <ToggleGroupItem
              value={REQUEST_LEVEL}
              size="sm"
              title={`Only ${requestPattern} requests`}
              className="min-w-0 flex-1 shrink font-mono"
            >
              <span className="truncate">{requestPattern}</span>
            </ToggleGroupItem>
          )}
        </ToggleGroup>
      )}
      {net?.navigation && net.site && (
        <Button
          variant="outline"
          size="sm"
          className="w-full"
          disabled={decide.isPending}
          onClick={() => act("allowSite")}
        >
          {busy("allowSite") && <Spinner />}
          {`Allow site ${net.site} for this browser session`}
        </Button>
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
          <ScopeToggle
            value={scope}
            onChange={setScope}
            sandboxName={net?.sandboxName}
            owner={owner?.name}
          />
          {browser && (
            <WhereToggle browserOnly={browserOnly} onChange={setBrowserOnly} />
          )}
          <div className="flex flex-wrap justify-end gap-1.5">
            <Button
              variant="outline"
              size="sm"
              disabled={decide.isPending}
              onClick={() => act("allowHour")}
            >
              {busy("allowHour") && <Spinner />}
              Allow for 1 hour
            </Button>
            {browser && owner && (
              <Tooltip>
                <TooltipTrigger
                  render={
                    <Button
                      variant="outline"
                      size="sm"
                      disabled={decide.isPending}
                      onClick={() => act("allowSession")}
                    />
                  }
                >
                  {busy("allowSession") && <Spinner />}
                  Allow for this session
                </TooltipTrigger>
                <TooltipContent>
                  {`Allow ${pattern} in ${owner.name}'s browser until it stops, without a rule`}
                </TooltipContent>
              </Tooltip>
            )}
            <Button
              variant="outline"
              size="sm"
              disabled={decide.isPending}
              onClick={() => act("allowOnce")}
            >
              {busy("allowOnce") && <Spinner />}
              Allow once
            </Button>
            <Button
              variant="outline"
              size="sm"
              disabled={decide.isPending}
              onClick={() => act("denyOnce")}
            >
              {busy("denyOnce") && <Spinner />}
              Deny once
            </Button>
          </div>
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
