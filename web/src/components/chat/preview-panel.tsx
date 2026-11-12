import { useEffect, useRef, useState } from "react"
import {
  ArrowClockwiseIcon,
  ArrowLeftIcon,
  ArrowRightIcon,
  ArrowSquareOutIcon,
  BroadcastIcon,
  BroomIcon,
  CaretDownIcon,
  LockKeyIcon,
  LockSimpleIcon,
} from "@phosphor-icons/react"
import { Link } from "@tanstack/react-router"
import { toast } from "sonner"
import { Button } from "@/components/ui/button"
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuLabel,
  DropdownMenuSeparator,
  DropdownMenuTrigger,
} from "@/components/ui/dropdown-menu"
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
import type { components } from "@/lib/api/schema.gen"
import { inApp, openExternal } from "@/lib/desktop"
import { usePreviewSetting } from "@/lib/preview-setting"
import {
  onStudioMachine,
  freshPreviewHint,
  openPreviewTab,
  previewLoginURL,
} from "@/lib/previews"
import { cn, formatAge } from "@/lib/utils"

type PreviewPort = components["schemas"]["PreviewPort"]

/**
 * The sandbox's running app, framed: the ports it listens on, polled every 5 s, and the
 * chosen one in an iframe that reloads when a request ends (docs/design/chat-mode.md §5).
 * Above it a browser bar locked to the preview: the port is a fixed part of the address that
 * picks another, and only the path is typed. Some apps refuse to be framed, so "Open in new
 * tab" is always next to it.
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
  // The page a link to another port of the sandbox goes to on that port.
  const [landing, setLanding] = useState<{ port: number; path: string }>()

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
  if (!port) return null

  const picker = (
    <PortPicker
      ports={list}
      port={port}
      onPort={(p) => {
        setLanding(undefined)
        setPicked(p)
      }}
    />
  )
  if (!onStudioMachine) {
    return (
      <div className="flex min-h-0 flex-1 flex-col gap-2">
        <div className="@container flex min-w-0 items-center gap-1">
          <div className="flex h-7 items-center rounded-md border">
            {picker}
          </div>
          <Button
            variant="outline"
            size="sm"
            className="ml-auto"
            onClick={() => void openPreviewTab(env, sandboxId, port)}
          >
            <ArrowSquareOutIcon />
            Open in new tab
          </Button>
        </div>
        <p className="text-muted-foreground">
          Previews only open on the computer running Studio.
        </p>
      </div>
    )
  }
  return (
    <PreviewFrame
      key={port}
      env={env}
      sandboxId={sandboxId}
      port={port}
      ports={list}
      picker={picker}
      start={landing?.port === port ? landing.path : undefined}
      onPort={(p, path) => {
        setLanding({ port: p, path })
        setPicked(p)
      }}
      reloadKey={String(reloadKey)}
    />
  )
}

/** The fixed part of the preview's address: localhost and the port, which picks another. */
function PortPicker({
  ports,
  port,
  onPort,
}: {
  ports: PreviewPort[]
  port: number
  onPort: (port: number) => void
}) {
  return (
    <Select
      value={String(port)}
      onValueChange={(v) => v && onPort(Number(v))}
      items={Object.fromEntries(
        ports.map((p) => [String(p.port), `Port ${p.port}`])
      )}
    >
      <SelectTrigger
        size="sm"
        aria-label="Port"
        title="This preview's port: pick another"
        className="h-full shrink-0 gap-1 rounded-none rounded-l-md border-0 border-r bg-transparent px-2 font-mono text-xs text-muted-foreground dark:bg-transparent"
      >
        <LockSimpleIcon className="size-3" />
        <SelectValue>
          {() => (
            <span>
              <span className="hidden @md:inline">localhost</span>:{port}
            </span>
          )}
        </SelectValue>
      </SelectTrigger>
      <SelectContent alignItemWithTrigger={false} align="start">
        {ports.map((p) => (
          <SelectItem key={p.port} value={String(p.port)}>
            Port {p.port}
          </SelectItem>
        ))}
      </SelectContent>
    </Select>
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

/** What the preview's pages say through Studio's bridge (internal/webauth previewBridge). */
type NavMessage = {
  type?: unknown
  event?: unknown
  path?: unknown
  href?: unknown
  back?: unknown
  forward?: unknown
}

const navType = "studio-preview-nav"

/** A path the preview's open endpoint takes: from the root, without the fragment. */
function loadPath(path: string) {
  const bare = path.split("#")[0]
  return bare.startsWith("/") && !bare.startsWith("//") && bare !== "/"
    ? bare
    : undefined
}

/** A typed path, or a whole address on the preview's own origin, as a path from the root. */
function typedPath(raw: string, origin: string) {
  const text = raw.trim()
  if (!text) return "/"
  try {
    const u = new URL(text)
    if (u.origin === origin) return `${u.pathname}${u.search}${u.hash}`
  } catch {
    // Not a whole address: a path.
  }
  return text.startsWith("/") ? text : `/${text}`
}

/** The port a link names on the user's own computer, which is the sandbox's in a preview. */
function localPort(u: URL) {
  if (!["localhost", "127.0.0.1", "0.0.0.0", "[::1]"].includes(u.hostname))
    return undefined
  return Number(u.port || (u.protocol === "https:" ? 443 : 80))
}

type Visit = { path: string; at: number }

function PreviewFrame({
  env,
  sandboxId,
  port,
  ports,
  picker,
  start,
  onPort,
  reloadKey,
}: {
  env: string
  sandboxId: string
  port: number
  ports: PreviewPort[]
  /** The port part of the address. */
  picker: React.ReactNode
  /** The page to open first, such as the one a link on another port named. */
  start?: string
  /** Shows another port of the sandbox, on a page of it. */
  onPort: (port: number, path: string) => void
  reloadKey: string
}) {
  const [src, setSrc] = useState<string>()
  const [error, setError] = useState<string>()
  // When the frame last logged in, and the hidden frame of a renewal while it loads.
  const loggedIn = useRef(0)
  const [renewal, setRenewal] = useState<string>()
  const frame = useRef<HTMLIFrameElement>(null)
  // The app's basic auth challenge, which Studio's proxy hands to this frame.
  const [asked, setAsked] = useState<BasicAuthAsk>()
  // The username last signed in with, offered again when the app refuses the sign-in.
  const [user, setUser] = useState("")
  // Each load counts up; the page it opens is the one shown, and a fresh one also clears the
  // preview's storage as it loads.
  const [load, setLoad] = useState(0)
  const here = useRef(start ?? "/")
  const freshNext = useRef(false)
  // What the page last said through the bridge, and the paths it went to, newest first.
  const [shown, setShown] = useState(start ?? "/")
  const [bridged, setBridged] = useState(false)
  const [history, setHistory] = useState<{
    back: boolean | null
    forward: boolean | null
  }>({ back: null, forward: null })
  const [visits, setVisits] = useState<Visit[]>([])
  const [loading, setLoading] = useState(true)
  const [typed, setTyped] = useState<string>()
  const origin = src ? new URL(src).origin : ""

  const go = (path: string) => {
    here.current = path
    setShown(path)
    setLoad((n) => n + 1)
  }
  const command = (cmd: "back" | "forward" | "go", path?: string) => {
    frame.current?.contentWindow?.postMessage(
      { type: navType, cmd, path },
      origin
    )
  }
  const follow = (href: string) => {
    let u: URL
    try {
      u = new URL(href)
    } catch {
      return
    }
    const path = `${u.pathname}${u.search}${u.hash}`
    const sibling =
      localPort(u) ??
      ports.find((p) => {
        try {
          return new URL(p.url, window.location.href).host === u.host
        } catch {
          return false
        }
      })?.port
    if (sibling !== undefined) {
      if (ports.some((p) => p.port === sibling)) {
        if (sibling === port) go(path)
        else onPort(sibling, path)
      } else {
        toast(`Port ${sibling} isn't listening in the sandbox.`)
      }
      return
    }
    if (inApp) openExternal(u.href)
    else window.open(u.href, "_blank", "noopener,noreferrer")
    toast(`${u.host} isn't part of this preview. It opened in a new tab.`)
  }
  const followRef = useRef(follow)
  useEffect(() => {
    followRef.current = follow
  })

  useEffect(() => {
    const onMessage = (e: MessageEvent) => {
      if (e.source !== frame.current?.contentWindow) return
      const m = e.data as (Partial<BasicAuthAsk> & NavMessage) | null
      if (m?.type === "studio-preview-basic-auth") {
        setAsked({
          realm: typeof m.realm === "string" ? m.realm : "",
          failed: m.failed === true,
          path: typeof m.path === "string" ? m.path : undefined,
        })
        return
      }
      if (m?.type !== navType) return
      if (m.event === "location" && typeof m.path === "string") {
        const path = m.path
        here.current = path
        setShown(path)
        setBridged(true)
        setHistory({
          back: typeof m.back === "boolean" ? m.back : null,
          forward: typeof m.forward === "boolean" ? m.forward : null,
        })
        setVisits((v) =>
          [{ path, at: Date.now() }, ...v.filter((x) => x.path !== path)].slice(
            0,
            10
          )
        )
      } else if (m.event === "loading") {
        setLoading(true)
      } else if (m.event === "link" && typeof m.href === "string") {
        followRef.current(m.href)
      }
    }
    window.addEventListener("message", onMessage)
    return () => window.removeEventListener("message", onMessage)
  }, [])

  // Every load needs a new one-time login URL, and opens the page shown last.
  useEffect(() => {
    let cancelled = false
    const fresh = freshNext.current
    freshNext.current = false
    setLoading(true)
    previewLoginURL(env, sandboxId, port, {
      embed: true,
      fresh,
      path: loadPath(here.current),
    }).then(
      (url) => {
        if (cancelled) return
        loggedIn.current = Date.now()
        setError(undefined)
        setBridged(false)
        setSrc(url)
      },
      (err: unknown) => {
        if (!cancelled) setError(errorMessage(err))
      }
    )
    return () => {
      cancelled = true
    }
  }, [env, sandboxId, port, reloadKey, load])

  // A load that never ends, such as a download, doesn't keep the bar going.
  useEffect(() => {
    if (!loading) return
    const timer = setTimeout(() => setLoading(false), 20_000)
    return () => clearTimeout(timer)
  }, [loading])

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
    const visible = () => document.visibilityState === "visible" && check()
    document.addEventListener("visibilitychange", visible)
    return () => {
      stopped = true
      clearInterval(timer)
      document.removeEventListener("visibilitychange", visible)
    }
  }, [env, sandboxId, port, src])

  const submit = () => {
    if (typed === undefined) return
    const path = typedPath(typed, origin)
    setTyped(undefined)
    if (bridged) command("go", path)
    else go(path)
  }
  const others = ports.filter((p) => p.port !== port)

  return (
    <div className="flex min-h-0 flex-1 flex-col gap-2">
      <div className="@container flex min-w-0 items-center gap-0.5">
        <Button
          variant="ghost"
          size="icon-sm"
          aria-label="Back"
          title="Back"
          disabled={!bridged || history.back === false}
          onClick={() => command("back")}
        >
          <ArrowLeftIcon />
        </Button>
        <Button
          variant="ghost"
          size="icon-sm"
          aria-label="Forward"
          title="Forward"
          disabled={!bridged || history.forward === false}
          onClick={() => command("forward")}
        >
          <ArrowRightIcon />
        </Button>
        <Button
          variant="ghost"
          size="icon-sm"
          aria-label="Reload"
          title="Reload"
          onClick={() => setLoad((n) => n + 1)}
        >
          <ArrowClockwiseIcon />
        </Button>
        <div className="mx-1 flex h-7 min-w-0 flex-1 items-center rounded-md border bg-background focus-within:border-ring focus-within:ring-2 focus-within:ring-ring/30">
          {picker}
          <form
            className="flex min-w-0 flex-1"
            onSubmit={(e) => {
              e.preventDefault()
              submit()
            }}
          >
            <input
              aria-label="Preview path"
              value={typed ?? shown}
              spellCheck={false}
              onFocus={(e) => {
                setTyped(shown)
                e.currentTarget.select()
              }}
              onBlur={() => setTyped(undefined)}
              onChange={(e) => setTyped(e.target.value)}
              onKeyDown={(e) => {
                if (e.key === "Escape") e.currentTarget.blur()
              }}
              className="h-full min-w-0 flex-1 bg-transparent px-2 font-mono text-xs outline-none"
            />
          </form>
          <DropdownMenu>
            <DropdownMenuTrigger
              render={
                <button
                  type="button"
                  aria-label="Pages visited in this preview"
                  title="Pages visited in this preview"
                  className="flex h-full shrink-0 items-center px-1.5 text-muted-foreground hover:text-foreground"
                />
              }
            >
              <CaretDownIcon className="size-3.5" />
            </DropdownMenuTrigger>
            <DropdownMenuContent align="end" className="w-72">
              {visits.length === 0 && (
                <DropdownMenuLabel className="font-normal text-muted-foreground">
                  The pages you visit here show up here.
                </DropdownMenuLabel>
              )}
              {visits.map((v) => (
                <DropdownMenuItem
                  key={v.path}
                  onClick={() => (bridged ? command("go", v.path) : go(v.path))}
                  className={cn(v.path === shown && "font-medium")}
                >
                  <span className="min-w-0 flex-1 truncate font-mono text-xs">
                    {v.path}
                  </span>
                  {v.path !== shown && (
                    <span className="shrink-0 text-caption text-muted-foreground">
                      {formatAge(new Date(v.at).toISOString())}
                    </span>
                  )}
                </DropdownMenuItem>
              ))}
              {others.length > 0 && <DropdownMenuSeparator />}
              {others.map((p) => (
                <DropdownMenuItem
                  key={p.port}
                  onClick={() => onPort(p.port, "/")}
                >
                  <span className="flex-1">Port {p.port}</span>
                  <span className="text-caption text-muted-foreground">
                    switch port
                  </span>
                </DropdownMenuItem>
              ))}
            </DropdownMenuContent>
          </DropdownMenu>
        </div>
        <Button
          variant="ghost"
          size="icon-sm"
          title={`Reload fresh: ${freshPreviewHint.toLowerCase()}`}
          aria-label="Reload fresh"
          onClick={() => {
            freshNext.current = true
            setLoad((n) => n + 1)
          }}
        >
          <BroomIcon />
        </Button>
        <Button
          variant="ghost"
          size="icon-sm"
          aria-label="Open in new tab"
          title="Open in new tab"
          onClick={() =>
            void openPreviewTab(env, sandboxId, port, false, loadPath(shown))
          }
        >
          <ArrowSquareOutIcon />
        </Button>
      </div>
      {error ? (
        <p className="text-muted-foreground">Can't open the preview: {error}</p>
      ) : !src ? (
        <Spinner className="m-auto" />
      ) : (
        <>
          <div className="relative flex min-h-80 flex-1 flex-col">
            <div
              aria-hidden
              className={cn(
                "pointer-events-none absolute inset-x-0 top-0 z-10 h-0.5 overflow-hidden rounded-t-md",
                !loading && "hidden"
              )}
            >
              <div
                role="progressbar"
                aria-label="Loading the page"
                className="h-full w-1/3 animate-preview-load bg-primary motion-reduce:animate-pulse"
              />
            </div>
            <iframe
              ref={frame}
              key={src}
              src={src}
              title={`Preview of port ${port}`}
              referrerPolicy="no-referrer"
              onLoad={() => setLoading(false)}
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
                  setUser(name)
                  setAsked(undefined)
                  go(asked.path ?? here.current)
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
            Blank, or says it refused to connect? Some apps can't be shown
            inside Studio: use Open in new tab.
          </p>
        </>
      )}
    </div>
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
