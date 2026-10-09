import { useState } from "react"
import { Link } from "@tanstack/react-router"
import { ArrowsLeftRightIcon, PlugsConnectedIcon } from "@phosphor-icons/react"
import { Button } from "@/components/ui/button"
import {
  Empty,
  EmptyDescription,
  EmptyHeader,
  EmptyTitle,
} from "@/components/ui/empty"
import {
  Sheet,
  SheetContent,
  SheetDescription,
  SheetHeader,
  SheetTitle,
  SheetTrigger,
} from "@/components/ui/sheet"
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

/** The sandbox's recent connections, from a sheet in its header. */
export function ConnectionsSheet({
  env,
  sandbox,
}: {
  env: string
  sandbox: Sandbox
}) {
  const [open, setOpen] = useState(false)
  return (
    <Sheet open={open} onOpenChange={setOpen}>
      <SheetTrigger render={<Button variant="ghost" size="sm" />}>
        <PlugsConnectedIcon />
        Connections
      </SheetTrigger>
      <SheetContent className="data-[side=right]:sm:max-w-2xl">
        <SheetHeader>
          <SheetTitle>Connections</SheetTitle>
          <SheetDescription>
            Where {sandbox.name} connects, newest first. Rules and requests are
            on the{" "}
            <Link
              to="/e/$env/network"
              params={{ env }}
              className="underline underline-offset-4 hover:text-foreground"
            >
              Network page
            </Link>
            .
          </SheetDescription>
        </SheetHeader>
        <ConnectionLog env={env} id={sandbox.id} />
      </SheetContent>
    </Sheet>
  )
}

/** Mounted only while the sheet is open, so the log refreshes only then. */
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
      <p className="px-6 text-xs text-destructive">
        {errorMessage(conns.error)}
      </p>
    )
  }
  const list = conns.data ?? []
  if (list.length === 0) {
    return (
      <Empty className="mx-6 mb-6">
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
    <div className="min-h-0 flex-1 overflow-auto px-6 pb-6">
      <Table>
        <TableHeader>
          <TableRow>
            <TableHead>Time</TableHead>
            <TableHead>Destination</TableHead>
            <TableHead>Verdict</TableHead>
            <TableHead>Source</TableHead>
            <TableHead className="text-right">Traffic</TableHead>
          </TableRow>
        </TableHeader>
        <TableBody>
          {list.map((c) => (
            <TableRow key={c.id}>
              <TableCell className="text-muted-foreground tabular-nums">
                {new Date(c.started).toLocaleTimeString()}
              </TableCell>
              <TableCell className="font-mono">
                {hostPort(c.host, c.port)}
              </TableCell>
              <TableCell>
                <div className="flex items-center gap-1.5">
                  <VerdictBadge conn={c} />
                  {c.intercepted && <InterceptedMarker conn={c} />}
                </div>
              </TableCell>
              <TableCell className="text-muted-foreground">
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
    </div>
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
            Studio handled the HTTP requests (a proxy rule, or a host with
            secrets)
          </span>
          {reason && <span>{reason}</span>}
        </span>
      </TooltipContent>
    </Tooltip>
  )
}
