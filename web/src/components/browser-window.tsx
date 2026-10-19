import {
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
  ArrowsOutSimpleIcon,
  CaretLeftIcon,
  CaretRightIcon,
  ClockCounterClockwiseIcon,
  CursorClickIcon,
  DetectiveIcon,
  DotsThreeIcon,
  DownloadSimpleIcon,
  EyeSlashIcon,
  HandIcon,
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
  BrowserCredentialDecision,
} from "@/components/browser-approval"
import { BrowserDownloads } from "@/components/browser-downloads"
import { BrowserLive } from "@/components/browser-live"
import { KindChip } from "@/components/kind-chip"
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
import {
  $api,
  errorMessage,
  errorStatus,
  type Approval,
} from "@/lib/api/client"
import type { components } from "@/lib/api/schema.gen"
import { ruleClass, tileClass } from "@/lib/accents"
import { useClaimApprovals } from "@/lib/approval-claims"
import { normalizeBrowserAddress } from "@/lib/browser-address"
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
  /** "full" on the teammate's Browser tab; "panel" beside a sandbox's terminal. */
  size: "full" | "panel"
  /** Opens the browser on the teammate's page, from the panel. */
  onExpand?: () => void
  /** Shows the list of sites, from the site info's "All sites". */
  onSites?: () => void
  replay?: BrowserReplay | null
  onReplay?: (replay: BrowserReplay | null) => void
}

/** The page an approval is about: its ephemeral session, "" for the profile. */
function approvalSession(a: Approval) {
  return a.browserAction?.session ?? a.browserCredential?.session ?? ""
}

/**
 * A teammate's browser as a window: tabs for its profile and the throwaway sessions agents
 * opened, a toolbar like any browser's, one line that says who drives, and the live page with
 * the agent's questions on it. The same window sits on the teammate's Browser tab and beside
 * a sandbox's terminal (docs/browser.md).
 */
