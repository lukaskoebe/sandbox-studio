import { useEffect, useMemo, useRef, useState, type FormEvent } from "react"
import { useQueryClient } from "@tanstack/react-query"
import { toast } from "sonner"
import {
  CaretLeftIcon,
  CaretRightIcon,
  HandIcon,
  PlayIcon,
  PowerIcon,
  StopIcon,
  TrashIcon,
} from "@phosphor-icons/react"
import { BrowserDownloads } from "@/components/browser-downloads"
import { BrowserLive, BrowserSessionSelect } from "@/components/browser-live"
import { Ident } from "@/components/ident"
import { StatusDot, type Status } from "@/components/status-dot"
import { Badge } from "@/components/ui/badge"
import { Button } from "@/components/ui/button"
import {
  Empty,
  EmptyDescription,
  EmptyHeader,
  EmptyTitle,
} from "@/components/ui/empty"
import { Input } from "@/components/ui/input"
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select"
import { Spinner } from "@/components/ui/spinner"
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "@/components/ui/table"
import { Tabs, TabsContent, TabsList, TabsTrigger } from "@/components/ui/tabs"
import { $api, errorMessage } from "@/lib/api/client"
import { normalizeBrowserAddress } from "@/lib/browser-address"
import type { components } from "@/lib/api/schema.gen"
import { cn, formatAge } from "@/lib/utils"

type BrowserAction = components["schemas"]["BrowserAction"]
type BrowserPattern = components["schemas"]["BrowserPattern"]
type BrowserStatus = components["schemas"]["BrowserStatus"]

const browserStatusPath = "/api/environments/{env}/teammates/{teammate}/browser"

// The VM states that are one of the four statuses; the others show their own word.
const vmStatus: Partial<Record<BrowserStatus["vm"], Status>> = {
  running: "running",
  suspended: "stopped",
  stopped: "stopped",
  crashed: "failed",
}

