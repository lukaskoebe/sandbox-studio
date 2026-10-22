import { useState, type ReactNode } from "react"
import { useQueryClient, type QueryClient } from "@tanstack/react-query"
import { toast } from "sonner"
import { CaretDownIcon } from "@phosphor-icons/react"
import { Button } from "@/components/ui/button"
import { Checkbox } from "@/components/ui/checkbox"
import { Input } from "@/components/ui/input"
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
import type { paths } from "@/lib/api/schema.gen"
import { cn } from "@/lib/utils"

type DecideBody =
  paths["/api/environments/{env}/approvals/{id}/decide"]["post"]["requestBody"]["content"]["application/json"]

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

/** Who a rule covers: the whole environment, or only the sandbox that asked. */
export type DecisionScope = "environment" | "teammate" | "sandbox"

/**
 * Who a browser request's rule covers: the teammate's browser (the sandbox's, without a
 * teammate), every browser of the environment, or the browsers and the sandboxes.
 */
export type BrowserReach = "browser" | "browsers" | "everywhere"

/**
 * How long a decision lasts: a rule, a rule for an hour, the browser session (browser requests
 * only, without a rule), or this request only.
 */
export type Lifetime = "always" | "hour" | "session" | "once"

/** Whether an approval is a browser's request, which names its method. */
export function isBrowserRequest(a: Approval) {
  return !!a.network?.method
}

/** Where a browser request's decision starts: the teammate's choice of scope, else its browser. */
export function defaultReach(a: Approval): BrowserReach {
  return a.teammate?.approvalScope === "environment" ? "browsers" : "browser"
}

/** The scope a rule gets from a request's choices. */
function scopeOf(
  a: Approval,
  scope: DecisionScope,
  reach: BrowserReach
): DecisionScope {
  if (!isBrowserRequest(a)) return scope
  if (reach !== "browser") return "environment"
  return a.teammate ? "teammate" : "sandbox"
}

/**
 * The decision for an approval: allow or deny, for the lifetime chosen. Always and an hour make
 * a rule for the host pattern and the scope (a browser request's reach). The browser session
 * allows a navigation's site, or another request's host pattern, in the teammate's browser
 * without a rule; denying for it denies once. Once answers the held connections of this request.
 * A rule keeps the note; without one the server writes what was asked, by whom and when.
 */
export function decisionBody(
  a: Approval,
  action: "allow" | "deny",
  choice: {
    lifetime: Lifetime
    scope: DecisionScope
    reach: BrowserReach
    host?: string
    ports?: number[]
    method?: string
    pathPrefix?: string
    note?: string
  }
): DecideBody {
  const scope = scopeOf(a, choice.scope, choice.reach)
  if (
    choice.lifetime === "once" ||
    (choice.lifetime === "session" && action === "deny")
  )
    return { action: `${action}_once`, scope }
  if (choice.lifetime === "session")
    return a.network?.navigation && a.network.site
      ? { action: "allow_site", scope }
      : { action: "allow_session", host: choice.host, scope }
  return {
    action,
    scope,
    host: choice.host,
    ports: choice.ports,
    method: choice.method,
    pathPrefix: choice.pathPrefix,
    ...(choice.note?.trim() ? { note: choice.note.trim() } : {}),
    ...(isBrowserRequest(a) && choice.reach !== "everywhere"
      ? { browserOnly: true }
      : {}),
    ...(choice.lifetime === "hour"
      ? { expiresAt: new Date(Date.now() + hour).toISOString() }
      : {}),
  }
}

/** The label of the allow or deny button for a lifetime, such as "Allow for 1 hour". */
export function decisionLabel(
  action: "allow" | "deny",
  lifetime: Lifetime,
  all = false
) {
  const verb = `${action === "allow" ? "Allow" : "Deny"}${all ? " all" : ""}`
  switch (lifetime) {
    case "hour":
      return `${verb} for 1 hour`
    case "session":
      return action === "allow" ? `${verb} for session` : `${verb} once`
    case "once":
      return `${verb} once`
  }
  return verb
}

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

/** A labeled row of the Options: what the toggles below it decide. */
function OptionRow({
  label,
  children,
}: {
  label: string
  children: ReactNode
}) {
  return (
    <div className="grid grid-cols-1 gap-1">
      <span className="text-xs text-muted-foreground">{label}</span>
      {children}
    </div>
  )
}

/**
 * Who a browser request's rule covers. The session and once answers make no rule, so the
 * choice is off for them.
 */
export function ReachToggle({
  value,
  onChange,
  owner,
  disabled,
}: {
  value: BrowserReach
  onChange: (reach: BrowserReach) => void
  owner?: string
  disabled?: boolean
}) {
  const items: { value: BrowserReach; label: string; title: string }[] = [
    {
      value: "browser",
      label: owner ? `${owner}'s browser` : "This browser",
      title: "The rule covers what this browser loads",
    },
    {
      value: "browsers",
      label: "All browsers",
      title: "The rule covers what every browser of the environment loads",
    },
    {
      value: "everywhere",
      label: "Browsers and sandboxes",
      title:
        "The rule covers the browsers and the sandboxes' own connections too",
    },
  ]
  return (
    <ToggleGroup
      aria-label="Who"
      variant="outline"
      value={[value]}
      disabled={disabled}
      onValueChange={(v) => {
        const next = items.find((i) => i.value === v[0])
        if (next) onChange(next.value)
      }}
      className="w-full"
    >
      {items.map((i) => (
        <ToggleGroupItem
          key={i.value}
          value={i.value}
          size="sm"
          title={i.title}
          className="min-w-0 flex-1 shrink"
        >
          <span className="truncate">{i.label}</span>
        </ToggleGroupItem>
      ))}
    </ToggleGroup>
  )
}

