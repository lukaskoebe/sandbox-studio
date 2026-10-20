import { Link } from "@tanstack/react-router"
import { ArrowsLeftRightIcon } from "@phosphor-icons/react"
import { Ident } from "@/components/ident"
import { StatusDot, type Status } from "@/components/status-dot"
import {
  Empty,
  EmptyDescription,
  EmptyHeader,
  EmptyTitle,
} from "@/components/ui/empty"
import { ScrollArea } from "@/components/ui/scroll-area"
import { Spinner } from "@/components/ui/spinner"
import {
  Tooltip,
  TooltipContent,
  TooltipTrigger,
} from "@/components/ui/tooltip"
import {
  $api,
  errorMessage,
  type Connection,
  type Sandbox,
} from "@/lib/api/client"
import { cn, formatBytes, hostPort } from "@/lib/utils"

/** The body of a sandbox's Connections panel: where it connects, newest first. */
export function ConnectionsPanel({
  env,
  sandbox,
}: {
  env: string
  sandbox: Sandbox
}) {
  return (
    <>
      <p className="px-4 pt-3 pb-2 text-xs text-muted-foreground">
        Where {sandbox.name} connects, newest first. The rules are in{" "}
        <Link
          to="/e/$env/settings/network"
          params={{ env }}
          className="underline underline-offset-4 hover:text-foreground"
        >
          Settings → Network
        </Link>
        .
      </p>
      <ConnectionLog env={env} id={sandbox.id} />
    </>
  )
}

/** A verdict as one of the four statuses: held for you is waiting, allowed is running. */
const verdictStatus: Record<string, Status> = {
  open: "stopped",
  allowed: "running",
  denied: "failed",
  failed: "failed",
  undecided: "waiting",
  pending: "waiting",
}

/** A connection's dot and its verdict as its word. A failed one explains itself in a tooltip. */
function VerdictDot({ conn }: { conn: Connection }) {
  const dot = (
    <StatusDot
      status={verdictStatus[conn.verdict] ?? "stopped"}
      label={conn.verdict}
      className="shrink-0"
    />
  )
  if (conn.verdict !== "failed" || !conn.error) return dot
  return (
    <Tooltip>
      <TooltipTrigger render={<span className="inline-flex shrink-0" />}>
        {dot}
      </TooltipTrigger>
      <TooltipContent>{conn.error}</TooltipContent>
    </Tooltip>
  )
}

/** Mounted only while the panel is open, so the log refreshes only then. */
function ConnectionLog({ env, id }: { env: string; id: string }) {
  const conns = $api.useQuery(
    "get",
    "/api/environments/{env}/sandboxes/{id}/connections",
    { params: { path: { env, id } } },
    { refetchInterval: 2000 }
  )

  if (conns.isPending) return <Spinner className="m-auto" />
  if (conns.isError) {
    return (
      <p className="px-4 text-xs text-destructive">
        {errorMessage(conns.error)}
      </p>
    )
  }
  const list = conns.data ?? []
  if (list.length === 0) {
    return (
      <Empty className="mx-4 mb-4">
        <EmptyHeader>
          <EmptyTitle>No connections yet</EmptyTitle>
          <EmptyDescription>
            Connections from this sandbox show up here as it makes them.
          </EmptyDescription>
        </EmptyHeader>
      </Empty>
    )
  }

  return (
    <ScrollArea className="min-h-0 flex-1" viewportClassName="px-4 pb-4">
      <ul>
        {list.map((c) => (
          <li key={c.id} className="grid gap-1 border-b py-2 text-xs">
            <div className="flex min-w-0 items-center gap-2">
              <VerdictDot conn={c} />
              <Ident className="min-w-0 flex-1">
                {hostPort(c.host, c.port)}
              </Ident>
              <time
                dateTime={c.started}
                className="shrink-0 font-mono text-[0.6875rem] text-muted-foreground tabular-nums"
              >
                {new Date(c.started).toLocaleTimeString()}
              </time>
            </div>
            <div className="flex min-w-0 items-center gap-2 pl-4 text-[0.6875rem] text-muted-foreground">
              <span className="min-w-0 truncate">{c.source}</span>
              {c.intercepted && <InterceptedMarker conn={c} />}
              <span className="ml-auto shrink-0 font-mono whitespace-nowrap tabular-nums">
                ↑ {formatBytes(c.sent)} ↓ {formatBytes(c.received)}
              </span>
            </div>
          </li>
        ))}
      </ul>
    </ScrollArea>
  )
}

/**
 * Marks a connection Studio handled itself. A request Studio refused explains
 * itself in the tooltip, unless the verdict badge already shows it.
 */
function InterceptedMarker({ conn }: { conn: Connection }) {
  const reason = conn.verdict === "failed" ? undefined : conn.error
  return (
    <Tooltip>
      <TooltipTrigger
        render={
          <span
            className={cn(
              "inline-flex",
              reason
                ? "text-amber-600 dark:text-amber-400"
                : "text-muted-foreground"
            )}
          />
        }
      >
        <ArrowsLeftRightIcon className="size-3.5" />
      </TooltipTrigger>
      <TooltipContent>
        <span className="grid gap-1">
          <span>
            Studio handled the HTTP requests (a proxy or Caddy rule, or a host
            with secrets)
          </span>
          {reason && <span>{reason}</span>}
        </span>
      </TooltipContent>
    </Tooltip>
  )
}