export function TeammateBrowser({
  env,
  teammate,
}: {
  env: string
  teammate: string
}) {
  const path = { env, teammate }
  const statusParams = { params: { path } }
  const queryClient = useQueryClient()
  const status = $api.useQuery("get", browserStatusPath, statusParams, {
    refetchInterval: 5000,
  })
  const st = status.data
  const running = st?.vm === "running"
  // The session the live view and controls act on: "" is the persistent profile, and a picked
  // ephemeral session that has closed falls back to it.
  const [picked, setPicked] = useState("")
  const sessions = st?.sessions ?? []
  const session = sessions.find((s) => s.id === picked)
  const sessionID = session?.id ?? ""
  const [streamURL, setStreamURL] = useState<string>()
  const [statusURLReady, setStatusURLReady] = useState(true)
  const stoppedStatusAt = useRef<number | undefined>(undefined)
  useEffect(() => {
    if (!running) {
      stoppedStatusAt.current = status.dataUpdatedAt
      setStreamURL(undefined)
      setStatusURLReady(false)
      return
    }
    if (
      stoppedStatusAt.current !== undefined &&
      status.dataUpdatedAt > stoppedStatusAt.current
    ) {
      stoppedStatusAt.current = undefined
      setStatusURLReady(true)
    }
  }, [running, status.dataUpdatedAt])
  // A new session starts without the previous one's page.
  useEffect(() => {
    setStreamURL(undefined)
  }, [sessionID])

  const sessionPageURL = session ? session.url : st?.url
  const pageURL = streamURL ?? (statusURLReady ? sessionPageURL : undefined)
  const addressPageURL = pageURL === "about:blank" ? "" : (pageURL ?? "")
  const [addressDraft, setAddressDraft] = useState("")
  const [addressDirty, setAddressDirty] = useState(false)
  const [addressError, setAddressError] = useState("")
  const requestedURL = useRef("")
  useEffect(() => {
    if (!addressDirty) setAddressDraft(addressPageURL)
  }, [addressDirty, addressPageURL])
  const refresh = () => {
    queryClient.invalidateQueries({
      queryKey: ["get", "/api/environments/{env}/teammates/{teammate}/browser"],
    })
    queryClient.invalidateQueries({
      queryKey: [
        "get",
        "/api/environments/{env}/teammates/{teammate}/browser/actions",
      ],
    })
  }
  const onError = (what: string) => (err: unknown) =>
    toast.error(`Could not ${what}`, { description: errorMessage(err) })
  const control = $api.useMutation(
    "post",
    "/api/environments/{env}/teammates/{teammate}/browser/{action}",
    { onSettled: refresh, onError: onError("change the browser") }
  )
  const takeover = $api.useMutation(
    "put",
    "/api/environments/{env}/teammates/{teammate}/browser/takeover",
    { onSettled: refresh, onError: onError("change who drives") }
  )
  const remove = $api.useMutation(
    "delete",
    "/api/environments/{env}/teammates/{teammate}/browser",
    { onSettled: refresh, onError: onError("delete the browser") }
  )
  const navigate = $api.useMutation(
    "post",
    "/api/environments/{env}/teammates/{teammate}/browser/navigate",
    {
      onSuccess: (browserStatus) => {
        queryClient.setQueryData<BrowserStatus>(
          ["get", browserStatusPath, statusParams],
          browserStatus
        )
        const shown = sessionID
          ? browserStatus.sessions?.find((s) => s.id === sessionID)?.url
          : browserStatus.url
        const actualURL = shown ?? requestedURL.current
        setStreamURL(actualURL)
        setAddressDraft(actualURL === "about:blank" ? "" : actualURL)
        setAddressDirty(false)
        setAddressError("")
      },
      onSettled: refresh,
      onError: (err) => setAddressError(errorMessage(err)),
    }
  )
  const driving = session ? session.takeover : !!st?.takeover
  const navigationBlocked =
    navigate.isPending ||
    takeover.isPending ||
    control.isPending ||
    remove.isPending
  const liveURL =
    `${window.location.origin}/api/environments/${env}/teammates/${teammate}/browser/live` +
    (sessionID ? `?session=${encodeURIComponent(sessionID)}` : "")
  const openAddress = (event: FormEvent) => {
    event.preventDefault()
    setAddressError("")
    if (!running || !driving || navigationBlocked) return

    const normalized = normalizeBrowserAddress(addressDraft)
    if (!normalized.value) {
      setAddressError(normalized.error ?? "Enter a valid website address.")
      return
    }
    requestedURL.current = normalized.value
    navigate.mutate({
      params: { path },
      body: { url: normalized.value, session: sessionID },
    })
  }
  const pickSession = (id: string) => {
    setPicked(id)
    setAddressDirty(false)
    setAddressError("")
  }

  return (
    <Tabs defaultValue="live">
      <div className="flex flex-wrap items-center gap-2">
        <TabsList>
          <TabsTrigger value="live">Live</TabsTrigger>
          <TabsTrigger value="downloads">Downloads</TabsTrigger>
          <TabsTrigger value="history">History</TabsTrigger>
          <TabsTrigger value="rules">Rules</TabsTrigger>
        </TabsList>
        <VMStatus vm={st?.vm} />
        {running && sessions.length > 0 && (
          <BrowserSessionSelect
            sessions={sessions}
            value={sessionID}
            onValueChange={pickSession}
          />
        )}
        {st?.driver && !driving && (
          <span className="text-xs text-muted-foreground">
            last driven by {st.driver}
          </span>
        )}
        <div className="ml-auto flex items-center gap-1.5">
          {running ? (
            <>
              <Button
                size="sm"
                variant={driving ? "default" : "outline"}
                aria-pressed={driving}
                disabled={navigationBlocked}
                onClick={() =>
                  takeover.mutate({
                    params: { path },
                    body: { on: !driving, session: sessionID },
                  })
                }
              >
                <HandIcon />
                {driving ? "Hand back to agents" : "Take over"}
              </Button>
              <Button
                size="sm"
                variant="ghost"
                disabled={navigationBlocked}
                onClick={() =>
                  control.mutate({
                    params: { path: { ...path, action: "stop" } },
                  })
                }
              >
                <StopIcon />
                Stop
              </Button>
            </>
          ) : (
            <Button
              size="sm"
              disabled={navigationBlocked || !st}
              onClick={() =>
                control.mutate({
                  params: { path: { ...path, action: "start" } },
                })
              }
            >
              {control.isPending ? <Spinner /> : <PowerIcon />}
              Start
            </Button>
          )}
          <Button
            size="sm"
            variant="ghost"
            title="Delete the browser VM with its profile: cookies and logins"
            disabled={navigationBlocked || st?.vm === "absent"}
            onClick={() => {
              if (
                confirm(
                  "Delete this browser with its cookies and logins? The history stays."
                )
              )
                remove.mutate({ params: { path } })
            }}
          >
            <TrashIcon />
          </Button>
        </div>
      </div>
      <TabsContent value="live" className="mt-3">
        {driving && (
          <p className="mb-2 text-sm">
            You are driving: agent browser calls wait, then are refused, until
            you hand back.
          </p>
        )}
        {running ? (
          <>
            <div className="mb-2 flex max-w-5xl flex-col gap-1.5">
              <form
                className="flex min-w-0 flex-col gap-2 sm:flex-row"
                onSubmit={openAddress}
              >
                <label className="sr-only" htmlFor="browser-address">
                  Browser address
                </label>
                <Input
                  id="browser-address"
                  aria-label="Browser address"
                  aria-invalid={!!addressError}
                  aria-describedby={
                    [
                      !driving && "browser-address-hint",
                      addressError && "browser-address-error",
                    ]
                      .filter(Boolean)
                      .join(" ") || undefined
                  }
                  autoComplete="url"
                  inputMode="url"
                  placeholder="https://example.com"
                  value={addressDraft}
                  readOnly={!driving || navigationBlocked}
                  className="h-8 min-w-0 flex-1 font-mono"
                  onChange={(event) => {
                    setAddressDraft(event.target.value)
                    setAddressDirty(true)
                    setAddressError("")
                  }}
                />
                <Button
                  type="submit"
                  size="sm"
                  disabled={!driving || navigationBlocked}
                >
                  {navigate.isPending && <Spinner />}
                  {navigate.isPending ? "Opening…" : "Open"}
                </Button>
              </form>
              {!driving && (
                <p
                  id="browser-address-hint"
                  className="text-xs text-muted-foreground"
                >
                  Take over to open a website.
                </p>
              )}
              {addressError && (
                <p
                  id="browser-address-error"
                  role="alert"
                  className="text-xs text-destructive"
                >
                  {addressError}
                </p>
              )}
            </div>
            <BrowserLive
              key={sessionID}
              url={liveURL}
              driving={driving}
              inputEnabled={!navigationBlocked}
              pageURL={pageURL}
              onPageURL={setStreamURL}
              className="max-w-5xl overflow-hidden rounded-md border"
            />
          </>
        ) : (
          <Empty className="max-w-5xl border">
            <EmptyHeader>
              <EmptyTitle>The browser is not running</EmptyTitle>
              <EmptyDescription>
                It starts when an agent uses a browser tool, or with Start, and
                stops after a while idle.
              </EmptyDescription>
            </EmptyHeader>
          </Empty>
        )}
      </TabsContent>
      <TabsContent value="downloads" className="mt-3">
        <BrowserDownloads env={env} teammate={teammate} />
      </TabsContent>
      <TabsContent value="history" className="mt-3">
        <History env={env} teammate={teammate} current={st?.sessionId} />
      </TabsContent>
      <TabsContent value="rules" className="mt-3">
        <Patterns env={env} teammate={teammate} />
      </TabsContent>
    </Tabs>
  )
}

