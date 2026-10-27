import { useEffect, useRef, useState } from "react"
import {
  ArrowClockwiseIcon,
  BroomIcon,
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
  freshPreviewHint,
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
  // Each reload counts up; a fresh one also clears the preview's storage as it loads.
  const [nonce, setNonce] = useState({ n: 0, fresh: false })

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
          onClick={() => setNonce((r) => ({ n: r.n + 1, fresh: false }))}
        >
          <ArrowClockwiseIcon />
        </Button>
        <Button
          variant="ghost"
          size="icon-sm"
          title={`Reload fresh: ${freshPreviewHint.toLowerCase()}`}
          aria-label="Reload fresh"
          disabled={!onStudioMachine}
          onClick={() => setNonce((r) => ({ n: r.n + 1, fresh: true }))}
        >
          <BroomIcon />
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
            reloadKey={`${String(reloadKey)}:${nonce.n}${nonce.fresh ? ":fresh" : ""}`}
          />
        ) : (
          <p className="text-muted-foreground">
            Previews only open on the computer running Studio.
          </p>
        ))}
    </div>
  )
}

/** How old a shown preview's login gets before it is renewed; its cookies last eight hours. */
const renewAfter = 60 * 60_000

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
  // When the frame last logged in, and the hidden frame of a renewal while it loads.
  const loggedIn = useRef(0)
  const [renewal, setRenewal] = useState<string>()

  // Every load needs a new one-time login URL.
  useEffect(() => {
    let cancelled = false
    previewLoginURL(env, sandboxId, port, {
      embed: true,
      fresh: reloadKey.endsWith(":fresh"),
    }).then(
      (url) => {
        if (cancelled) return
        loggedIn.current = Date.now()
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

  // The preview's cookies last eight hours from its login (docs/api.md). Once the login is an
  // hour old, a hidden frame logs in again, so an app left open keeps working, its service
  // worker's fetches included, without being reloaded. The check runs every minute, which
  // also catches up after the computer slept, and when the tab shows again.
  useEffect(() => {
    if (!src) return
    let busy = false
    let stopped = false
    const check = () => {
      if (busy || Date.now() - loggedIn.current < renewAfter) return
      busy = true
      previewLoginURL(env, sandboxId, port, { embed: true, renew: true })
        .then((url) => {
          if (stopped) return
          loggedIn.current = Date.now()
          setRenewal(url)
        })
        // Studio may be down for a moment; the next check tries again.
        .catch(() => undefined)
        .finally(() => (busy = false))
    }
    const timer = setInterval(check, 60_000)
    const shown = () => document.visibilityState === "visible" && check()
    document.addEventListener("visibilitychange", shown)
    return () => {
      stopped = true
      clearInterval(timer)
      document.removeEventListener("visibilitychange", shown)
    }
  }, [env, sandboxId, port, src])

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
      {renewal && (
        <iframe
          hidden
          src={renewal}
          title={`Renewing the preview of port ${port}`}
          referrerPolicy="no-referrer"
          onLoad={() => setRenewal(undefined)}
        />
      )}
      <p className="text-caption text-muted-foreground">
        Blank, or says it refused to connect? Some apps can't be shown inside
        Studio: use Open in new tab.
      </p>
    </>
  )
}
