import {
  useCallback,
  useEffect,
  useRef,
  useState,
  type FormEvent,
  type ReactNode,
} from "react"
import { Link } from "@tanstack/react-router"
import { useQueryClient } from "@tanstack/react-query"
import { toast } from "sonner"
import {
  ArrowClockwiseIcon,
  ArrowLeftIcon,
  ArrowRightIcon,
  ArrowsOutIcon,
  CaretLeftIcon,
  CaretRightIcon,
  ClockCounterClockwiseIcon,
  CursorClickIcon,
  DetectiveIcon,
  DotsThreeIcon,
  DownloadSimpleIcon,
  EyeSlashIcon,
  HandIcon,
  KeyboardIcon,
  KeyIcon,
  MoonIcon,
  PowerIcon,
  ShieldCheckIcon,
  ShieldWarningIcon,
  SignOutIcon,
  WarningIcon,
  XIcon,
} from "@phosphor-icons/react"
import {
  BrowserActionDecision,
  BrowserControlDecision,
  BrowserCredentialDecision,
} from "@/components/browser-approval"
import { BrowserDownloads } from "@/components/browser-downloads"
import { BrowserLive } from "@/components/browser-live"
import {
  SaveLoginCard,
  SignInStrip,
  SiteLogins,
} from "@/components/browser-logins"
import {
  NoteMarkers,
  NotesBar,
  useBrowserNotes,
} from "@/components/browser-notes"
import { KindChip } from "@/components/kind-chip"
import { TeammateAvatar } from "@/components/teammate-avatar"
import {
  AlertDialog,
  AlertDialogAction,
  AlertDialogCancel,
  AlertDialogContent,
  AlertDialogDescription,
  AlertDialogFooter,
  AlertDialogHeader,
  AlertDialogTitle,
} from "@/components/ui/alert-dialog"
import { Button } from "@/components/ui/button"
import {
  DropdownMenu,
  DropdownMenuCheckboxItem,
  DropdownMenuContent,
  DropdownMenuGroup,
  DropdownMenuItem,
  DropdownMenuSeparator,
  DropdownMenuTrigger,
} from "@/components/ui/dropdown-menu"
import {
  Popover,
  PopoverContent,
  PopoverTitle,
  PopoverTrigger,
} from "@/components/ui/popover"
import { Spinner } from "@/components/ui/spinner"
import { usePendingApprovals } from "@/hooks/use-pending-approvals"
import {
  $api,
  errorMessage,
  fetchClient,
  errorStatus,
  type Approval,
} from "@/lib/api/client"
import type { components } from "@/lib/api/schema.gen"
import { ruleClass, tileClass } from "@/lib/accents"
import { useClaimApprovals } from "@/lib/approval-claims"
import { isBrowserAsk } from "@/lib/approval-kinds"
import { normalizeBrowserAddress } from "@/lib/browser-address"
import { neverOffered, type TypedPassword } from "@/lib/browser-logins"
import {
  defaultPageSize,
  fitPreferred,
  setFitPreferred,
  type PageSize,
} from "@/lib/browser-fit"
import {
  elementWords,
  hostOf,
  originOf,
  policyWords,
  siteRules,
  sitePolicy,
  useBrowserPatterns,
  useSetSitePolicy,
  type SitePolicy,
} from "@/lib/browser-sites"
import { cn, formatAge } from "@/lib/utils"

type BrowserStatus = components["schemas"]["BrowserStatus"]
type BrowserSession = components["schemas"]["BrowserSession"]
export type BrowserAction = components["schemas"]["BrowserAction"]

/** A step of the action log shown in place of the live page, with the steps around it. */
export type BrowserReplay = { steps: BrowserAction[]; at: number }

const statusPath = "/api/environments/{env}/teammates/{teammate}/browser"

export function screenshotURL(env: string, teammate: string, a: BrowserAction) {
  return `/api/environments/${encodeURIComponent(env)}/teammates/${encodeURIComponent(teammate)}/browser/actions/${a.id}/screenshot`
}

/** The page of a step: its own, or the last one before it, as taking the wheel has none. */
function replayURL(replay: BrowserReplay) {
  return (
    replay.steps
      .slice(0, replay.at + 1)
      .reverse()
      .find((a) => a.url)?.url ?? ""
  )
}

/** The newest step at or before `at` that kept a screenshot: not every step has one. */
export function replayShot(replay: BrowserReplay) {
  return replay.steps
    .slice(0, replay.at + 1)
    .reverse()
    .find((a) => a.screenshot)
}

type Props = {
  env: string
  teammate: string
  /** Shows or hides the activity and sites beside the window. */
  onActivity?: () => void
  /** Whether they show. */
  activity?: boolean
  /** Shows the list of sites, from the site info's "All sites". */
  onSites?: () => void
  replay?: BrowserReplay | null
  onReplay?: (replay: BrowserReplay | null) => void
}

/** The page an approval is about: its ephemeral session, "" for the profile. */
function approvalSession(a: Approval) {
  return (
    a.browserAction?.session ??
    a.browserCredential?.session ??
    a.browserControl?.session ??
    ""
  )
}

/**
 * A teammate's browser as a window: a toolbar like any browser's that also says who drives,
 * the live page with the agent's questions over it, and tabs for the throwaway sessions agents
 * opened, when there are any. It is the Browser tab of the teammate's computer screen
 * (docs/browser.md).
 */