/** How long a decision lasts. The browser session is offered for browser requests only. */
export function LifetimeToggle({
  value,
  onChange,
  browser,
}: {
  value: Lifetime
  onChange: (lifetime: Lifetime) => void
  browser: boolean
}) {
  const items: { value: Lifetime; label: string; title: string }[] = [
    { value: "always", label: "Always", title: "A rule that stays" },
    { value: "hour", label: "1 hour", title: "A rule that expires in an hour" },
    ...(browser
      ? [
          {
            value: "session" as const,
            label: "This session",
            title:
              "Until the browser stops, without a rule; the sandboxes still ask",
          },
        ]
      : []),
    {
      value: "once",
      label: "Once",
      title: "Only this request, without a rule; the next one asks again",
    },
  ]
  return (
    <ToggleGroup
      aria-label="For"
      variant="outline"
      value={[value]}
      onValueChange={(v) => {
        const next = items.find((i) => i.value === v[0])
        if (next) onChange(next.value)
      }}
      className="w-full"
    >
      {items.map((i) => (
        <ToggleGroupItem
          key={i.value}
          value={i.value}
          size="sm"
          title={i.title}
          className="min-w-0 flex-1 shrink"
        >
          <span className="truncate">{i.label}</span>
        </ToggleGroupItem>
      ))}
    </ToggleGroup>
  )
}

/**
 * Decides one network approval: the host pattern to allow or deny, who the rule covers and for
 * how long. A sandbox's request picks the scope and the ports; a browser's request picks the
 * browser, every browser, or the browsers and the sandboxes, and can be allowed for the browser
 * session. Allow and Deny say the lifetime they decide for. Compact keeps the pattern and the
 * buttons visible and folds the other choices behind an Options disclosure.
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
  const browser = isBrowserRequest(approval)
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
  const [reach, setReach] = useState<BrowserReach>(defaultReach(approval))
  const [lifetime, setLifetime] = useState<Lifetime>("always")
  const [note, setNote] = useState("")
  const [optionsOpen, setOptionsOpen] = useState(false)
  const decide = useDecideApproval()
  const [chosen, setChosen] = useState<string>()
  const busy = (choice: string) => decide.isPending && chosen === choice
  // A folded choice that differs from the default still shows on the Options button.
  const changed =
    anyPort ||
    lifetime !== "always" ||
    note.trim() !== "" ||
    (browser ? reach !== defaultReach(approval) : scope !== defaultScope)

  const act = (choice: "allow" | "deny" | "dismiss") => {
    setChosen(choice)
    const params = {
      params: { path: { env: approval.environmentId, id: approval.id } },
    }
    if (choice === "dismiss")
      return decide.mutate({ ...params, body: { action: choice, scope } })
    decide.mutate({
      ...params,
      body: decisionBody(approval, choice, {
        lifetime,
        scope,
        reach,
        host: pattern,
        ports: anyPort ? [] : undefined,
        note,
        ...(requestLevel && net?.method && net.pathPattern
          ? { method: net.method, pathPrefix: net.pathPattern }
          : {}),
      }),
    })
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
      {(!compact || optionsOpen) && (
        <>
          {browser ? (
            <OptionRow label="Who">
              <ReachToggle
                value={reach}
                onChange={setReach}
                owner={owner?.name}
                disabled={lifetime === "session" || lifetime === "once"}
              />
            </OptionRow>
          ) : (
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
              <OptionRow label="Who">
                <ScopeToggle
                  value={scope}
                  onChange={setScope}
                  sandboxName={net?.sandboxName}
                  owner={owner?.name}
                />
              </OptionRow>
            </>
          )}
          <OptionRow label="For">
            <LifetimeToggle
              value={lifetime}
              onChange={setLifetime}
              browser={browser}
            />
          </OptionRow>
          {browser && lifetime === "session" && (
            <p className="text-xs text-muted-foreground">
              {net?.navigation && net.site
                ? `Allows the site ${net.site} in this browser until it stops.`
                : `Allows ${pattern} in this browser until it stops.`}
            </p>
          )}
          {(lifetime === "always" || lifetime === "hour") && (
            <Input
              aria-label="Why? (saved on the rule)"
              placeholder="Why? (saved on the rule)"
              maxLength={500}
              value={note}
              onChange={(e) => setNote(e.target.value)}
              className="h-7 text-xs"
            />
          )}
        </>
      )}
      <div className="flex flex-wrap items-center justify-end gap-1.5">
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
          {decisionLabel("deny", lifetime)}
        </Button>
        <Button
          size="sm"
          disabled={decide.isPending}
          onClick={() => act("allow")}
        >
          {busy("allow") && <Spinner />}
          {decisionLabel("allow", lifetime)}
        </Button>
      </div>
    </div>
  )
}
