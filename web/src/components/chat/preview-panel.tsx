import { useEffect, useRef, useState } from "react"
import {
  ArrowClockwiseIcon,
  BroomIcon,
  ArrowSquareOutIcon,
  BroadcastIcon,
  LockKeyIcon,
} from "@phosphor-icons/react"
import { Link } from "@tanstack/react-router"
import { Button } from "@/components/ui/button"
import {
  Empty,
  EmptyContent,
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
import { Input } from "@/components/ui/input"
import { Label } from "@/components/ui/label"
import { Spinner } from "@/components/ui/spinner"
import { $api, errorMessage, fetchClient } from "@/lib/api/client"
import { usePreviewSetting } from "@/lib/preview-setting"
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
  port: asked,
  onPort,
}: {
  env: string
  sandboxId: string
  /** Changes when the app may have changed, e.g. when a request ends. */
  reloadKey: unknown
  /** The port to show, such as the one a link named; picking another calls onPort. */
  port?: number
  onPort?: (port: number) => void
}) {
  const ports = $api.useQuery(
    "get",
    "/api/environments/{env}/sandboxes/{id}/ports",
    { params: { path: { env, id: sandboxId } } },
    { refetchInterval: 5000 }
  )
  const list = ports.data ?? []
  const [own, setOwn] = useState<number>()
  const picked = onPort ? asked : (own ?? asked)
  const setPicked = onPort ?? setOwn
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

/**
 * The Preview tab while previews are off, reached from a link such as a preview on Home: it says
 * so and offers to turn them on, rather than showing another tab. The ports are listed, as the
 * setting promises.
 */
export function PreviewsOff({
  env,
  sandboxId,
  port,
}: {
  env: string
  sandboxId: string
  port?: number
}) {
  const previews = usePreviewSetting()
  const ports = $api.useQuery(
    "get",
    "/api/environments/{env}/sandboxes/{id}/ports",
    { params: { path: { env, id: sandboxId } } },
    { refetchInterval: 5000 }
  )
  const list = (ports.data ?? []).map((p) => p.port)
  return (
    <Empty>
      <EmptyHeader>
        <EmptyMedia variant="icon">
          <BroadcastIcon />
        </EmptyMedia>
        <EmptyTitle>Previews are off</EmptyTitle>
        <EmptyDescription>
          {port ? `Port ${port} can't open here` : "Ports can't open here"}{" "}
          while previews of sandbox ports are off in{" "}
          <Link
            to="/e/$env/settings/general"
            params={{ env }}
            className="underline underline-offset-4"
          >
            Settings › General
          </Link>
          .{" "}
          {list.length
            ? `The sandbox listens on ${list.map((p) => `:${p}`).join(", ")}.`
            : port && ports.data
              ? "The sandbox doesn't listen on any port now."
              : ""}
        </EmptyDescription>
      </EmptyHeader>
      <EmptyContent>
        <Button
          size="sm"
          disabled={!previews.loaded || previews.pending}
          onClick={() => previews.setEnabled(true)}
        >
          Turn on previews
        </Button>
      </EmptyContent>
    </Empty>
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
  const frame = useRef<HTMLIFrameElement>(null)
  // The app's basic auth challenge, which Studio's proxy hands to this frame, and the page to
  // open again once signed in.
  const [asked, setAsked] = useState<BasicAuthAsk>()
  const [again, setAgain] = useState<{ n: number; path?: string }>({ n: 0 })
  // The username last signed in with, offered again when the app refuses the sign-in.
  const [user, setUser] = useState("")

  useEffect(() => {
    const onMessage = (e: MessageEvent) => {
      const m = e.data as (Partial<BasicAuthAsk> & { type?: unknown }) | null
      if (
        e.source !== frame.current?.contentWindow ||
        m?.type !== "studio-preview-basic-auth"
      )
        return
      setAsked({
        realm: typeof m.realm === "string" ? m.realm : "",
        failed: m.failed === true,
        path: typeof m.path === "string" ? m.path : undefined,
      })
    }
    window.addEventListener("message", onMessage)
    return () => window.removeEventListener("message", onMessage)
  }, [])

  // Every load needs a new one-time login URL.
  useEffect(() => {
    let cancelled = false
    previewLoginURL(env, sandboxId, port, {
      embed: true,
      fresh: again.n === 0 && reloadKey.endsWith(":fresh"),
      path: again.path,
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
  }, [env, sandboxId, port, reloadKey, again])

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
      <div className="relative flex min-h-80 flex-1 flex-col">
        <iframe
          ref={frame}
          key={src}
          src={src}
          title={`Preview of port ${port}`}
          referrerPolicy="no-referrer"
          className="min-h-80 w-full flex-1 rounded-md bg-white ring-1 ring-foreground/10"
        />
        {asked && (
          <BasicAuthDialog
            env={env}
            sandboxId={sandboxId}
            port={port}
            asked={asked}
            user={user}
            onCancel={() => setAsked(undefined)}
            onSignedIn={(name) => {
              const path = asked.path
              setUser(name)
              setAsked(undefined)
              setAgain((a) => ({ n: a.n + 1, path }))
            }}
          />
        )}
      </div>
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

type BasicAuthAsk = { realm: string; failed: boolean; path?: string }

/**
 * Studio's own sign-in for a preview that asks for HTTP basic auth, over the preview alone, in
 * place of the browser's dialog over all of Studio. Studio keeps the sign-in until it stops and
 * sends it with this preview's requests only.
 */
function BasicAuthDialog({
  env,
  sandboxId,
  port,
  asked,
  user,
  onCancel,
  onSignedIn,
}: {
  env: string
  sandboxId: string
  port: number
  asked: BasicAuthAsk
  /** The username to start from. */
  user: string
  onCancel: () => void
  onSignedIn: (username: string) => void
}) {
  const [username, setUsername] = useState(user)
  const [password, setPassword] = useState("")
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState<string>()
  const submit = async () => {
    setBusy(true)
    setError(undefined)
    const res = await fetchClient.PUT(
      "/api/environments/{env}/sandboxes/{id}/previews/{port}/basic-auth",
      {
        params: { path: { env, id: sandboxId, port } },
        body: { username, password },
      }
    )
    setBusy(false)
    if (res.error) setError(errorMessage(res.error))
    else onSignedIn(username)
  }
  return (
    <div className="absolute inset-0 z-10 grid place-items-center rounded-md bg-background/70 p-4 backdrop-blur-[2px]">
      <form
        role="dialog"
        aria-modal="false"
        aria-labelledby="preview-sign-in-title"
        className="grid w-full max-w-80 gap-3 rounded-lg bg-popover p-4 text-popover-foreground shadow-md ring-1 ring-foreground/10"
        onSubmit={(e) => {
          e.preventDefault()
          void submit()
        }}
        onKeyDown={(e) => {
          if (e.key === "Escape") onCancel()
        }}
      >
        <div className="grid gap-1">
          <h3
            id="preview-sign-in-title"
            className="flex items-center gap-1.5 font-heading text-sm font-medium"
          >
            <LockKeyIcon className="size-4 text-muted-foreground" />
            Sign in to port {port}
          </h3>
          <p className="text-caption text-muted-foreground">
            {asked.realm ? (
              <>
                The app asks for a username and password for{" "}
                <span className="font-medium text-foreground">
                  “{asked.realm}”
                </span>
                .
              </>
            ) : (
              "The app asks for a username and password."
            )}
          </p>
          {asked.failed && (
            <p className="text-caption text-destructive">
              It didn’t take that sign-in. Try again.
            </p>
          )}
        </div>
        <div className="grid gap-1.5">
          <Label htmlFor="preview-sign-in-user">Username</Label>
          <Input
            id="preview-sign-in-user"
            autoFocus={!user}
            autoComplete="off"
            value={username}
            onChange={(e) => setUsername(e.target.value)}
          />
        </div>
        <div className="grid gap-1.5">
          <Label htmlFor="preview-sign-in-password">Password</Label>
          <Input
            id="preview-sign-in-password"
            autoFocus={!!user}
            type="password"
            autoComplete="off"
            value={password}
            onChange={(e) => setPassword(e.target.value)}
          />
        </div>
        {error && <p className="text-caption text-destructive">{error}</p>}
        <p className="text-caption text-muted-foreground">
          Studio sends it only to this preview, and forgets it when it stops.
        </p>
        <div className="flex justify-end gap-2">
          <Button type="button" variant="ghost" size="sm" onClick={onCancel}>
            Cancel
          </Button>
          <Button type="submit" size="sm" disabled={busy || !username}>
            {busy && <Spinner />}
            Sign in
          </Button>
        </div>
      </form>
    </div>
  )
}