export function BrowserWindow({
  env,
  teammate,
  onActivity,
  activity,
  onSites,
  replay = null,
  onReplay,
}: Props) {
  const path = { env, teammate }
  const statusParams = { params: { path } }
  const queryClient = useQueryClient()
  const status = $api.useQuery("get", statusPath, statusParams, {
    refetchInterval: 5000,
  })
  const tm = $api.useQuery("get", "/api/environments/{env}/teammates/{id}", {
    params: { path: { env, id: teammate } },
  })
  const name = tm.data?.name ?? "The teammate"
  const avatar = tm.data?.avatar
  const statusMissing = status.isError && errorStatus(status.error) === 404
  const st = statusMissing ? undefined : status.data
  const running = st?.vm === "running"
  // The window's body shows the live view, rather than the sleeping card, a replay or a notice.
  const liveView = running && !replay

  // The tab the window shows: "" is the profile; a picked session that has ended falls back.
  const [picked, setPicked] = useState("")
  const sessions = st?.sessions ?? []
  const session = sessions.find((s) => s.id === picked)
  const sessionID = session?.id ?? ""
  const driving = session ? session.takeover : !!st?.takeover

  // The page the live view reports wins over the status's, which lags behind it.
  const [streamURL, setStreamURL] = useState<string>()
  useEffect(() => setStreamURL(undefined), [sessionID, running])
  const pageURL = streamURL ?? (session ? session.url : st?.url)
  // A deny rule refused the page's host: the window says so, with the rule a click away.
  const refused = session ? session.blocked : st?.blocked
  const blocked =
    !replay && refused && hostname(pageURL) === refused.host
      ? refused
      : undefined
  // A page asks for a username and password, which the browser can't ask the user for.
  const asked = session ? session.signIn : st?.signIn
  const signIn =
    !replay && asked && hostname(pageURL) === asked.host ? asked : undefined
  // A password the user typed into the page, which the window offers to save.
  const [typedPassword, setTypedPassword] = useState<TypedPassword>()
  useEffect(() => setTypedPassword(undefined), [sessionID, running])
  const offerPassword = useCallback((typed: TypedPassword) => {
    if (!neverOffered(typed.host)) setTypedPassword(typed)
  }, [])
  const dismissPassword = useCallback(() => setTypedPassword(undefined), [])
  const passwordField = useCallback(
    async (at: { x: number; y: number }) => {
      const { data } = await fetchClient.GET(
        "/api/environments/{env}/teammates/{teammate}/browser/element",
        {
          params: {
            path: { env, teammate },
            query: { x: at.x, y: at.y, session: sessionID || undefined },
          },
          signal: AbortSignal.timeout(3000),
        }
      )
      return data?.tag === "input" && data.type?.toLowerCase() === "password"
    },
    [env, teammate, sessionID]
  )
  // The gateway logs the refusal as the page fails, after the live view reports its URL.
  const refetchStatus = status.refetch
  useEffect(() => {
    if (!streamURL) return
    const t = setTimeout(() => void refetchStatus(), 1000)
    return () => clearTimeout(t)
  }, [streamURL, refetchStatus])

  // The user's notes on the live view, on the frame they hold; they belong to that view.
  const notes = useBrowserNotes(env, teammate, sessionID)
  const frame = useRef<HTMLCanvasElement>(null)
  const noting = notes.notes.length > 0 && running && !replay
  const clearNotes = notes.clear
  useEffect(() => clearNotes(), [sessionID, running, clearNotes])
  // The keys the page can't get from a keyboard, under the frame while you drive.
  const [keys, setKeys] = useState(false)
  // Whether the page takes the size of the view, a choice of this computer's; the view's
  // body is the room it fits.
  const [fit, setFit] = useState(fitPreferred)
  const [room, setRoom] = useState<HTMLDivElement | null>(null)

  const pending = usePendingApprovals()
  const asks = (pending.data ?? []).filter(
    (a) => a.environmentId === env && isBrowserAsk(a, teammate)
  )
  const asksHere = running
    ? asks.filter((a) => approvalSession(a) === sessionID)
    : []
  // The questions show on the page, so the floating stack leaves them out while it does.
  useClaimApprovals(replay ? [] : asksHere.map((a) => a.id))

  const refresh = () => {
    queryClient.invalidateQueries({ queryKey: ["get", statusPath] })
    queryClient.invalidateQueries({
      queryKey: ["get", `${statusPath}/actions`],
    })
  }
  const onError = (what: string) => (err: unknown) =>
    toast.error(`Could not ${what}`, { description: errorMessage(err) })
  const control = $api.useMutation("post", `${statusPath}/{action}`, {
    onSettled: refresh,
  })
  const takeover = $api.useMutation("put", `${statusPath}/takeover`, {
    onSettled: refresh,
  })
  const remove = $api.useMutation("delete", statusPath, {
    onSettled: refresh,
    onError: onError("reset the profile"),
  })
  const closeSession = $api.useMutation(
    "delete",
    `${statusPath}/sessions/{session}`,
    { onSettled: refresh, onError: onError("end the session") }
  )
  // A failed fit leaves the page as it was; the view asks again when its frames say so.
  const resize = $api.useMutation("put", `${statusPath}/viewport`)
  const fitPage = (size: PageSize) =>
    resize.mutate({ params: { path }, body: { ...size, session: sessionID } })
  const changeFit = (on: boolean) => {
    setFit(on)
    setFitPreferred(on)
    // The page may have been fitted by an earlier visit, so it goes back either way.
    if (on || !running) return
    resize.mutate({
      params: { path },
      body: { ...defaultPageSize, session: sessionID },
    })
  }
  const [addressError, setAddressError] = useState("")
  const landed = (next: BrowserStatus) => {
    queryClient.setQueryData<BrowserStatus>(
      ["get", statusPath, statusParams],
      next
    )
    const url = sessionID
      ? next.sessions?.find((s) => s.id === sessionID)?.url
      : next.url
    if (url) setStreamURL(url)
    setAddressError("")
  }
  const navigate = $api.useMutation("post", `${statusPath}/navigate`, {
    onSuccess: landed,
    onSettled: refresh,
    onError: (err) => setAddressError(errorMessage(err)),
  })
  const move = $api.useMutation("post", `${statusPath}/go`, {
    onSuccess: landed,
    onSettled: refresh,
    onError: (err) => setAddressError(errorMessage(err)),
  })
  const busy =
    navigate.isPending ||
    move.isPending ||
    takeover.isPending ||
    control.isPending ||
    remove.isPending

  const setWheel = async (on: boolean) => {
    try {
      await takeover.mutateAsync({
        params: { path },
        body: { on, session: sessionID },
      })
      return true
    } catch (err) {
      onError("change who drives")(err)
      return false
    }
  }
  // Browsing yourself takes the wheel: there is no separate step before typing an address.
  const takeWheel = async () => driving || (await setWheel(true))
  const open = async (raw: string) => {
    setAddressError("")
    const normalized = normalizeBrowserAddress(raw)
    if (!normalized.value) {
      setAddressError(normalized.error ?? "Enter a valid website address.")
      return false
    }
    if (!(await takeWheel())) return false
    navigate.mutate({
      params: { path },
      body: { url: normalized.value, session: sessionID },
    })
    return true
  }
  const go = async (to: "back" | "forward" | "reload") => {
    if (!(await takeWheel())) return
    move.mutate({ params: { path }, body: { to, session: sessionID } })
  }
  // Why the last wake failed, on the sleeping browser's card until the next try: a toast
  // that fades left the card as it was, as if waking had done nothing.
  const [wakeError, setWakeError] = useState("")
  useEffect(() => {
    if (running) setWakeError("")
  }, [running])
  const wake = async (raw: string) => {
    setWakeError("")
    let next: BrowserStatus
    try {
      next = await control.mutateAsync({
        params: { path: { ...path, action: "start" } },
      })
    } catch (err) {
      setWakeError(errorMessage(err))
      return
    }
    // The answer is the browser's state after the start: the card goes at once.
    queryClient.setQueryData<BrowserStatus>(
      ["get", statusPath, statusParams],
      next
    )
    if (next.vm !== "running" && next.vm !== "starting") {
      setWakeError(`It still reports “${vmWords(next.vm)}”.`)
      return
    }
    if (raw.trim()) {
      if (!(await setWheel(true))) return
      const normalized = normalizeBrowserAddress(raw)
      if (normalized.value)
        navigate.mutate({ params: { path }, body: { url: normalized.value } })
      else setAddressError(normalized.error ?? "Enter a valid website address.")
    }
  }
  const [resetting, setResetting] = useState(false)

  const w: Win = {
    env,
    teammate,
    name,
    avatar,
    st,
    running,
    sessions,
    session,
    sessionID,
    driving,
    pageURL,
    asks,
    asksHere,
    replay,
    busy,
    pick: (id) => {
      setPicked(id)
      setAddressError("")
      onReplay?.(null)
    },
    open,
    go,
    setWheel,
    endSession: (id) =>
      closeSession.mutate({ params: { path: { ...path, session: id } } }),
    onReplay,
  }

  const live = `${window.location.origin}/api/environments/${encodeURIComponent(env)}/teammates/${encodeURIComponent(teammate)}/browser/live${sessionID ? `?session=${encodeURIComponent(sessionID)}` : ""}`

  return (
    <div className="@container/bw relative flex min-h-0 min-w-0 flex-1 flex-col overflow-hidden bg-background">
      {running && sessions.length > 0 && <TabStrip w={w} />}
      <div className="flex h-10 shrink-0 items-center gap-0.5 border-b px-1.5">
        {replay ? (
          <ReplaySteps w={w} replay={replay} />
        ) : (
          <>
            <Button
              size="icon-sm"
              variant="ghost"
              aria-label="Back"
              title={driving ? "Back" : `Back: takes the wheel from ${name}`}
              disabled={!running || busy}
              onClick={() => go("back")}
            >
              <ArrowLeftIcon />
            </Button>
            <Button
              size="icon-sm"
              variant="ghost"
              aria-label="Forward"
              title={
                driving ? "Forward" : `Forward: takes the wheel from ${name}`
              }
              disabled={!running || busy}
              className="@max-[34rem]/bw:hidden"
              onClick={() => go("forward")}
            >
              <ArrowRightIcon />
            </Button>
            <Button
              size="icon-sm"
              variant="ghost"
              aria-label="Reload"
              title={
                driving ? "Reload" : `Reload: takes the wheel from ${name}`
              }
              disabled={!running || busy}
              onClick={() => go("reload")}
            >
              {navigate.isPending || move.isPending ? (
                <Spinner />
              ) : (
                <ArrowClockwiseIcon />
              )}
            </Button>
          </>
        )}
        {running ? (
          <Address
            key={`${sessionID}:${replay?.steps[replay.at]?.id ?? "live"}`}
            w={w}
            url={
              replay
                ? replayURL(replay)
                : pageURL === "about:blank"
                  ? ""
                  : (pageURL ?? "")
            }
            onSites={onSites}
          />
        ) : (
          <span className="mx-1 flex h-7 min-w-0 flex-1 items-center rounded-full bg-muted px-3 text-xs text-muted-foreground">
            <span className="truncate">{vmWords(st?.vm)}</span>
          </span>
        )}
        {running && <Driver w={w} />}
        {liveView && driving && !noting && (
          // A phone or a touch screen always has the keys (BrowserLive).
          <Button
            variant="ghost"
            size="icon-sm"
            aria-label={keys ? "Hide keyboard controls" : "Keyboard controls"}
            aria-expanded={keys}
            title="Keys the page can't get from your keyboard: Ctrl, Esc, Tab, arrows"
            className={cn(
              "max-md:hidden any-pointer-coarse:hidden",
              keys && "bg-muted"
            )}
            onClick={() => setKeys(!keys)}
          >
            <KeyboardIcon />
          </Button>
        )}
        {running && <DownloadsButton env={env} teammate={teammate} />}
        {onActivity && (
          <Button
            variant="ghost"
            size="icon-sm"
            aria-label="Activity and sites"
            title="Activity and sites"
            aria-pressed={!!activity}
            className={cn(activity && "bg-muted")}
            onClick={onActivity}
          >
            <ClockCounterClockwiseIcon />
          </Button>
        )}
        <DropdownMenu modal={false}>
          <DropdownMenuTrigger
            render={
              <Button
                variant="ghost"
                size="icon-sm"
                aria-label="Browser options"
                title="Browser options"
              />
            }
          >
            <DotsThreeIcon weight="bold" />
          </DropdownMenuTrigger>
          <DropdownMenuContent align="end" className="w-60">
            <DropdownMenuGroup>
              {running ? (
                <DropdownMenuItem
                  disabled={busy}
                  onClick={() =>
                    control.mutate(
                      { params: { path: { ...path, action: "stop" } } },
                      { onError: onError("put the browser to sleep") }
                    )
                  }
                >
                  <MoonIcon />
                  Put to sleep
                </DropdownMenuItem>
              ) : (
                <DropdownMenuItem
                  disabled={busy || !st || transitioning(st.vm)}
                  onClick={() => void wake("")}
                >
                  <PowerIcon />
                  Wake up
                </DropdownMenuItem>
              )}
              <DropdownMenuItem
                render={<Link to="/e/$env/settings/network" params={{ env }} />}
              >
                <ShieldCheckIcon />
                Network rules
              </DropdownMenuItem>
            </DropdownMenuGroup>
            <DropdownMenuSeparator />
            <DropdownMenuGroup>
              <DropdownMenuCheckboxItem
                checked={fit}
                onCheckedChange={changeFit}
                title="The page takes the size of this view, so it shows at full size. Agents get the same page."
              >
                <ArrowsOutIcon />
                Fit the page to this view
              </DropdownMenuCheckboxItem>
            </DropdownMenuGroup>
            <DropdownMenuSeparator />
            <DropdownMenuGroup>
              <DropdownMenuItem
                variant="destructive"
                disabled={busy || !st || st.vm === "absent"}
                onClick={() => setResetting(true)}
              >
                <SignOutIcon />
                Reset profile…
              </DropdownMenuItem>
            </DropdownMenuGroup>
          </DropdownMenuContent>
        </DropdownMenu>
      </div>
      {addressError && (
        <p
          role="alert"
          className="shrink-0 border-b bg-destructive/5 px-3 py-1 text-xs text-destructive"
        >
          {addressError}
        </p>
      )}
      {running && blocked && (
        <div
          role="status"
          className="flex shrink-0 items-center gap-2 border-b bg-(--st-waiting)/10 px-3 py-1 text-xs"
        >
          <ShieldWarningIcon className="size-3.5 shrink-0 text-(--st-waiting-fg)" />
          <span className="min-w-0 flex-1">
            A network rule blocks{" "}
            <b className="font-mono font-semibold whitespace-nowrap">
              {blocked.host}
            </b>
            : deny{" "}
            <span className="font-mono whitespace-nowrap">
              {blocked.ruleHost}
            </span>
            .
          </span>
          <Button
            variant="outline"
            size="xs"
            className="shrink-0"
            render={
              <Link
                to="/e/$env/settings/network"
                params={{ env }}
                search={{ rule: blocked.ruleId }}
              />
            }
          >
            Open the rule
          </Button>
        </div>
      )}
      {running && signIn && (
        <SignInStrip
          key={`${signIn.host} ${signIn.login ?? ""} ${signIn.refused}`}
          env={env}
          teammate={teammate}
          name={name}
          signIn={signIn}
          onSignedIn={() => void go("reload")}
        />
      )}
      {liveView && typedPassword && (
        <SaveLoginCard
          key={`${typedPassword.host} ${typedPassword.password}`}
          env={env}
          teammate={teammate}
          name={name}
          typed={typedPassword}
          onDone={dismissPassword}
        />
      )}
      {noting && (
        <NotesBar
          env={env}
          teammate={teammate}
          name={name}
          driver={session ? session.sandbox : st.driver}
          notes={notes}
          canvas={frame}
        />
      )}
      {status.isRefetchError && !statusMissing && (
        <p
          role="status"
          className="flex shrink-0 items-center gap-1.5 border-b bg-amber-500/5 px-3 py-1 text-xs text-amber-700 dark:text-amber-400"
        >
          <WarningIcon className="size-3 shrink-0" />
          Couldn't refresh the browser's state. Retrying…
        </p>
      )}
      {/* The questions sit above the view, which then has less room, rather than in it, where
          they would push the frame out of its box. */}
      {liveView && asksHere.length > 0 && (
        <div className="grid max-h-[50%] shrink-0 gap-2 overflow-y-auto border-b p-2">
          {asksHere.map((a) => (
            <AskCard key={a.id} approval={a} name={name} />
          ))}
        </div>
      )}
      <div
        ref={setRoom}
        data-testid="browser-view"
        className={cn(
          // A size container: the live view fits its frame into the room it has (cqw, cqh).
          "[container-type:size] relative flex min-h-0 flex-1 flex-col bg-muted/40",
          // The live view fits by construction, so it never scrolls: clip rather than hidden,
          // which would still let focus or a scrollIntoView scroll it with no way back, takes
          // a sub-pixel of rounding or the frame before the controls under it are measured.
          // The other states can be taller than a short panel and scroll.
          liveView ? "overflow-clip" : "overflow-auto"
        )}
      >
        {statusMissing ? (
          <Notice title="Browser unavailable">
            This teammate no longer exists.
          </Notice>
        ) : !st ? (
          status.isError ? (
            <Notice title="Browser state unavailable">
              {errorMessage(status.error)}
              <Button
                variant="outline"
                size="sm"
                className="mt-2"
                onClick={() => void status.refetch()}
              >
                Retry
              </Button>
            </Notice>
          ) : (
            <div className="flex flex-1 items-center justify-center">
              <Spinner />
            </div>
          )
        ) : !running ? (
          <Asleep
            w={w}
            waking={control.isPending}
            error={wakeError}
            onWake={wake}
          />
        ) : replay ? (
          <Replayed w={w} replay={replay} />
        ) : (
          <div className="group relative mx-auto w-fit max-w-full">
            <BrowserLive
              key={sessionID}
              url={live}
              name={name}
              driving={driving}
              inputEnabled={!busy}
              pageURL={pageURL}
              onPageURL={setStreamURL}
              frozen={noting}
              onNote={notes.add}
              overlay={<NoteMarkers notes={notes} name={name} />}
              canvasRef={frame}
              controlsOpen={keys}
              onControlsOpen={setKeys}
              fitRoom={fit ? room : undefined}
              onFit={fitPage}
              passwordField={passwordField}
              onPassword={offerPassword}
            />
            {!noting && (
              <span className="pointer-events-none absolute top-2 right-2 rounded-full bg-foreground/75 px-2.5 py-1 text-caption text-background opacity-0 shadow-sm transition group-hover:opacity-100">
                Right-click to add a note for {name}
              </span>
            )}
          </div>
        )}
      </div>
      <AlertDialog open={resetting} onOpenChange={setResetting}>
        <AlertDialogContent>
          <AlertDialogHeader>
            <AlertDialogTitle>Reset {name}'s browser profile?</AlertDialogTitle>
            <AlertDialogDescription>
              {name} is signed out of every site: cookies and site data go, with
              the browser's VM. The activity log and site rules stay.
            </AlertDialogDescription>
          </AlertDialogHeader>
          <AlertDialogFooter>
            <AlertDialogCancel>Cancel</AlertDialogCancel>
            <AlertDialogAction
              variant="destructive"
              onClick={() => {
                remove.mutate({ params: { path } })
                setResetting(false)
              }}
            >
              Reset profile
            </AlertDialogAction>
          </AlertDialogFooter>
        </AlertDialogContent>
      </AlertDialog>
    </div>
  )
}

