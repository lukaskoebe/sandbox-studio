import { useMemo } from "react"
import { useQueryClient } from "@tanstack/react-query"
import { CaretRightIcon } from "@phosphor-icons/react"
import { toast } from "sonner"
import { StatusDot } from "@/components/status-dot"
import { Button } from "@/components/ui/button"
import {
  Collapsible,
  CollapsibleContent,
  CollapsibleTrigger,
} from "@/components/ui/collapsible"
import {
  Empty,
  EmptyDescription,
  EmptyHeader,
  EmptyTitle,
} from "@/components/ui/empty"
import { Spinner } from "@/components/ui/spinner"
import { Switch } from "@/components/ui/switch"
import { $api, errorMessage } from "@/lib/api/client"
import type { components } from "@/lib/api/schema.gen"
import { cn, formatBytes } from "@/lib/utils"

type RecordingState = components["schemas"]["RecordingState"]
type Exchange = components["schemas"]["Exchange"]
type Header = components["schemas"]["Header"]
type Body = components["schemas"]["Body"]

const recordingPath = "/api/environments/{env}/sandboxes/{id}/recording"
const requestsPath = "/api/environments/{env}/sandboxes/{id}/recording/requests"

const time = (at: string) =>
  new Date(at).toLocaleTimeString([], { hour: "2-digit", minute: "2-digit" })

/** A sandbox's recording: whether it runs, while the panel is open. */
export function useRecording(env: string, id: string) {
  return $api.useQuery(
    "get",
    recordingPath,
    { params: { path: { env, id } } },
    // As often as the requests while it records, so their count keeps up.
    { refetchInterval: (q) => (q.state.data?.recording ? 2000 : 5000) }
  )
}

/**
 * Turns recording a sandbox's intercepted traffic on and off. A recording is short and kept
 * in memory only, as it holds what the sandbox's tools send (internal/gateway/record.go).
 */
export function RecordingControl({
  env,
  id,
  name,
  state,
}: {
  env: string
  id: string
  name: string
  state: RecordingState | undefined
}) {
  const queryClient = useQueryClient()
  const path = { env, id }
  const refresh = () => {
    void queryClient.invalidateQueries({ queryKey: ["get", recordingPath] })
    void queryClient.invalidateQueries({ queryKey: ["get", requestsPath] })
  }
  const onError = (what: string) => (err: unknown) =>
    toast.error(`Could not ${what}`, { description: errorMessage(err) })
  const set = $api.useMutation("put", recordingPath, { onSettled: refresh })
  const clear = $api.useMutation("delete", recordingPath, {
    onSettled: refresh,
    onError: onError("clear the recording"),
  })
  const recording = state?.recording ?? false
  const busy = set.isPending || clear.isPending

  return (
    <div className="mx-4 mb-3 grid gap-1.5 rounded-md border p-2.5 text-xs">
      <div className="flex min-w-0 flex-wrap items-center gap-2">
        <label className="flex items-center gap-2 font-medium">
          <Switch
            checked={recording}
            disabled={!state || busy}
            onCheckedChange={(on) =>
              set.mutate(
                { params: { path }, body: { recording: on } },
                {
                  onError: onError(
                    on ? "start recording" : "stop the recording"
                  ),
                }
              )
            }
          />
          Record intercepted traffic
        </label>
        {recording && state?.until && (
          <span className="ml-auto inline-flex items-center gap-1.5 text-caption">
            <StatusDot status="running" label="Recording" pulse />
            <span className="text-muted-foreground">
              until {time(state.until)}
            </span>
          </span>
        )}
      </div>
      <p className="text-caption text-muted-foreground">
        Keeps the HTTP requests Studio handles for {name} (a browser's pages,
        proxy and Caddy rules, hosts with secrets) with their responses, for 15
        minutes. They can hold tokens and what's typed into pages, so they stay
        in memory only, with secret values masked, and go an hour after
        recording stops. Connections Studio passes through untouched can't be
        read.
      </p>
      {state && state.count > 0 && (
        <div className="flex flex-wrap items-center gap-2 text-caption text-muted-foreground">
          <span>
            {state.count === 1 ? "1 request" : `${state.count} requests`} kept
            {state.expires && <> until {time(state.expires)}</>}.
          </span>
          <Button
            variant="outline"
            size="xs"
            disabled={busy}
            onClick={() => clear.mutate({ params: { path } })}
          >
            Clear
          </Button>
        </div>
      )}
    </div>
  )
}

/** What a recording kept, newest first; it refreshes while it records. */
export function RecordedRequests({
  env,
  id,
  name,
  recording,
}: {
  env: string
  id: string
  name: string
  recording: boolean
}) {
  const list = $api.useQuery(
    "get",
    requestsPath,
    { params: { path: { env, id } } },
    { refetchInterval: recording ? 2000 : false }
  )

  if (list.isPending) return <Spinner className="m-auto" />
  if (list.isError) {
    return (
      <p className="px-4 text-xs text-destructive">
        {errorMessage(list.error)}
      </p>
    )
  }
  const requests = list.data ?? []
  if (requests.length === 0) {
    return (
      <Empty className="mx-4 mb-4">
        <EmptyHeader>
          <EmptyTitle>Nothing recorded yet</EmptyTitle>
          <EmptyDescription>
            {recording
              ? `Requests show up here as ${name} makes them over connections Studio intercepts.`
              : "Turn on recording to keep the requests Studio handles."}
          </EmptyDescription>
        </EmptyHeader>
      </Empty>
    )
  }
  return (
    <ul className="px-4 pb-4" aria-label="Recorded requests">
      {requests.map((x) => (
        <RecordedRequest key={x.id} x={x} />
      ))}
    </ul>
  )
}

