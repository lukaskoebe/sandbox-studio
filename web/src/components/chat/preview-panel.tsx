import { useEffect, useState } from "react"
import {
  ArrowClockwiseIcon,
  ArrowSquareOutIcon,
  BroadcastIcon,
} from "@phosphor-icons/react"
import { Button } from "@/components/ui/button"
import {
  Empty,
  EmptyDescription,
  EmptyHeader,
  EmptyMedia,
  EmptyTitle,
} from "@/components/ui/empty"
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select"
import { Spinner } from "@/components/ui/spinner"
import { $api, errorMessage } from "@/lib/api/client"
import {
  onStudioMachine,
  openPreviewTab,
  previewLoginURL,
} from "@/lib/previews"

/**
 * The sandbox's running app, framed: the ports it listens on, polled every 5 s, and the
 * chosen one in an iframe that reloads when a request ends (docs/design/chat-mode.md §5).
 * Some apps refuse to be framed, so "Open in new tab" is always next to it.
 */
export function PreviewPanel({
  env,
  sandboxId,
  reloadKey,
}: {
  env: string
  sandboxId: string
  /** Changes when the app may have changed, e.g. when a request ends. */
  reloadKey: unknown
}) {
  const ports = $api.useQuery(
    "get",
    "/api/environments/{env}/sandboxes/{id}/ports",
    { params: { path: { env, id: sandboxId } } },
    { refetchInterval: 5000 }
  )
  const list = ports.data ?? []
  const [picked, setPicked] = useState<number>()
  // The first port until one is picked, and again if the picked one goes away.
  const port = list.some((p) => p.port === picked) ? picked : list.at(0)?.port
  const [nonce, setNonce] = useState(0)

  if (list.length === 0) {
    return (
      <Empty>
        <EmptyHeader>
          <EmptyMedia variant="icon">
            {ports.isPending ? <Spinner /> : <BroadcastIcon />}
          </EmptyMedia>
          <EmptyTitle>Nothing to preview yet</EmptyTitle>
          <EmptyDescription>
            {ports.isError
              ? `Can't check the sandbox: ${errorMessage(ports.error)}`
              : "When your teammate starts a website or app in the sandbox, it shows up here. Ask it to start one, for example “run the app so I can see it”."}
          </EmptyDescription>
        </EmptyHeader>
      </Empty>
    )
  }

  return (
    <div className="flex min-h-0 flex-1 flex-col gap-2">
      <div className="flex min-w-0 items-center gap-1">
        <Select
          value={String(port)}
          onValueChange={(v) => v && setPicked(Number(v))}
          items={Object.fromEntries(
            list.map((p) => [String(p.port), `Port ${p.port}`])
          )}
        >
          <SelectTrigger size="sm" className="w-32" aria-label="Port">
            <SelectValue />
          </SelectTrigger>
          <SelectContent>
            {list.map((p) => (
              <SelectItem key={p.port} value={String(p.port)}>
                Port {p.port}
              </SelectItem>
            ))}
          </SelectContent>
        </Select>
        <Button
          variant="ghost"
          size="icon-sm"
          title="Reload"
          disabled={!onStudioMachine}
          onClick={() => setNonce((n) => n + 1)}
        >
          <ArrowClockwiseIcon />
        </Button>
        <Button
          variant="outline"
          size="sm"
          className="ml-auto"
          onClick={() => port && void openPreviewTab(env, sandboxId, port)}
        >
          <ArrowSquareOutIcon />
          Open in new tab
        </Button>
      </div>
      {port &&
        (onStudioMachine ? (
          <PreviewFrame
            key={port}
            env={env}
            sandboxId={sandboxId}
            port={port}
            reloadKey={`${String(reloadKey)}:${nonce}`}
          />
        ) : (
          <p className="text-muted-foreground">
            Previews only open on the computer running Studio.
          </p>
        ))}
    </div>
  )
}

function PreviewFrame({
  env,
  sandboxId,
  port,
  reloadKey,
}: {
  env: string
  sandboxId: string
  port: number
  reloadKey: string
}) {
  const [src, setSrc] = useState<string>()
  const [error, setError] = useState<string>()

  // Every load needs a new one-time login URL.
  useEffect(() => {
    let cancelled = false
    previewLoginURL(env, sandboxId, port, true).then(
      (url) => {
        if (cancelled) return
        setError(undefined)
        setSrc(url)
      },
      (err: unknown) => {
        if (!cancelled) setError(errorMessage(err))
      }
    )
    return () => {
      cancelled = true
    }
  }, [env, sandboxId, port, reloadKey])

  if (error) {
    return (
      <p className="text-muted-foreground">Can't open the preview: {error}</p>
    )
  }
  if (!src) return <Spinner className="m-auto" />
  return (
    <>
      <iframe
        key={src}
        src={src}
        title={`Preview of port ${port}`}
        referrerPolicy="no-referrer"
        className="min-h-80 w-full flex-1 rounded-md bg-white ring-1 ring-foreground/10"
      />
      <p className="text-[0.6875rem] text-muted-foreground">
        Blank, or says it refused to connect? Some apps can't be shown inside
        Studio: use Open in new tab.
      </p>
    </>
  )
}