/** What the window's parts share. */
type Win = {
  env: string
  teammate: string
  name: string
  avatar?: string
  st?: BrowserStatus
  running: boolean
  sessions: BrowserSession[]
  session?: BrowserSession
  sessionID: string
  driving: boolean
  pageURL?: string
  asks: Approval[]
  asksHere: Approval[]
  replay: BrowserReplay | null
  busy: boolean
  pick: (session: string) => void
  open: (raw: string) => Promise<boolean>
  go: (to: "back" | "forward" | "reload") => Promise<void>
  setWheel: (on: boolean) => Promise<boolean>
  endSession: (session: string) => void
  onReplay?: (replay: BrowserReplay | null) => void
}

/** The URL's host name, lower-cased as the gateway logs it; "" for none. */
function hostname(url?: string) {
  try {
    return url ? new URL(url).hostname.toLowerCase() : ""
  } catch {
    return ""
  }
}

function transitioning(vm?: BrowserStatus["vm"]) {
  return vm === "starting" || vm === "draining"
}

function vmWords(vm?: BrowserStatus["vm"]) {
  switch (vm) {
    case "starting":
      return "Waking up…"
    case "draining":
      return "Going to sleep…"
    case "crashed":
      return "Stopped unexpectedly"
    case "absent":
      return "No browser yet"
    case undefined:
      return "…"
    default:
      return "Asleep"
  }
}