export function BrowserWindow({
  env,
  teammate,
  size,
  onExpand,
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

  const pending = $api.useQuery(
    "get",
    "/api/approvals",
    { params: { query: { status: "pending" } } },
    { refetchInterval: 15000 }
  )
  const asks = (pending.data ?? []).filter(
    (a) =>
      a.environmentId === env &&
      (a.kind === "browser.action" || a.kind === "browser.credential") &&
      (a.browserAction?.teammateId ?? a.browserCredential?.teammateId) ===
        teammate
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
    onError: onError("change the browser"),
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
  const wake = async (raw: string) => {
    try {
      await control.mutateAsync({
        params: { path: { ...path, action: "start" } },
      })
    } catch {
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
    size,
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
    <div
      className={cn(
        "relative flex min-h-0 min-w-0 flex-1 flex-col overflow-hidden bg-background",
        size === "full" && "rounded-lg border shadow-xs"
      )}
    >
      {running && <TabStrip w={w} />}
      <div className="flex shrink-0 items-center gap-1 border-b px-2 py-1.5">
        {running ? (
          <>
            <Button
              size="icon-sm"
              variant="ghost"
              aria-label="Back"
              title={driving ? "Back" : `Back: takes the wheel from ${name}`}
              disabled={!!replay || busy}
              onClick={() => go("back")}
            >
              <ArrowLeftIcon />
            </Button>
            {size === "full" && (
              <Button
                size="icon-sm"
                variant="ghost"
                aria-label="Forward"
                title={
                  driving ? "Forward" : `Forward: takes the wheel from ${name}`
                }
                disabled={!!replay || busy}
                onClick={() => go("forward")}
              >
                <ArrowRightIcon />
              </Button>
            )}
            <Button
              size="icon-sm"
              variant="ghost"
              aria-label="Reload"
              title={
                driving ? "Reload" : `Reload: takes the wheel from ${name}`
              }
              disabled={!!replay || busy}
              onClick={() => go("reload")}
            >
              {navigate.isPending || move.isPending ? (
                <Spinner />
              ) : (
                <ArrowClockwiseIcon />
              )}
            </Button>
            <Address
              key={`${sessionID}:${replay?.steps[replay.at]?.id ?? "live"}`}
              w={w}
              url={
                replay
                  ? (replay.steps[replay.at]?.url ?? "")
                  : pageURL === "about:blank"
                    ? ""
                    : (pageURL ?? "")
              }
              onSites={onSites}
            />
            <DownloadsButton env={env} teammate={teammate} />
          </>
        ) : (
          <span className="flex-1 truncate px-1 text-xs text-muted-foreground">
            {vmWords(st?.vm)}
          </span>
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
              {onExpand && (
                <DropdownMenuItem onClick={onExpand}>
                  <ArrowsOutSimpleIcon />
                  Open on {name}'s page
                </DropdownMenuItem>
              )}
              {running ? (
                <DropdownMenuItem
                  disabled={busy}
                  onClick={() =>
                    control.mutate({
                      params: { path: { ...path, action: "stop" } },
                    })
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
                render={<Link to="/e/$env/network/rules" params={{ env }} />}
              >
                <ShieldCheckIcon />
                Network rules
              </DropdownMenuItem>
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
      {running && <DriverBar w={w} />}
      <div
        className={cn(
          // A size container: the live view fits its frame into the room it has (cqw, cqh).
          "[container-type:size] relative flex min-h-0 flex-1 flex-col overflow-auto bg-muted/50",
          size === "full" ? "p-4" : "p-0"
        )}
      >
        {status.isRefetchError && !statusMissing && (
          <p
            role="status"
            className="mb-2 flex shrink-0 items-center gap-1.5 rounded-md bg-amber-500/5 px-2 py-1 text-xs text-amber-700 dark:text-amber-400"
          >
            <WarningIcon className="size-3 shrink-0" />
            Couldn't refresh the browser's state. Retrying…
          </p>
        )}
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
          <Asleep w={w} waking={control.isPending} onWake={wake} />
        ) : replay ? (
          <Replayed w={w} replay={replay} />
        ) : (
          <>
            {asksHere.length > 0 && (
              <div
                className={cn(
                  "z-20 grid gap-2",
                  size === "full"
                    ? "absolute top-6 left-6 w-80 max-w-[calc(100%-3rem)]"
                    : "m-2"
                )}
              >
                {asksHere.map((a) => (
                  <AskCard key={a.id} approval={a} name={name} />
                ))}
              </div>
            )}
            <div
              className={cn(
                "group relative mx-auto w-fit max-w-full",
                size === "full" &&
                  "overflow-hidden rounded-md shadow-sm ring-1 ring-border [--live-max:72rem]"
              )}
            >
              <BrowserLive
                key={sessionID}
                url={live}
                driving={driving}
                inputEnabled={!busy}
                pageURL={pageURL}
                onPageURL={setStreamURL}
              />
              {!driving && (
                <div className="absolute inset-x-0 bottom-0 flex justify-center pb-4 opacity-0 transition group-hover:opacity-100 focus-within:opacity-100">
                  <Button
                    size="sm"
                    variant="outline"
                    className="bg-background/95 shadow-md"
                    disabled={busy}
                    onClick={() => void setWheel(true)}
                  >
                    <HandIcon />
                    Watching {name} · take the wheel to use the page
                  </Button>
                </div>
              )}
            </div>
          </>
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
  size: Props["size"]
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
 * teammate's face or a throwaway's mask in front of it, like any browser's tabs.
 */
function TabStrip({ w }: { w: Win }) {
  const waiting = (id: string) => w.asks.some((a) => approvalSession(a) === id)
  const tab = (
    id: string,
    icon: ReactNode,
    url: string | undefined,
    title: string,
    extra?: ReactNode
  ) => {
    const active = w.sessionID === id
    return (
      <div
        key={id || "profile"}
        className={cn(
          "group relative flex h-8 min-w-0 items-center gap-1.5 rounded-t-lg border border-transparent px-2.5 text-xs",
          w.size === "full" ? "w-56" : "max-w-44 flex-1",
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
          className="flex min-w-0 flex-1 items-center gap-2 text-left outline-none after:absolute after:inset-0 focus-visible:underline"
        >
          <span className="flex size-4 shrink-0 items-center justify-center">
            {icon}
          </span>
          <span className="min-w-0 truncate">{tabLabel(url)}</span>
          {extra}
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
      className="flex h-10 shrink-0 items-end gap-1 overflow-x-auto border-b bg-muted/50 px-2"
    >
      {tab(
        "",
        w.avatar ? (
          <span className="text-[0.8125rem] leading-none">{w.avatar}</span>
        ) : (
          <ShieldCheckIcon className="size-3.5" />
        ),
        w.sessionID ? w.st?.url : w.pageURL,
        `${w.name}'s own profile: logins and cookies stay between sessions`
      )}
      {w.sessions.map((s) =>
        tab(
          s.id,
          <DetectiveIcon className="size-3.5" />,
          w.sessionID === s.id ? w.pageURL : s.url,
          `A throwaway session an agent in ${s.sandbox} opened: a temporary profile that is gone when it closes`,
          w.size === "full" && (
            <span className="ml-auto max-w-[45%] min-w-0 shrink-0 truncate font-mono text-[0.625rem] text-muted-foreground">
              {s.sandbox}
            </span>
          )
        )
      )}
      <span className="mb-3 ml-auto inline-flex shrink-0 items-center gap-1 pl-2 font-mono text-[0.625rem] text-muted-foreground">
        {w.replay ? (
          <>
            <ClockCounterClockwiseIcon className="size-3" />
            REPLAY
          </>
        ) : (
          <>
            <span className="size-1.5 animate-pulse rounded-full bg-(--st-running)" />
            LIVE
          </>
        )}
      </span>
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
        "relative flex h-8 min-w-0 flex-1 items-center rounded-md border bg-muted/40 pr-1 focus-within:border-ring focus-within:bg-background focus-within:ring-2 focus-within:ring-ring/30",
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
        className="h-full min-w-0 flex-1 bg-transparent font-mono text-xs outline-none placeholder:font-sans"
      />
      {focused && editable && !w.driving && (
        <span className="ml-1 shrink-0 rounded bg-muted px-1.5 py-0.5 text-[0.625rem] whitespace-nowrap text-muted-foreground max-sm:hidden">
          ↵ takes the wheel from {w.name}
        </span>
      )}
      {w.replay && (
        <span className="ml-1 shrink-0 rounded bg-muted px-1.5 py-0.5 text-[0.625rem] text-muted-foreground">
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
              "ml-0.5 inline-flex h-6 shrink-0 items-center gap-1 rounded px-1.5 hover:bg-muted",
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
              <PopoverTitle className="font-mono text-[0.8125rem]">
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
              <dd>Filled by Studio from the vault when you allow it</dd>
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

/** The files the browser saved, each sendable to one of the teammate's sandboxes. */
function DownloadsButton({ env, teammate }: { env: string; teammate: string }) {
  const downloads = $api.useQuery(
    "get",
    `${statusPath}/downloads`,
    { params: { path: { env, teammate } } },
    { refetchInterval: 10000 }
  )
  const count = downloads.data?.length ?? 0
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
        {count > 0 && (
          <span className="absolute -top-0.5 -right-0.5 inline-flex size-3.5 items-center justify-center rounded-full bg-primary font-mono text-[0.5625rem] text-primary-foreground">
            {count}
          </span>
        )}
      </PopoverTrigger>
      <PopoverContent align="end" className="w-[34rem]">
        <PopoverTitle className="mb-2">Downloads</PopoverTitle>
        <BrowserDownloads env={env} teammate={teammate} />
      </PopoverContent>
    </Popover>
  )
}

/** One line under the toolbar that always answers "who is driving, and what now?". */
function DriverBar({ w }: { w: Win }) {
  const panel = w.size === "panel"
  const bar = (className: string, children: ReactNode) => (
    <div
      className={cn(
        "flex min-h-9 shrink-0 flex-wrap items-center gap-x-2 gap-y-1 border-b px-3 py-1.5 text-xs",
        className
      )}
    >
      {children}
    </div>
  )

  if (w.replay) {
    const { steps, at } = w.replay
    const step = steps[at]
    return bar(
      "bg-muted/60",
      <>
        <ClockCounterClockwiseIcon className="size-3.5 shrink-0" />
        <span className="min-w-0 flex-1 truncate">
          <span className="font-medium">Replay</span>{" "}
          <span className="font-mono text-muted-foreground">
            {new Date(step.at).toLocaleTimeString()}
          </span>{" "}
          {stepWords(step, w.name)}
        </span>
        <span className="flex items-center gap-0.5">
          <Button
            size="icon-xs"
            variant="ghost"
            aria-label="Previous step"
            disabled={at === 0}
            onClick={() => w.onReplay?.({ steps, at: at - 1 })}
          >
            <CaretLeftIcon />
          </Button>
          <span className="font-mono text-muted-foreground">
            {at + 1}/{steps.length}
          </span>
          <Button
            size="icon-xs"
            variant="ghost"
            aria-label="Next step"
            disabled={at + 1 >= steps.length}
            onClick={() => w.onReplay?.({ steps, at: at + 1 })}
          >
            <CaretRightIcon />
          </Button>
        </span>
        <Button size="xs" onClick={() => w.onReplay?.(null)}>
          Back to live
        </Button>
      </>
    )
  }

  if (w.session && !w.driving)
    return bar(
      "bg-muted/40",
      <>
        <DetectiveIcon className="size-3.5 shrink-0" />
        <span className="min-w-0 flex-1">
          {panel
            ? `Throwaway session from ${w.session.sandbox}.`
            : `An agent in ${w.session.sandbox} uses this throwaway session: a temporary profile, so nothing reaches ${w.name}'s. It ends when closed, with its chat or after a while unused.`}
        </span>
        <Button
          size="xs"
          variant="outline"
          onClick={() => void w.setWheel(true)}
        >
          <HandIcon />
          Take the wheel
        </Button>
        <Button
          size="xs"
          variant="outline"
          onClick={() => w.endSession(w.sessionID)}
        >
          End session
        </Button>
      </>
    )

  if (w.driving)
    return bar(
      "border-b-primary/30 bg-primary/8",
      <>
        <HandIcon className="size-3.5 shrink-0 text-primary" weight="fill" />
        <span className="min-w-0 flex-1">
          <span className="font-medium">You're driving.</span>{" "}
          {!panel && `${w.name}'s browser calls wait until you hand back.`}
        </span>
        <Button
          size="xs"
          disabled={w.busy}
          onClick={() => void w.setWheel(false)}
        >
          Hand back to {w.name}
        </Button>
      </>
    )

  const waiting = w.asksHere.length > 0
  return bar(
    waiting ? "bg-(--st-waiting)/10" : "",
    <>
      <span className="relative inline-flex size-4 shrink-0 items-center justify-center text-[0.75rem] leading-none">
        {w.avatar}
        {waiting && (
          <span className="absolute -right-0.5 -bottom-0.5 size-1.5 rounded-full bg-(--st-waiting) ring-1 ring-background" />
        )}
      </span>
      <span className="min-w-0 flex-1 truncate">
        {waiting ? (
          <>
            <span className="font-medium">{w.name} is waiting for you</span>
            {!panel && (
              <span className="text-muted-foreground">
                {" "}
                · answer on the page
              </span>
            )}
          </>
        ) : w.st?.driver ? (
          <>
            <span className="font-medium">{w.name} drives</span>
            <span className="text-muted-foreground">
              {" "}
              · last from <span className="font-mono">{w.st.driver}</span>
            </span>
          </>
        ) : (
          <span className="text-muted-foreground">
            {w.name} isn't using the browser.
            {!panel && " It sleeps after a while unused."}
          </span>
        )}
      </span>
      <Button
        size="xs"
        variant="outline"
        disabled={w.busy}
        onClick={() => void w.setWheel(true)}
      >
        <HandIcon />
        Take the wheel
      </Button>
    </>
  )
}

/** A step of the log in words: `Ada clicked “Send”`. */
export function stepWords(a: BrowserAction, name: string) {
  const who = a.actor === "user" ? "You" : name
  return [who, a.action, a.target].filter(Boolean).join(" ")
}

/** The question an agent asks, on the page it is about. */
function AskCard({ approval, name }: { approval: Approval; name: string }) {
  const act = approval.browserAction
  const cred = approval.browserCredential
  const Icon = cred ? KeyIcon : CursorClickIcon
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
        <KindChip>{cred ? "Login" : "Page action"}</KindChip>
        <span className="ml-auto text-muted-foreground">
          {formatAge(approval.createdAt)}
        </span>
      </div>
      <p className="text-[0.8125rem]">
        {cred ? (
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
      {cred ? (
        <BrowserCredentialDecision approval={approval} />
      ) : (
        <BrowserActionDecision approval={approval} />
      )}
    </div>
  )
}

/** A step of the log, as the page looked then. */
function Replayed({ w, replay }: { w: Win; replay: BrowserReplay }) {
  const shot = replayShot(replay)
  return shot ? (
    <img
      src={screenshotURL(w.env, w.teammate, shot)}
      alt={`The page after ${shot.action}`}
      className={cn(
        "mx-auto w-full max-w-6xl grayscale-[.35]",
        w.size === "full" && "rounded-md shadow-sm ring-1 ring-border"
      )}
    />
  ) : (
    <Notice title="No picture of this step">
      Steps keep a screenshot when they change the page.
    </Notice>
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
  onWake,
}: {
  w: Win
  waking: boolean
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
    <div className="relative flex min-h-80 flex-1 items-center justify-center">
      {last && (
        <img
          aria-hidden
          alt=""
          src={screenshotURL(w.env, w.teammate, last)}
          className="pointer-events-none absolute inset-x-0 top-0 mx-auto w-full max-w-6xl rounded-md opacity-25 blur-[1px] grayscale"
        />
      )}
      <div className="relative m-4 grid w-full max-w-sm gap-3 rounded-xl border bg-background/95 p-5 text-center shadow-lg">
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
                "mx-auto inline-flex size-9 items-center justify-center rounded-lg",
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
              className="flex gap-1.5"
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
                className="h-8 min-w-0 flex-1 rounded-md border bg-background px-2 font-mono text-xs outline-none placeholder:font-sans focus:border-ring focus:ring-2 focus:ring-ring/30"
              />
              <Button size="sm" type="submit">
                {url.trim() ? "Open" : "Wake up"}
              </Button>
            </form>
            {last?.url && (
              <p className="text-[0.6875rem] text-muted-foreground">
                Last page <span className="font-mono">{hostOf(last.url)}</span>
              </p>
            )}
          </>
        )}
      </div>
    </div>
  )
}
