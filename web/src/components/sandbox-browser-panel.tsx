import { useEffect, useRef, useState, type FormEvent } from "react"
import { useQueryClient } from "@tanstack/react-query"
import { ScrollArea } from "@/components/ui/scroll-area"
import { toast } from "sonner"
import {
  ArrowSquareOutIcon,
  HandIcon,
  PowerIcon,
  StopIcon,
  WarningIcon,
} from "@phosphor-icons/react"
import { BrowserLive, BrowserSessionSelect } from "@/components/browser-live"
import { SidePanel } from "@/components/side-panel"
import { Badge } from "@/components/ui/badge"
import { Button } from "@/components/ui/button"
import {
  Empty,
  EmptyDescription,
  EmptyHeader,
  EmptyTitle,
} from "@/components/ui/empty"
import { Input } from "@/components/ui/input"
import { Spinner } from "@/components/ui/spinner"
import { $api, errorMessage, errorStatus } from "@/lib/api/client"
import { normalizeBrowserAddress } from "@/lib/browser-address"
import type { components } from "@/lib/api/schema.gen"

type BrowserStatus = components["schemas"]["BrowserStatus"]

const browserStatusPath = "/api/environments/{env}/teammates/{teammate}/browser"

/** The teammate's browser beside a sandbox's workspace: its live view, address and takeover. */
export function SandboxBrowserPanel({
  env,
  teammate,
  onClose,
}: {
  env: string
  teammate: string
  onClose: () => void
}) {
  const path = { env, teammate }
  const statusParams = { params: { path } }
  const queryClient = useQueryClient()
  const status = $api.useQuery("get", browserStatusPath, statusParams, {
    refetchInterval: 5000,
  })
  const statusMissing = status.isError && errorStatus(status.error) === 404
  const st = statusMissing ? undefined : status.data
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
      queryKey: ["get", browserStatusPath],
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
    navigate.isPending || takeover.isPending || control.isPending
  const routeEnv = encodeURIComponent(env)
  const routeTeammate = encodeURIComponent(teammate)
  const liveURL =
    `${window.location.origin}/api/environments/${routeEnv}` +
    `/teammates/${routeTeammate}/browser/live` +
    (sessionID ? `?session=${encodeURIComponent(sessionID)}` : "")
  const fullBrowserURL = `/e/${routeEnv}/teammates/${routeTeammate}/browser`
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

  const transitioning = st?.vm === "starting" || st?.vm === "draining"
  const stoppedDescription =
    st?.vm === "starting"
      ? "The browser VM is booting."
      : st?.vm === "draining"
        ? "The browser VM is shutting down."
        : st?.vm === "suspended"
          ? "The browser is suspended. Start it to reconnect to its current page."
          : st?.vm === "crashed"
            ? "The browser stopped unexpectedly. Its profile is kept."
            : st?.vm === "created"
              ? "The browser is created but is not running."
              : "The browser is not running. It can start when an agent uses a browser tool."

  return (
    <SidePanel
      label="Teammate browser"
      title="Browser"
      meta={
        <>
          {st && <Badge variant="outline">{st.vm}</Badge>}
          {driving && (
            <span className="hidden truncate text-[0.625rem] text-muted-foreground sm:inline">
              You are driving
            </span>
          )}
        </>
      }
      actions={
        <a
          href={fullBrowserURL}
          target="_blank"
          rel="noreferrer"
          aria-label="Open this teammate's browser page"
          className="inline-flex h-7 shrink-0 items-center gap-1 rounded-md px-2 text-xs text-muted-foreground hover:bg-muted hover:text-foreground focus-visible:ring-2 focus-visible:ring-ring/50 focus-visible:outline-none"
          title="Open this teammate's browser page"
        >
          <ArrowSquareOutIcon className="size-3.5" />
          <span className="hidden xl:inline">Full page</span>
        </a>
      }
      storageKey="studio.panel.sandbox"
      onClose={onClose}
    >
      {st && (
        <div className="flex shrink-0 flex-wrap items-center gap-1.5 border-b px-2 py-1.5">
          {running ? (
            <>
              {sessions.length > 0 && (
                <BrowserSessionSelect
                  sessions={sessions}
                  value={sessionID}
                  onValueChange={pickSession}
                />
              )}
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
                {driving ? "Hand back" : "Take over"}
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
                {control.isPending ? <Spinner /> : <StopIcon />}
                Stop
              </Button>
            </>
          ) : (
            <Button
              size="sm"
              disabled={navigationBlocked || transitioning}
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
          {st.driver && !driving && (
            <span className="ml-auto truncate text-[0.625rem] text-muted-foreground">
              Last driven by {st.driver}
            </span>
          )}
        </div>
      )}

      <ScrollArea
        className="min-h-0 min-w-0 flex-1"
        viewportClassName="flex flex-col gap-2 p-2"
      >
        {status.isRefetchError && !statusMissing && (
          <div
            role="status"
            className="flex shrink-0 items-center gap-1.5 rounded-md bg-amber-500/5 px-2 py-1 text-xs text-amber-700 dark:text-amber-400"
          >
            <WarningIcon className="size-3 shrink-0" />
            Browser status refresh failed. Retrying…
          </div>
        )}
        {statusMissing ? (
          <Empty className="border">
            <EmptyHeader>
              <EmptyTitle>Browser unavailable</EmptyTitle>
              <EmptyDescription>
                This teammate browser no longer exists.
              </EmptyDescription>
            </EmptyHeader>
          </Empty>
        ) : status.isPending && !st ? (
          <div className="flex min-h-32 flex-col items-center justify-center gap-2 text-sm text-muted-foreground">
            <Spinner />
            Connecting to browser status…
          </div>
        ) : status.isError && !st ? (
          <Empty className="border">
            <EmptyHeader>
              <EmptyTitle>Browser status unavailable</EmptyTitle>
              <EmptyDescription>{errorMessage(status.error)}</EmptyDescription>
            </EmptyHeader>
            <Button
              variant="outline"
              size="sm"
              onClick={() => void status.refetch()}
            >
              Retry
            </Button>
          </Empty>
        ) : running ? (
          <>
            {driving && (
              <p className="text-xs text-muted-foreground">
                Agent browser calls pause until you hand back control. Closing
                this view does not hand back control.
              </p>
            )}
            <form
              className="flex min-w-0 flex-col gap-1.5"
              onSubmit={openAddress}
            >
              <label className="sr-only" htmlFor="sandbox-browser-address">
                Browser address
              </label>
              <div className="flex min-w-0 gap-1.5">
                <Input
                  id="sandbox-browser-address"
                  aria-label="Browser address"
                  aria-invalid={!!addressError}
                  aria-describedby={
                    [
                      !driving && "sandbox-browser-address-hint",
                      addressError && "sandbox-browser-address-error",
                    ]
                      .filter(Boolean)
                      .join(" ") || undefined
                  }
                  autoComplete="url"
                  inputMode="url"
                  placeholder="https://example.com"
                  value={addressDraft}
                  readOnly={!driving || navigationBlocked}
                  className="h-8 min-w-0 flex-1"
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
              </div>
              {!driving && (
                <p
                  id="sandbox-browser-address-hint"
                  className="text-[0.625rem] text-muted-foreground"
                >
                  Take over to open a website.
                </p>
              )}
              {addressError && (
                <p
                  id="sandbox-browser-address-error"
                  role="alert"
                  className="text-xs text-destructive"
                >
                  {addressError}
                </p>
              )}
            </form>
            <BrowserLive
              key={sessionID}
              url={liveURL}
              driving={driving}
              inputEnabled={!navigationBlocked}
              pageURL={pageURL}
              onPageURL={setStreamURL}
              className="min-w-0 overflow-hidden rounded-md border"
            />
          </>
        ) : st ? (
          <Empty className="border">
            <EmptyHeader>
              {transitioning && <Spinner />}
              <EmptyTitle>
                {st.vm === "starting"
                  ? "Browser starting"
                  : st.vm === "draining"
                    ? "Browser stopping"
                    : st.vm === "suspended"
                      ? "Browser suspended"
                      : st.vm === "crashed"
                        ? "Browser stopped unexpectedly"
                        : "Browser stopped"}
              </EmptyTitle>
              <EmptyDescription>{stoppedDescription}</EmptyDescription>
            </EmptyHeader>
          </Empty>
        ) : null}
      </ScrollArea>
    </SidePanel>
  )
}