function tabLabel(url?: string) {
  if (!url || url === "about:blank") return "New tab"
  return hostOf(url) || url
}

/**
 * The profile and the throwaway sessions agents opened, as tabs: the page's site in each, the
 * teammate's face or a throwaway's mask in front of it, like any browser's tabs. The profile
 * alone needs none.
 */
function TabStrip({ w }: { w: Win }) {
  const waiting = (id: string) => w.asks.some((a) => approvalSession(a) === id)
  const tab = (
    id: string,
    icon: ReactNode,
    url: string | undefined,
    title: string
  ) => {
    const active = w.sessionID === id
    return (
      <div
        key={id || "profile"}
        className={cn(
          "group relative flex h-7 max-w-44 min-w-0 flex-1 items-center gap-1.5 rounded-t-md border border-transparent px-2 text-xs",
          active
            ? "-mb-px border-border border-b-background bg-background text-foreground"
            : "text-muted-foreground hover:bg-background/50 hover:text-foreground"
        )}
      >
        <button
          type="button"
          role="tab"
          aria-selected={active}
          title={title}
          onClick={() => w.pick(id)}
          className="flex min-w-0 flex-1 items-center gap-1.5 text-left outline-none after:absolute after:inset-0 focus-visible:underline"
        >
          <span className="flex size-4 shrink-0 items-center justify-center">
            {icon}
          </span>
          <span className="min-w-0 truncate">{tabLabel(url)}</span>
          {!active && waiting(id) && (
            <span
              className="ml-auto size-1.5 shrink-0 rounded-full bg-(--st-waiting)"
              aria-label="Waiting for you"
            />
          )}
        </button>
        {id && (
          <button
            type="button"
            aria-label="End the throwaway session"
            title="End the session: its cookies and storage are gone"
            onClick={() => w.endSession(id)}
            className="relative z-10 -mr-1 inline-flex size-5 shrink-0 items-center justify-center rounded-full hover:bg-muted"
          >
            <XIcon className="size-3" />
          </button>
        )}
      </div>
    )
  }
  return (
    <div
      role="tablist"
      aria-label="Browser tabs"
      className="flex h-9 shrink-0 items-end gap-1 overflow-x-auto border-b bg-muted/50 px-2"
    >
      {tab(
        "",
        <TeammateAvatar
          name={w.name}
          avatar={w.avatar}
          className="size-4 text-[0.4375rem]"
        />,
        w.sessionID ? w.st?.url : w.pageURL,
        `${w.name}'s own profile: logins and cookies stay between sessions`
      )}
      {w.sessions.map((s) =>
        tab(
          s.id,
          <DetectiveIcon className="size-3.5" />,
          w.sessionID === s.id ? w.pageURL : s.url,
          `A throwaway session an agent in ${s.sandbox} opened: a temporary profile that is gone when it closes`
        )
      )}
    </div>
  )
}

