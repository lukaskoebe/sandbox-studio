import { Link } from "@tanstack/react-router"
import { ArrowsLeftRightIcon } from "@phosphor-icons/react"
import {
  Empty,
  EmptyDescription,
  EmptyHeader,
  EmptyTitle,
} from "@/components/ui/empty"
import { ScrollArea } from "@/components/ui/scroll-area"
import { Spinner } from "@/components/ui/spinner"
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "@/components/ui/table"
import {
  Tooltip,
  TooltipContent,
  TooltipTrigger,
} from "@/components/ui/tooltip"
import { VerdictBadge } from "@/components/status-badge"
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
        Where {sandbox.name} connects, newest first. Rules and requests are on
        the{" "}
        <Link
          to="/e/$env/network"
          params={{ env }}
          className="underline underline-offset-4 hover:text-foreground"
        >
          Network page
        </Link>
        .
      </p>
      <ConnectionLog env={env} id={sandbox.id} />
    </>
  )
}

/**
 * The newest connections of a sandbox, for its dashboard. The full log is in the
 * Connections panel.
 */
export function RecentConnections({
  env,
  id,
  limit = 5,
}: {
  env: string
  id: string
  limit?: number
}) {
  const conns = $api.useQuery(
    "get",
    "/api/environments/{env}/sandboxes/{id}/connections",
    { params: { path: { env, id } } },
    { refetchInterval: 5000 }
  )
  if (conns.isPending) return <Spinner />
  if (conns.isError) {
    return (
      <p className="text-xs text-destructive">{errorMessage(conns.error)}</p>
    )
  }
  const list = conns.data ?? []
  if (list.length === 0) {
    return (
      <p className="text-xs text-muted-foreground">
        Connections from this sandbox show up here as it makes them.
      </p>
    )
  }
  return (
    <ul className="divide-y">
      {list.slice(0, limit).map((c) => (
        <li key={c.id} className="flex items-center gap-2 py-2 text-xs">
          <span className="min-w-0 flex-1 truncate font-mono">
            {hostPort(c.host, c.port)}
          </span>
          <VerdictBadge conn={c} />
          <time
            dateTime={c.started}
            className="shrink-0 text-muted-foreground tabular-nums"
          >
            {new Date(c.started).toLocaleTimeString()}
          </time>
        </li>
      ))}
    </ul>
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
      <Table>
        <TableHeader>
          <TableRow>
            <TableHead>Time</TableHead>
            <TableHead>Destination</TableHead>
            <TableHead>Verdict</TableHead>
            <TableHead className="hidden sm:table-cell">Source</TableHead>
            <TableHead className="text-right">Traffic</TableHead>
          </TableRow>
        </TableHeader>
        <TableBody>
          {list.map((c) => (
            <TableRow key={c.id}>
              <TableCell className="text-muted-foreground tabular-nums">
                {new Date(c.started).toLocaleTimeString()}
              </TableCell>
              <TableCell className="font-mono break-all">
                {hostPort(c.host, c.port)}
              </TableCell>
              <TableCell>
                <div className="flex items-center gap-1.5">
                  <VerdictBadge conn={c} />
                  {c.intercepted && <InterceptedMarker conn={c} />}
                </div>
              </TableCell>
              <TableCell className="hidden text-muted-foreground sm:table-cell">
                {c.source}
              </TableCell>
              <TableCell className="text-right text-muted-foreground tabular-nums">
                <span className="whitespace-nowrap">
                  ↑ {formatBytes(c.sent)}
                </span>{" "}
                <span className="whitespace-nowrap">
                  ↓ {formatBytes(c.received)}
                </span>
              </TableCell>
            </TableRow>
          ))}
        </TableBody>
      </Table>
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