function statusClass(status: number) {
  if (status === 0) return "text-muted-foreground"
  if (status >= 400) return "text-destructive"
  return "text-foreground"
}

/** One recorded request: a line, and its headers and bodies once opened. */
function RecordedRequest({ x }: { x: Exchange }) {
  const url = new URL(x.url)
  return (
    <li className="border-b">
      <Collapsible>
        <CollapsibleTrigger className="group grid w-full gap-1 py-2 text-left text-xs hover:bg-muted/50">
          <span className="flex min-w-0 items-center gap-2">
            <CaretRightIcon className="size-3 shrink-0 text-muted-foreground transition-transform group-data-[panel-open]:rotate-90" />
            <span className="shrink-0 font-mono font-semibold">{x.method}</span>
            <span
              className={cn(
                "shrink-0 font-mono tabular-nums",
                statusClass(x.status)
              )}
            >
              {x.status || "…"}
            </span>
            <span className="min-w-0 flex-1 truncate font-mono">
              {url.pathname + url.search}
            </span>
            <time
              dateTime={x.started}
              className="shrink-0 font-mono text-caption text-muted-foreground tabular-nums"
            >
              {new Date(x.started).toLocaleTimeString()}
            </time>
          </span>
          <span className="flex min-w-0 items-center gap-2 pl-5 text-caption text-muted-foreground">
            <span className="min-w-0 truncate">{url.host}</span>
            {x.refused && <span className="shrink-0">refused by Studio</span>}
            {(x.secrets?.length ?? 0) > 0 && (
              <span className="shrink-0">
                {x.secrets?.length === 1
                  ? "1 secret"
                  : `${x.secrets?.length} secrets`}
              </span>
            )}
            <span className="ml-auto shrink-0 font-mono whitespace-nowrap tabular-nums">
              ↑ {formatBytes(x.requestBody.size)} ↓{" "}
              {formatBytes(x.responseBody.size)}
            </span>
          </span>
        </CollapsibleTrigger>
        <CollapsibleContent className="grid gap-3 pb-3 pl-5 text-xs">
          <p className="font-mono break-all text-muted-foreground">{x.url}</p>
          {x.refused && (
            <p className="text-destructive">Studio refused it: {x.refused}</p>
          )}
          {((x.secrets?.length ?? 0) > 0 || x.login) && (
            <p className="text-muted-foreground">
              Studio sent{" "}
              {[
                ...(x.secrets ?? []).map((s) => `the secret ${s}`),
                ...(x.login ? [`the browser login ${x.login}`] : []),
              ].join(", ")}{" "}
              in place of the placeholders shown here.
            </p>
          )}
          <Part
            title="Request"
            headers={x.requestHeaders ?? []}
            changed={x.changed ?? []}
            body={x.requestBody}
          />
          <Part
            title={x.status ? `Response · ${x.status}` : "Response"}
            headers={x.responseHeaders ?? []}
            body={x.responseBody}
            pending={!x.ended}
          />
        </CollapsibleContent>
      </Collapsible>
    </li>
  )
}

function Part({
  title,
  headers,
  changed = [],
  body,
  pending = false,
}: {
  title: string
  headers: Header[]
  changed?: string[]
  body: Body
  pending?: boolean
}) {
  const unseen = changed.filter((c) => !headers.some((h) => h.name === c))
  return (
    <section className="grid min-w-0 gap-1.5">
      <h4 className="font-medium">{title}</h4>
      {headers.length > 0 && (
        <dl className="grid min-w-0 grid-cols-[auto_1fr] gap-x-3 gap-y-0.5 font-mono text-caption">
          {headers.map((h, i) => (
            <div key={i} className="contents">
              <dt className="text-muted-foreground">{h.name}</dt>
              <dd className="min-w-0 break-all">
                {h.value}
                {changed.includes(h.name) && (
                  <span className="ml-1.5 font-sans text-muted-foreground">
                    (changed by Studio)
                  </span>
                )}
              </dd>
            </div>
          ))}
        </dl>
      )}
      {unseen.length > 0 && (
        <p className="text-caption text-muted-foreground">
          Studio added {unseen.join(", ")}.
        </p>
      )}
      <BodyView body={body} pending={pending} />
    </section>
  )
}

function BodyView({ body, pending }: { body: Body; pending: boolean }) {
  const pretty = useMemo(() => prettyJSON(body.text), [body.text])
  if (pending)
    return <p className="text-caption text-muted-foreground">Streaming…</p>
  if (body.size === 0)
    return <p className="text-caption text-muted-foreground">No body.</p>
  if (body.omitted)
    return (
      <p className="text-caption text-muted-foreground">
        {formatBytes(body.size)}, not shown: {body.omitted}.
      </p>
    )
  return (
    <div className="grid gap-1">
      <pre className="max-h-64 overflow-auto rounded-md bg-muted/60 p-2 font-mono text-caption break-all whitespace-pre-wrap">
        {pretty ?? body.text}
      </pre>
      {body.truncated && (
        <p className="text-caption text-muted-foreground">
          The first 64 KB of {formatBytes(body.size)}.
        </p>
      )}
    </div>
  )
}

function prettyJSON(text: string | undefined) {
  const t = text?.trim()
  if (!t || !/^[[{]/.test(t)) return undefined
  try {
    return JSON.stringify(JSON.parse(t), null, 2)
  } catch {
    return undefined
  }
}