/**
 * The address bar. Always editable: opening a page while an agent drives takes the wheel, and
 * the bar says so while you type. The shield in front opens what agents may do on the site.
 */
function Address({
  w,
  url,
  onSites,
}: {
  w: Win
  url: string
  onSites?: () => void
}) {
  // What you typed, while you edit; otherwise the bar follows the page.
  const [draft, setDraft] = useState<string | null>(null)
  const [focused, setFocused] = useState(false)
  const input = useRef<HTMLInputElement>(null)
  const editable = !w.replay
  const submit = async (e: FormEvent) => {
    e.preventDefault()
    if (!editable || w.busy) return
    if (await w.open(draft ?? url)) {
      setDraft(null)
      input.current?.blur()
    }
  }
  // The value doesn't change on focus: text that arrives as the field focuses (a fast typist,
  // autofill) would land after it instead of replacing it. An address without a scheme opens
  // as https.
  const shown = draft ?? url.replace(/^https:\/\//, "")
  return (
    <form
      onSubmit={submit}
      className={cn(
        "relative mx-1 flex h-7 min-w-0 flex-1 items-center rounded-full border border-transparent bg-muted pr-1 focus-within:border-ring focus-within:bg-background focus-within:ring-2 focus-within:ring-ring/30",
        !editable && "opacity-80"
      )}
    >
      <SiteInfo w={w} url={url} onSites={onSites} />
      <input
        ref={input}
        aria-label="Browser address"
        autoComplete="url"
        inputMode="url"
        placeholder="Search or enter an address"
        value={shown}
        readOnly={!editable}
        spellCheck={false}
        onFocus={(e) => {
          setFocused(true)
          const el = e.target
          requestAnimationFrame(() => el.select())
        }}
        onBlur={() => {
          setFocused(false)
          setDraft(null)
        }}
        onChange={(e) => setDraft(e.target.value)}
        className="h-full min-w-0 flex-1 bg-transparent font-mono text-xs text-ellipsis outline-none placeholder:font-sans"
      />
      {focused && editable && !w.driving && (
        <span className="ml-1 shrink-0 rounded bg-muted px-1.5 py-0.5 text-meta whitespace-nowrap text-muted-foreground max-sm:hidden">
          ↵ takes the wheel from {w.name}
        </span>
      )}
      {w.replay && (
        <span className="ml-1 shrink-0 rounded bg-muted px-1.5 py-0.5 text-meta text-muted-foreground">
          Replay
        </span>
      )}
    </form>
  )
}

/** What agents may do on the site in the address bar, like a browser's site info. */
function SiteInfo({
  w,
  url,
  onSites,
}: {
  w: Win
  url: string
  onSites?: () => void
}) {
  const origin = originOf(url)
  const patterns = useBrowserPatterns(w.env, w.teammate)
  const setPolicy = useSetSitePolicy(w.env, w.teammate)
  const [open, setOpen] = useState(false)
  const all = patterns.data ?? []
  const asking = w.asksHere.length > 0
  const rules = siteRules(all, origin)
  return (
    <Popover open={open} onOpenChange={setOpen}>
      <PopoverTrigger
        render={
          <button
            type="button"
            aria-label={origin ? `Site info for ${hostOf(url)}` : "Site info"}
            className={cn(
              "ml-0.5 inline-flex h-6 shrink-0 items-center gap-1 rounded-full px-1.5 hover:bg-background",
              asking ? "text-(--st-waiting)" : "text-muted-foreground"
            )}
          />
        }
      >
        {asking ? (
          <ShieldWarningIcon className="size-3.5" weight="fill" />
        ) : (
          <ShieldCheckIcon className="size-3.5" />
        )}
      </PopoverTrigger>
      <PopoverContent>
        {origin ? (
          <div className="grid gap-3">
            <div>
              <PopoverTitle className="font-mono text-title">
                {hostOf(url)}
              </PopoverTitle>
              <p className="text-muted-foreground">
                Reading pages is always allowed; scripts, cookies and storage
                never.
              </p>
            </div>
            <PolicyPicker
              value={sitePolicy(all, origin)}
              onChange={(p) => void setPolicy(origin, p)}
            />
            <dl className="grid grid-cols-[7.5rem_1fr] gap-y-1.5">
              <dt className="text-muted-foreground">Hidden from {w.name}</dt>
              <dd className="flex flex-wrap gap-1">
                {["Passwords", ...rules.hidden.map(elementWords)].map((f) => (
                  <span
                    key={f}
                    className="inline-flex items-center gap-1 rounded bg-muted px-1.5 py-0.5"
                  >
                    <EyeSlashIcon className="size-3" />
                    {f}
                  </span>
                ))}
              </dd>
              {rules.actions.length > 0 && (
                <>
                  <dt className="text-muted-foreground">Other rules</dt>
                  <dd className="grid gap-0.5">
                    {rules.actions.map((p) => (
                      <span key={p.id}>
                        {p.verdict === "allow" ? "Allow" : "Block"}{" "}
                        <span className="font-mono">{p.action}</span> on{" "}
                        {elementWords(p)}
                      </span>
                    ))}
                  </dd>
                </>
              )}
              <dt className="text-muted-foreground">Logins</dt>
              <dd>
                <SiteLogins
                  env={w.env}
                  teammate={w.teammate}
                  name={w.name}
                  host={hostOf(url)}
                />
              </dd>
            </dl>
            {onSites && (
              <button
                type="button"
                className="justify-self-start text-muted-foreground underline-offset-4 hover:text-foreground hover:underline"
                onClick={() => {
                  setOpen(false)
                  onSites()
                }}
              >
                All sites
              </button>
            )}
          </div>
        ) : (
          <p className="text-muted-foreground">
            Open a website to see what {w.name} may do on it.
          </p>
        )}
      </PopoverContent>
    </Popover>
  )
}

/** Ask first / Allowed / Blocked for clicks and typing on a site. */
export function PolicyPicker({
  value,
  onChange,
}: {
  value: SitePolicy
  onChange: (p: SitePolicy) => void
}) {
  return (
    <div className="grid gap-1">
      <span className="text-muted-foreground">Clicks and typing</span>
      <div
        role="radiogroup"
        aria-label="Clicks and typing"
        className="inline-flex w-fit rounded-md border p-0.5"
      >
        {(Object.keys(policyWords) as SitePolicy[]).map((p) => (
          <button
            key={p}
            type="button"
            role="radio"
            aria-checked={value === p}
            onClick={() => onChange(p)}
            className={cn(
              "rounded px-2 py-0.5",
              value === p
                ? "bg-foreground text-background"
                : "text-muted-foreground hover:text-foreground"
            )}
          >
            {policyWords[p]}
          </button>
        ))}
      </div>
    </div>
  )
}

/** The files the browser saved, each sendable to one of the teammate's sandboxes, once any are. */
function DownloadsButton({ env, teammate }: { env: string; teammate: string }) {
  const downloads = $api.useQuery(
    "get",
    `${statusPath}/downloads`,
    { params: { path: { env, teammate } } },
    { refetchInterval: 10000 }
  )
  const count = downloads.data?.length ?? 0
  if (count === 0) return null
  return (
    <Popover>
      <PopoverTrigger
        render={
          <Button
            size="icon-sm"
            variant="ghost"
            aria-label={`Downloads, ${count}`}
            title="Downloads"
            className="relative"
          />
        }
      >
        <DownloadSimpleIcon />
        <span className="absolute -top-0.5 -right-0.5 inline-flex size-3.5 items-center justify-center rounded-full bg-primary font-mono text-[0.5625rem] text-primary-foreground">
          {count}
        </span>
      </PopoverTrigger>
      <PopoverContent align="end" className="w-[34rem]">
        <PopoverTitle className="mb-2">Downloads</PopoverTitle>
        <BrowserDownloads env={env} teammate={teammate} />
      </PopoverContent>
    </Popover>
  )
}

/**
 * Who drives, after the address, with the one thing to do about it: take the wheel, or hand
 * it back. A narrow screen keeps the face and the button; the words go to the title.
 */
function Driver({ w }: { w: Win }) {
  const words = (children: ReactNode, className?: string) => (
    <span className={cn("min-w-0 truncate @max-[30rem]/bw:hidden", className)}>
      {children}
    </span>
  )
  if (w.replay)
    return (
      <span className="ml-1 flex min-w-0 shrink-0 items-center gap-1.5 rounded-full bg-muted py-0.5 pr-0.5 pl-2 text-xs">
        <ClockCounterClockwiseIcon className="size-3.5 shrink-0 text-muted-foreground" />
        {words(
          stepTime(w.replay.steps[w.replay.at]),
          "font-mono text-muted-foreground"
        )}
        <Button
          size="xs"
          className="rounded-full"
          onClick={() => w.onReplay?.(null)}
        >
          Back to live
        </Button>
      </span>
    )
  if (w.driving)
    return (
      <span
        title={`You're driving: ${w.name}'s browser calls wait until you hand back.`}
        className="ml-1 flex min-w-0 shrink-0 items-center gap-1.5 rounded-full bg-primary/10 py-0.5 pr-0.5 pl-2 text-xs"
      >
        <HandIcon className="size-3.5 shrink-0 text-primary" weight="fill" />
        {words("You're driving", "font-medium")}
        <Button
          size="xs"
          aria-label={`Hand back to ${w.name}`}
          className="rounded-full"
          disabled={w.busy}
          onClick={() => void w.setWheel(false)}
        >
          Hand back
        </Button>
      </span>
    )

  const waiting = w.asksHere.length > 0
  const face = w.session ? (
    <DetectiveIcon className="size-3.5 shrink-0 text-muted-foreground" />
  ) : (
    <span className="relative shrink-0">
      <TeammateAvatar
        name={w.name}
        avatar={w.avatar}
        className="size-4 text-[0.4375rem]"
      />
      {waiting && (
        <span className="absolute -right-0.5 -bottom-0.5 size-1.5 rounded-full bg-(--st-waiting) ring-1 ring-background" />
      )}
    </span>
  )
  const [said, title] = w.session
    ? [
        words(`From ${w.session.sandbox}`, "text-muted-foreground"),
        `An agent in ${w.session.sandbox} uses this throwaway session: a temporary profile, so nothing reaches ${w.name}'s. It ends when closed, with its chat or after a while unused.`,
      ]
    : waiting
      ? [
          words(`${w.name} is waiting for you`, "font-medium"),
          `${w.name} is waiting for you: answer on the page.`,
        ]
      : w.st?.driver
        ? [
            words(`${w.name} is using it`),
            `${w.name} is using the browser, last from ${w.st.driver}.`,
          ]
        : [
            words(`${w.name} isn't using it`, "text-muted-foreground"),
            `${w.name} isn't using the browser. It sleeps after a while unused.`,
          ]
  return (
    <span
      title={title}
      className={cn(
        "ml-1 flex min-w-0 shrink-0 items-center gap-1.5 rounded-full py-0.5 pr-0.5 pl-2 text-xs",
        waiting && "bg-(--st-waiting)/10"
      )}
    >
      {face}
      {said}
      <Button
        size="xs"
        variant="ghost"
        aria-label="Take the wheel"
        title="Take the wheel"
        className="rounded-full text-primary hover:text-primary"
        disabled={w.busy}
        onClick={() => void w.setWheel(true)}
      >
        <HandIcon />
        {/* On a phone the address needs the room more than the words. */}
        <span className="@max-[24rem]/bw:hidden">Take the wheel</span>
      </Button>
    </span>
  )
}

/** In place of back and forward while a step of the log shows: the steps around it. */
function ReplaySteps({ w, replay }: { w: Win; replay: BrowserReplay }) {
  const { steps, at } = replay
  return (
    <span className="flex shrink-0 items-center">
      <Button
        size="icon-sm"
        variant="ghost"
        aria-label="Previous step"
        disabled={at === 0}
        onClick={() => w.onReplay?.({ steps, at: at - 1 })}
      >
        <CaretLeftIcon />
      </Button>
      <span className="font-mono text-caption text-muted-foreground tabular-nums">
        {at + 1}/{steps.length}
      </span>
      <Button
        size="icon-sm"
        variant="ghost"
        aria-label="Next step"
        disabled={at + 1 >= steps.length}
        onClick={() => w.onReplay?.({ steps, at: at + 1 })}
      >
        <CaretRightIcon />
      </Button>
    </span>
  )
}

/** What each action of the log did, and what it would do, before its object. */
const stepVerbs: Partial<Record<string, [done: string, asked: string]>> = {
  open: ["opened", "open"],
  back: ["went back", "go back"],
  forward: ["went forward", "go forward"],
  reload: ["reloaded the page", "reload the page"],
  click: ["clicked", "click"],
  fill: ["typed", "type"],
  type: ["typed", "type"],
  select: ["picked", "pick"],
  press: ["pressed", "press"],
  scroll: ["scrolled", "scroll"],
  get_text: ["read", "read"],
  screenshot: ["took a screenshot", "take a screenshot"],
  wait: ["waited for the page", "wait for the page"],
  fill_credential: ["filled a login into", "fill a login into"],
  "request control": ["asked you for the wheel", "ask you for the wheel"],
  close: ["closed the session", "close the session"],
  takeover: ["took the wheel", "take the wheel"],
  handback: ["handed the wheel back", "hand the wheel back"],
  "send download": ["sent the download", "send the download"],
}

/** An element of the log, `button "Sign in"`, as its name: “Sign in”; or its role alone. */
function elementName(target?: string) {
  const m = target?.match(/^[^"]*? "(.*)"$/)
  return m ? `“${m[1]}”` : (target ?? "")
}

/** A page's address without its scheme: shop.example.com/login. */
function pageWords(url?: string) {
  if (!url) return ""
  try {
    const u = new URL(url)
    return u.host + (u.pathname === "/" ? "" : u.pathname)
  } catch {
    return url
  }
}

/**
 * A step of the log in words: `Ada clicked “Send”`, `You opened shop.example.com/login`. A
 * step that waits for you or didn't happen says what was asked or tried.
 */
export function stepWords(a: BrowserAction, name: string) {
  const who = a.actor === "user" ? "You" : name
  const verbs = stepVerbs[a.action]
  if (!verbs) return [who, a.action, a.target].filter(Boolean).join(" ")
  const el = elementName(a.target)
  // Typed text and picked options are quoted in the detail; a hidden field's is a count.
  const typed = a.detail?.match(/^(\d+) characters into a sensitive field$/)
  const object =
    a.action === "open"
      ? pageWords(a.url)
      : a.action === "fill" || a.action === "type"
        ? `${typed ? `${typed[1]} hidden characters` : (a.detail ?? "")} into ${el}`
        : a.action === "select"
          ? `${a.detail ?? ""} in ${el}`
          : a.action === "press" || a.action === "scroll"
            ? (a.detail ?? "")
            : a.action === "send download"
              ? (a.target ?? "")
              : el
  const verb =
    a.outcome === "pending"
      ? `asked to ${verbs[1]}`
      : a.outcome === "ok"
        ? verbs[0]
        : `tried to ${verbs[1]}`
  return [who, verb, object.trim()].filter(Boolean).join(" ")
}

/**
 * What a step's words leave out: why it was denied or failed, the reason an agent asked for
 * the wheel, why a session closed.
 */
export function stepDetail(a: BrowserAction) {
  if (a.action === "request control" || a.action === "close") return a.detail
  if (a.outcome === "denied" || a.outcome === "failed") return a.detail
  return undefined
}

/** When a step was, to the second. */
function stepTime(a: BrowserAction) {
  return new Date(a.at).toLocaleTimeString([], {
    hour: "2-digit",
    minute: "2-digit",
    second: "2-digit",
  })
}

/** The question an agent asks, on the page it is about. */
function AskCard({ approval, name }: { approval: Approval; name: string }) {
  const act = approval.browserAction
  const cred = approval.browserCredential
  const control = approval.browserControl
  const Icon = control ? HandIcon : cred ? KeyIcon : CursorClickIcon
  return (
    <div
      role="alertdialog"
      aria-label={`${name} is asking`}
      className={cn(
        "grid animate-[tm-in_.25s_ease-out] gap-2 rounded-lg border border-l-3 bg-popover p-3 text-xs text-popover-foreground shadow-xl",
        ruleClass("browser")
      )}
    >
      <div className="flex items-center gap-2">
        <span
          className={cn(
            "inline-flex size-6 shrink-0 items-center justify-center rounded",
            tileClass("browser")
          )}
        >
          <Icon className="size-3.5" weight="bold" />
        </span>
        <KindChip>
          {control ? "Handback" : cred ? "Login" : "Page action"}
        </KindChip>
        <span className="ml-auto text-muted-foreground">
          {formatAge(approval.createdAt)}
        </span>
      </div>
      <p className="text-xs">
        {control ? (
          <>{name} asks you to hand the browser back.</>
        ) : cred ? (
          <>
            {name} wants Studio to fill <b>{cred.label || "a field"}</b> with{" "}
            <span className="font-mono">{cred.credential}</span>.
          </>
        ) : (
          <>
            {name} wants to {act?.action ?? "act"}
            {act?.label && (
              <>
                {" "}
                <b>“{act.label}”</b>
              </>
            )}{" "}
            on <span className="font-mono">{hostOf(act?.url)}</span>.
          </>
        )}
      </p>
      {control ? (
        <BrowserControlDecision approval={approval} />
      ) : cred ? (
        <BrowserCredentialDecision approval={approval} />
      ) : (
        <BrowserActionDecision approval={approval} />
      )}
    </div>
  )
}

/** A step of the log, as the page looked then, with the step in words over it. */
function Replayed({ w, replay }: { w: Win; replay: BrowserReplay }) {
  const shot = replayShot(replay)
  const step = replay.steps[replay.at]
  return (
    <div
      className={cn(
        "relative mx-auto flex w-full max-w-6xl flex-col",
        !shot && "flex-1"
      )}
    >
      {shot ? (
        <img
          src={screenshotURL(w.env, w.teammate, shot)}
          alt={`The page after ${shot.action}`}
          className="w-full grayscale-[.35]"
        />
      ) : (
        <Notice title="No picture of this step">
          Steps keep a screenshot when they change the page.
        </Notice>
      )}
      <p className="absolute inset-x-3 bottom-3 mx-auto w-fit max-w-[calc(100%-1.5rem)] rounded-full bg-foreground/80 px-3 py-1 text-xs text-background shadow-sm">
        <span className="font-mono opacity-70">{stepTime(step)}</span>{" "}
        {stepWords(step, w.name)}
      </p>
    </div>
  )
}

function Notice({ title, children }: { title: string; children: ReactNode }) {
  return (
    <div className="flex flex-1 items-center justify-center p-4">
      <div className="grid max-w-sm justify-items-center gap-1 text-center">
        <p className="text-sm font-medium">{title}</p>
        <div className="grid justify-items-center text-xs text-muted-foreground">
          {children}
        </div>
      </div>
    </div>
  )
}

/** The sleeping browser: its last page, faded, and a way to open a page right there. */
function Asleep({
  w,
  waking,
  error,
  onWake,
}: {
  w: Win
  waking: boolean
  /** Why the last wake failed. */
  error: string
  onWake: (url: string) => Promise<void>
}) {
  const [url, setUrl] = useState("")
  const actions = $api.useQuery("get", `${statusPath}/actions`, {
    params: {
      path: { env: w.env, teammate: w.teammate },
      query: { limit: 50 },
    },
  })
  const last = actions.data?.find((a) => a.screenshot && !a.ephemeral)
  const vm = w.st?.vm
  const starting = waking || vm === "starting"
  return (
    <div className="relative flex min-h-80 flex-1 items-center justify-center overflow-hidden">
      {last && (
        <img
          aria-hidden
          alt=""
          src={screenshotURL(w.env, w.teammate, last)}
          className="pointer-events-none absolute inset-x-0 top-0 mx-auto w-full max-w-6xl opacity-15 blur-[2px] grayscale"
        />
      )}
      <div className="relative m-4 grid w-full max-w-sm justify-items-center gap-3 rounded-2xl bg-background/80 p-5 text-center backdrop-blur-sm">
        {starting || vm === "draining" ? (
          <>
            <Spinner className="mx-auto" />
            <p className="text-sm font-medium">
              {vm === "draining"
                ? `${w.name}'s browser is going to sleep…`
                : `Waking ${w.name}'s browser…`}
            </p>
            {last?.url && (
              <p className="text-xs text-muted-foreground">
                Logins and cookies are kept.
              </p>
            )}
          </>
        ) : (
          <>
            <span
              className={cn(
                "inline-flex size-10 items-center justify-center rounded-full",
                tileClass("browser")
              )}
            >
              {vm === "crashed" ? (
                <WarningIcon className="size-4.5" weight="fill" />
              ) : (
                <MoonIcon className="size-4.5" weight="fill" />
              )}
            </span>
            <div>
              <p className="text-sm font-medium">
                {vm === "crashed"
                  ? `${w.name}'s browser stopped unexpectedly`
                  : vm === "absent"
                    ? `${w.name} has no browser yet`
                    : `${w.name}'s browser is asleep`}
              </p>
              <p className="mt-1 text-xs text-muted-foreground">
                {vm === "absent"
                  ? `It is made when ${w.name} first browses, or when you open a page. Only Studio reaches it.`
                  : `It wakes when ${w.name} browses, or when you open a page. Logins and cookies are kept.`}
              </p>
            </div>
            <form
              className="flex w-full gap-1.5"
              onSubmit={(e) => {
                e.preventDefault()
                void onWake(url)
              }}
            >
              <input
                aria-label="Open a page"
                placeholder="Open a page…"
                value={url}
                onChange={(e) => setUrl(e.target.value)}
                className="h-8 min-w-0 flex-1 rounded-full border bg-background px-3 font-mono text-xs outline-none placeholder:font-sans focus:border-ring focus:ring-2 focus:ring-ring/30"
              />
              <Button size="sm" type="submit" className="rounded-full">
                {url.trim() ? "Open" : error ? "Try again" : "Wake up"}
              </Button>
            </form>
            {error && (
              <p
                role="alert"
                className="rounded-md bg-destructive/5 px-2 py-1 text-left text-xs text-destructive"
              >
                Couldn't wake the browser. {error}
              </p>
            )}
            {last?.url && (
              <p className="text-caption text-muted-foreground">
                Last page <span className="font-mono">{hostOf(last.url)}</span>
              </p>
            )}
          </>
        )}
      </div>
    </div>
  )
}