/** The browser VM's state: a status dot where it is one of the four, else the state's word. */
function VMStatus({ vm }: { vm?: BrowserStatus["vm"] }) {
  const status = vm && vmStatus[vm]
  if (status) return <StatusDot status={status} />
  return <span className="text-xs text-muted-foreground">{vm ?? "…"}</span>
}

function shotURL(env: string, teammate: string, a: BrowserAction) {
  return `/api/environments/${env}/teammates/${teammate}/browser/actions/${a.id}/screenshot`
}

/** The action log, by session, with a step-through replay of the screenshots. */
function History({
  env,
  teammate,
  current,
}: {
  env: string
  teammate: string
  current?: string
}) {
  const actions = $api.useQuery(
    "get",
    "/api/environments/{env}/teammates/{teammate}/browser/actions",
    { params: { path: { env, teammate }, query: { limit: 500 } } },
    { refetchInterval: 10000 }
  )
  const sessions = useMemo(() => {
    const out = new Map<string, BrowserAction[]>()
    for (const a of actions.data ?? []) {
      const list = out.get(a.sessionId) ?? []
      list.unshift(a) // oldest first within a session
      out.set(a.sessionId, list)
    }
    return [...out.entries()]
  }, [actions.data])
  const [picked, setPicked] = useState<string>()
  const session: [string, BrowserAction[]] | undefined =
    sessions.find(([id]) => id === picked) ?? sessions.at(0)
  const steps = session?.[1] ?? []
  const [step, setStep] = useState(0)
  const [playing, setPlaying] = useState(false)
  const at = Math.min(step, Math.max(0, steps.length - 1))

  useEffect(() => {
    if (!playing) return
    const t = setInterval(() => {
      setStep((s) => {
        if (s + 1 >= steps.length) {
          setPlaying(false)
          return s
        }
        return s + 1
      })
    }, 1500)
    return () => clearInterval(t)
  }, [playing, steps.length])

  if (actions.isPending) return <Spinner />
  if (sessions.length === 0)
    return (
      <Empty className="border">
        <EmptyHeader>
          <EmptyTitle>No browser actions yet</EmptyTitle>
        </EmptyHeader>
      </Empty>
    )
  // The newest screenshot at or before the step: not every action has one.
  const shown = steps
    .slice(0, at + 1)
    .reverse()
    .find((a) => a.screenshot)

  return (
    <div className="grid gap-3 lg:grid-cols-[16rem_1fr]">
      <div className="flex flex-col gap-1">
        {sessions.map(([id, list]) => (
          <button
            key={id}
            type="button"
            onClick={() => {
              setPicked(id)
              setStep(0)
              setPlaying(false)
            }}
            className={cn(
              "rounded-md border px-2 py-1.5 text-left text-xs hover:bg-muted",
              id === session?.[0] && "bg-muted"
            )}
          >
            <span className="font-medium">
              {formatAge(list[0].at)}
              {id === current && " · current"}
            </span>
            <span className="block text-muted-foreground">
              {list.length} actions
            </span>
          </button>
        ))}
      </div>
      <div className="flex min-w-0 flex-col gap-2">
        <div className="flex items-center gap-1.5">
          <Button
            size="icon-sm"
            variant="outline"
            aria-label="Previous step"
            disabled={at === 0}
            onClick={() => setStep(at - 1)}
          >
            <CaretLeftIcon />
          </Button>
          <Button
            size="icon-sm"
            variant="outline"
            aria-label={playing ? "Pause" : "Replay"}
            onClick={() => {
              if (!playing && at + 1 >= steps.length) setStep(0)
              setPlaying(!playing)
            }}
          >
            {playing ? <StopIcon /> : <PlayIcon />}
          </Button>
          <Button
            size="icon-sm"
            variant="outline"
            aria-label="Next step"
            disabled={at + 1 >= steps.length}
            onClick={() => setStep(at + 1)}
          >
            <CaretRightIcon />
          </Button>
          <span className="text-xs text-muted-foreground">
            {at + 1} / {steps.length}
          </span>
        </div>
        {shown ? (
          <img
            src={shotURL(env, teammate, shown)}
            alt={`The page after ${shown.action}`}
            className="max-w-[min(100%,56rem)] rounded-md border"
          />
        ) : (
          <p className="text-sm text-muted-foreground">No screenshot yet.</p>
        )}
        <Table>
          <TableHeader>
            <TableRow>
              <TableHead>When</TableHead>
              <TableHead>Who</TableHead>
              <TableHead>Action</TableHead>
              <TableHead>Outcome</TableHead>
              <TableHead>Page</TableHead>
            </TableRow>
          </TableHeader>
          <TableBody>
            {steps.map((a, i) => (
              <TableRow
                key={a.id}
                onClick={() => setStep(i)}
                className={cn("cursor-pointer", i === at && "bg-muted")}
              >
                <TableCell className="text-xs whitespace-nowrap">
                  {new Date(a.at).toLocaleTimeString()}
                </TableCell>
                <TableCell className="text-xs">{a.actor}</TableCell>
                <TableCell className="max-w-72 truncate text-xs">
                  {a.ephemeral && (
                    <Badge variant="outline" className="mr-1.5">
                      Ephemeral
                    </Badge>
                  )}
                  <span className="font-medium">{a.action}</span> {a.target}
                  {a.detail && (
                    <span className="text-muted-foreground"> {a.detail}</span>
                  )}
                </TableCell>
                <TableCell>
                  <Badge
                    variant={
                      a.outcome === "denied" || a.outcome === "failed"
                        ? "destructive"
                        : "outline"
                    }
                  >
                    {a.outcome}
                  </Badge>
                </TableCell>
                <TableCell className="max-w-64 truncate text-xs text-muted-foreground">
                  {a.url && <Ident>{a.url}</Ident>}
                </TableCell>
              </TableRow>
            ))}
          </TableBody>
        </Table>
      </div>
    </div>
  )
}

const verdicts = { allow: "Allow", deny: "Deny", sensitive: "Sensitive field" }

/** The teammate's browser patterns: allow or deny actions per origin, sensitive fields. */
function Patterns({ env, teammate }: { env: string; teammate: string }) {
  const path = { env, teammate }
  const queryClient = useQueryClient()
  const patterns = $api.useQuery(
    "get",
    "/api/environments/{env}/teammates/{teammate}/browser/patterns",
    { params: { path } }
  )
  const refresh = () =>
    queryClient.invalidateQueries({
      queryKey: [
        "get",
        "/api/environments/{env}/teammates/{teammate}/browser/patterns",
      ],
    })
  const add = $api.useMutation(
    "post",
    "/api/environments/{env}/teammates/{teammate}/browser/patterns",
    {
      onSettled: refresh,
      onError: (err) =>
        toast.error("Could not add the rule", {
          description: errorMessage(err),
        }),
    }
  )
  const del = $api.useMutation(
    "delete",
    "/api/environments/{env}/teammates/{teammate}/browser/patterns/{id}",
    { onSettled: refresh }
  )
  const [form, setForm] = useState({
    verdict: "allow" as BrowserPattern["verdict"],
    action: "*",
    origin: "",
    role: "",
    label: "",
  })
  const submit = (e: FormEvent) => {
    e.preventDefault()
    add.mutate(
      { params: { path }, body: form },
      { onSuccess: () => setForm({ ...form, origin: "", label: "" }) }
    )
  }

  return (
    <div className="flex max-w-4xl flex-col gap-3">
      <p className="text-sm text-muted-foreground">
        Snapshots, scrolling and reading text are always allowed; script, cookie
        and storage access never. Other actions on an origin without a rule ask
        you first. Sensitive fields are redacted from snapshots like password
        fields.
      </p>
      <Table>
        <TableHeader>
          <TableRow>
            <TableHead>Rule</TableHead>
            <TableHead>Action</TableHead>
            <TableHead>Origin</TableHead>
            <TableHead>Element</TableHead>
            <TableHead />
          </TableRow>
        </TableHeader>
        <TableBody>
          {(patterns.data ?? []).map((p) => (
            <TableRow key={p.id}>
              <TableCell>{verdicts[p.verdict]}</TableCell>
              <TableCell>
                <Ident>{p.action}</Ident>
              </TableCell>
              <TableCell>
                <Ident>{p.origin}</Ident>
              </TableCell>
              <TableCell className="text-xs">
                {[p.role, p.label && `“${p.label}”`]
                  .filter(Boolean)
                  .join(" ") || "any"}
              </TableCell>
              <TableCell className="text-right">
                <Button
                  size="icon-sm"
                  variant="ghost"
                  aria-label="Delete rule"
                  onClick={() =>
                    del.mutate({ params: { path: { ...path, id: p.id } } })
                  }
                >
                  <TrashIcon />
                </Button>
              </TableCell>
            </TableRow>
          ))}
        </TableBody>
      </Table>
      <form onSubmit={submit} className="flex flex-wrap items-end gap-2">
        <Select
          value={form.verdict}
          onValueChange={(v) => v && setForm({ ...form, verdict: v })}
          items={verdicts}
        >
          <SelectTrigger size="sm" className="w-40">
            <SelectValue />
          </SelectTrigger>
          <SelectContent>
            {Object.entries(verdicts).map(([v, label]) => (
              <SelectItem key={v} value={v}>
                {label}
              </SelectItem>
            ))}
          </SelectContent>
        </Select>
        {form.verdict !== "sensitive" && (
          <Input
            aria-label="Action"
            className="h-8 w-24 font-mono"
            placeholder="click"
            value={form.action}
            onChange={(e) => setForm({ ...form, action: e.target.value })}
          />
        )}
        <Input
          aria-label="Origin"
          className="h-8 w-56 font-mono"
          placeholder="https://example.com"
          required
          value={form.origin}
          onChange={(e) => setForm({ ...form, origin: e.target.value })}
        />
        <Input
          aria-label="Role"
          className="h-8 w-28"
          placeholder="role"
          value={form.role}
          onChange={(e) => setForm({ ...form, role: e.target.value })}
        />
        <Input
          aria-label="Label"
          className="h-8 w-40"
          placeholder="label glob"
          value={form.label}
          onChange={(e) => setForm({ ...form, label: e.target.value })}
        />
        <Button size="sm" type="submit" disabled={add.isPending}>
          Add rule
        </Button>
      </form>
    </div>
  )
}
