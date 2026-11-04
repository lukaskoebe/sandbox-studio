import {
  createContext,
  memo,
  useContext,
  useEffect,
  useMemo,
  useRef,
  useState,
} from "react"
import { Link } from "@tanstack/react-router"
import { CaretDownIcon, CaretRightIcon } from "@phosphor-icons/react"
import { $api, type Sandbox } from "@/lib/api/client"
import type { Chat } from "@/lib/chats"
import {
  buildBranches,
  buildFlags,
  buildMarks,
  packLanes,
  pulses,
  sessionsOf,
  type Block,
  type Branch,
  type BranchKind,
  type Flag,
  type Lane,
  type Mark,
  type Pulse,
} from "@/lib/session-lines"
import { useStyleName } from "@/lib/theme"
import { cn } from "@/lib/utils"

/** The windows Home can show, in hours. */
const windows = [0.5, 1, 2, 8, 24] as const
type Hours = (typeof windows)[number]
const windowKey = "studio.lines-window"
const windowLabel = (h: Hours) => (h < 1 ? `${h * 60}m` : `${h}h`)
const windowWords = (h: Hours) =>
  h < 1 ? `${h * 60} minutes` : h === 1 ? "hour" : `${h} hours`

// Pixels of a sandbox's track.
const LANE = 38 // from one lane to the next
const TOP = 26 // above the highest lane's label
const BOTTOM = 14
const CURVE = 20 // how far a branch runs to reach its lane
const RIGHT = 14 // from now to the track's right edge
const FOLDED = 34 // a folded line's height
const PULSE = 7 // the most a branch swells either side of its lane
const BUCKET = 6 // the fewest pixels one width of a branch spans
const LABEL = 11 // from a lane up to its label's baseline

/**
 * How the lines are drawn in the style picked: the width of a label character, the rounding
 * of bars, line ends, and whether a branch's width is stepped (pixel) or smooth (quiet). scale
 * is the busiest minute of each kind in the window, and bucket the ms one width spans, counted
 * from the epoch so the stretches stay put as the window moves.
 */
type Look = {
  quiet: boolean
  char: number
  rx: number
  cap: "butt" | "round"
  scale: Record<BranchKind, number>
  bucket: number
}
const LookContext = createContext<Look>({
  quiet: false,
  char: 6.1,
  rx: 0,
  cap: "butt",
  scale: { chat: 1, terminal: 1, browser: 1, preview: 1 },
  bucket: 60_000,
})
const useLook = () => useContext(LookContext)

/** What a branch's activity counts, for its tooltip. */
const counted: Record<BranchKind, (n: number, errors: number) => string> = {
  chat: (n, e) =>
    `${n} tool call${n === 1 ? "" : "s"}${e ? `, ${e} failed` : ""}`,
  terminal: (n) =>
    `printed for ${n < 60 ? `${n} s` : `${Math.round(n / 60)} min`}`,
  browser: (n, e) =>
    `${n} action${n === 1 ? "" : "s"}${e ? `, ${e} failed or denied` : ""}`,
  preview: (n, e) =>
    `${n} request${n === 1 ? "" : "s"}${e ? `, ${e} answered 5xx` : ""}`,
}

/** A flag's color. */
function flagColor(f: Flag) {
  if (f.bad || f.kind === "failed") return "var(--st-failed)"
  switch (f.kind) {
    case "asked":
      return "var(--st-waiting-fg)"
    case "blocked":
      return "var(--line-blocked)"
    default:
      return "var(--acc-git)"
  }
}

/** A branch's stroke, by kind; the user's own sessions have their own. */
const stroke: Record<BranchKind, string> = {
  chat: "var(--acc-chat)",
  terminal: "var(--line-terminal)",
  browser: "var(--acc-browser)",
  preview: "var(--line-preview)",
}
const blockFill: Record<Block["tone"], string> = {
  turn: "color-mix(in oklch, var(--acc-chat) 55%, transparent)",
  live: "color-mix(in oklch, var(--st-running) 70%, transparent)",
  waiting: "color-mix(in oklch, var(--st-waiting) 55%, transparent)",
  stopped: "var(--st-stopped)",
  run: "color-mix(in oklch, var(--line-terminal) 45%, transparent)",
  drive: "var(--line-you)",
}
const glyph: Record<BranchKind, string> = {
  chat: "◧",
  terminal: "▭",
  browser: "◎",
  preview: "⇢",
}

function clock(t: number) {
  return new Date(t).toLocaleTimeString([], {
    hour: "2-digit",
    minute: "2-digit",
  })
}

/** The window the user picked last, in this browser. */
function useWindow() {
  const [hours, setHours] = useState<Hours>(() => {
    try {
      const v = Number(localStorage.getItem(windowKey))
      return windows.find((w) => w === v) ?? 2
    } catch {
      return 2
    }
  })
  const pick = (h: Hours) => {
    setHours(h)
    try {
      localStorage.setItem(windowKey, String(h))
    } catch {
      // The window just starts at two hours again.
    }
  }
  return [hours, pick] as const
}

/** The time, a little behind: the lines move on twice a minute. */
function useNow() {
  const [now, setNow] = useState(() => Date.now())
  useEffect(() => {
    const id = setInterval(() => setNow(Date.now()), 30_000)
    return () => clearInterval(id)
  }, [])
  return now
}

/** The width of an element, as it changes. */
function useWidth() {
  const ref = useRef<HTMLDivElement>(null)
  const [width, setWidth] = useState(0)
  useEffect(() => {
    const el = ref.current
    if (!el) return
    const ro = new ResizeObserver(([e]) =>
      setWidth(Math.floor(e.contentRect.width))
    )
    ro.observe(el)
    return () => ro.disconnect()
  }, [])
  return [ref, width] as const
}

/** The ticks of a window, on the local clock: every 5 minutes for half an hour, 10 for an hour,
 * half an hour for two hours, then every two or four. */
function ticks(since: number, until: number) {
  const span = until - since
  const step =
    span <= 35 * 60e3
      ? 5 * 60e3
      : span <= 70 * 60e3
        ? 10 * 60e3
        : (span <= 3 * 3600e3 ? 0.5 : span <= 9 * 3600e3 ? 2 : 4) * 3600e3
  const day = new Date(since)
  day.setHours(0, 0, 0, 0)
  let t = day.getTime()
  while (t <= since) t += step
  const out: number[] = []
  for (; t < until - step / 4; t += step) out.push(t)
  return out
}

type Row = {
  sandbox: Sandbox
  depth: number
  branches: Branch[]
  marks: Mark[]
  flags: Flag[]
  born?: number
  last?: number
  /** Its sessions, a group's previews each counted. */
  sessions: number
  open: number
}

/**
 * Home's sandboxes, one line each over the last hours. A session branches off its sandbox's
 * line as it opens and rejoins it as it closes, so what is still out on a branch still runs:
 * chats above the line with their turns on them, terminals with the programs that ran,
 * browser sessions with the user's turns, and ports below. Forks start a line of their own
 * under their source. A sandbox with nothing open folds into one line with a dot per session.
 */
export function SessionLines({
  env,
  sandboxes,
  chats,
  waiting,
}: {
  env: string
  sandboxes: Sandbox[]
  chats: Chat[]
  /** Sandboxes with an approval waiting. */
  waiting: ReadonlySet<string | undefined>
}) {
  const [hours, setHours] = useWindow()
  const now = useNow()
  const [axisRef, width] = useWidth()
  const lines = $api.useQuery(
    "get",
    "/api/environments/{env}/sessions",
    {
      params: {
        path: { env },
        query: hours < 2 ? { minutes: hours * 60 } : { hours },
      },
    },
    { refetchInterval: 60_000 }
  )
  const teammates = $api.useQuery("get", "/api/environments/{env}/teammates", {
    params: { path: { env } },
  })
  const [unfolded, setUnfolded] = useState<Record<string, boolean>>({})

  const since = now - hours * 3600e3
  const until = now
  const right = Math.max(0, width - RIGHT)
  const x = useMemo(
    () => (t: number) =>
      Math.max(0, Math.min(right, ((t - since) / (until - since)) * right)),
    [since, until, right]
  )

  const rows = useMemo(() => {
    const names = new Map((teammates.data ?? []).map((t) => [t.id, t.name]))
    const sbNames = new Map(sandboxes.map((s) => [s.id, s.name]))
    const waits = new Set([...waiting].filter((w): w is string => !!w))
    const branches = lines.data
      ? buildBranches(lines.data, {
          now,
          teammateName: (id) => names.get(id),
          waiting: waits,
        })
      : new Map<string, Branch[]>()
    const flags = buildFlags(lines.data?.flags ?? [])
    const { marks, born } = buildMarks(
      lines.data?.events ?? [],
      new Map(sandboxes.map((s) => [s.id, s.forkOf])),
      (id) => sbNames.get(id) ?? "a fork"
    )
    const row = (s: Sandbox, depth: number): Row => {
      const bs = branches.get(s.id) ?? []
      const ms = marks.get(s.id) ?? []
      const times = [
        ...bs.flatMap((b) => [b.start, b.end ?? now]),
        ...ms.map((m) => m.at),
      ]
      return {
        sandbox: s,
        depth,
        branches: bs,
        marks: ms,
        flags: flags.get(s.id) ?? [],
        born: born.get(s.id),
        last: times.length ? Math.max(...times) : undefined,
        sessions: bs.flatMap(sessionsOf).length,
        open: bs.flatMap(sessionsOf).filter((b) => b.end === undefined).length,
      }
    }
    const shown = (r: Row) =>
      r.sandbox.status === "running" ||
      r.branches.length > 0 ||
      r.marks.length > 0 ||
      r.flags.length > 0
    const kids = new Map<string, Sandbox[]>()
    const ids = new Set(sandboxes.map((s) => s.id))
    const roots: Sandbox[] = []
    for (const s of sandboxes) {
      if (s.forkOf && ids.has(s.forkOf)) {
        const list = kids.get(s.forkOf)
        if (list) list.push(s)
        else kids.set(s.forkOf, [s])
      } else roots.push(s)
    }
    const order = (a: Row, b: Row) =>
      Number(b.open > 0) - Number(a.open > 0) || (b.last ?? 0) - (a.last ?? 0)
    const out: Row[] = []
    const walk = (list: Sandbox[], depth: number) => {
      for (const r of list.map((s) => row(s, depth)).sort(order)) {
        const children = kids.get(r.sandbox.id) ?? []
        const before = out.length
        walk(children, depth + 1)
        const sub = out.splice(before)
        if (shown(r) || sub.length) out.push(r, ...sub)
      }
    }
    walk(roots, 0)
    return { rows: out, quiet: sandboxes.length - out.length }
  }, [lines.data, sandboxes, teammates.data, waiting, now])

  const working = useMemo(() => {
    const names = new Map((teammates.data ?? []).map((t) => [t.id, t.name]))
    const out = new Map<string, string>()
    for (const c of chats)
      if (c.state === "running")
        out.set(c.sandboxId, names.get(c.teammateId) ?? "A teammate")
    return out
  }, [chats, teammates.data])

  const style = useStyleName()
  const look = useMemo<Look>(() => {
    const scale: Record<BranchKind, number> = {
      chat: 1,
      terminal: 1,
      browser: 1,
      preview: 1,
    }
    for (const a of lines.data?.activity ?? [])
      scale[a.kind] = Math.max(scale[a.kind], a.count)
    // One width spans whole minutes, at least BUCKET pixels.
    const perPixel = right > 0 ? (hours * 3600e3) / right : 60_000
    const quiet = style === "quiet"
    return {
      quiet,
      char: quiet ? 5.5 : 6.1,
      rx: quiet ? 2 : 0,
      cap: quiet ? "round" : "butt",
      scale,
      bucket: Math.max(1, Math.ceil((BUCKET * perPixel) / 60_000)) * 60_000,
    }
  }, [lines.data, style, right, hours])

  return (
    <LookContext.Provider value={look}>
      <section aria-labelledby="session-lines" className="grid min-w-0 gap-2.5">
        <div className="flex min-w-0 flex-wrap items-baseline gap-x-3 gap-y-1">
          <h2
            id="session-lines"
            className="section-label text-muted-foreground"
          >
            Sandboxes
          </h2>
          <span className="text-caption text-muted-foreground">
            sessions branch off as they open and rejoin as they close
          </span>
          <div
            role="group"
            aria-label="How far back"
            className="ml-auto flex gap-0.5 font-control text-caption"
          >
            {windows.map((h) => (
              <button
                key={h}
                type="button"
                aria-pressed={hours === h}
                onClick={() => setHours(h)}
                className={cn(
                  "rounded-sm px-1.5 py-0.5 text-muted-foreground hover:bg-muted hover:text-foreground",
                  hours === h && "bg-muted text-foreground"
                )}
              >
                {windowLabel(h)}
              </button>
            ))}
          </div>
        </div>
        <div className="min-w-0 overflow-hidden rounded-(--radius) border bg-card">
          <div className="grid border-b sm:grid-cols-[11rem_minmax(0,1fr)]">
            <div className="hidden px-3 py-1 sm:block" />
            <div
              ref={axisRef}
              aria-hidden
              className="relative h-6 lines-font text-meta text-muted-foreground sm:border-l"
            >
              {width > 0 &&
                ticks(since, until).map((t) => (
                  <span
                    key={t}
                    className="absolute top-1 -translate-x-1/2"
                    style={{ left: x(t) }}
                  >
                    {clock(t)}
                  </span>
                ))}
              {width > 0 && (
                <span
                  className="absolute top-1 -translate-x-full text-(--line-you)"
                  style={{ left: right + RIGHT - 2 }}
                >
                  now
                </span>
              )}
            </div>
          </div>
          {rows.rows.length === 0 ? (
            <p className="px-3 py-3 text-xs text-muted-foreground">
              {lines.isPending
                ? "Loading…"
                : `No sandbox ran in the last ${windowWords(hours)}.`}
            </p>
          ) : (
            rows.rows.map((r) => (
              <SandboxRow
                key={r.sandbox.id}
                env={env}
                row={r}
                width={width}
                x={x}
                right={right}
                since={since}
                now={now}
                folded={!(unfolded[r.sandbox.id] ?? r.open > 0)}
                onFold={(fold) =>
                  setUnfolded((u) => ({ ...u, [r.sandbox.id]: !fold }))
                }
                waiting={waiting.has(r.sandbox.id)}
                working={working.get(r.sandbox.id)}
              />
            ))
          )}
        </div>
        <div className="flex flex-wrap items-center gap-x-4 gap-y-1 lines-font text-meta text-muted-foreground">
          <Legend color={stroke.chat}>chat</Legend>
          <Legend color={stroke.terminal}>an agent's terminal</Legend>
          <Legend color="var(--line-you)">yours</Legend>
          <Legend color={stroke.browser}>browser session</Legend>
          <Legend color={stroke.preview} dotted>
            preview
          </Legend>
          <span title="A branch widens with its tool calls, output, actions or requests">
            wider: busier
          </span>
          <span className="text-(--st-failed)">✗ failed</span>
          <span className="text-(--st-waiting-fg)">⏸ asked you</span>
          <span className="text-(--acc-git)">▲ push</span>
          <span className="text-(--line-blocked)">⚠ blocked</span>
          {rows.quiet > 0 && (
            <span className="ml-auto">
              {rows.quiet === 1
                ? "1 more sandbox had nothing open"
                : `${rows.quiet} more sandboxes had nothing open`}
            </span>
          )}
        </div>
      </section>
    </LookContext.Provider>
  )
}

function Legend({
  color,
  dotted,
  children,
}: {
  color: string
  dotted?: boolean
  children: string
}) {
  return (
    <span className="flex items-center gap-1.5">
      <span
        aria-hidden
        className="inline-block w-4 border-t-2"
        style={{ borderColor: color, borderStyle: dotted ? "dotted" : "solid" }}
      />
      {children}
    </span>
  )
}

const SandboxRow = memo(function SandboxLine({
  env,
  row,
  width,
  x,
  right,
  since,
  now,
  folded,
  onFold,
  waiting,
  working,
}: {
  env: string
  row: Row
  width: number
  x: (t: number) => number
  right: number
  since: number
  now: number
  folded: boolean
  onFold: (fold: boolean) => void
  waiting: boolean
  working?: string
}) {
  const s = row.sandbox
  const fork = row.depth > 0
  const closed = row.sessions - row.open
  const status = waiting
    ? "⏸ waiting on you"
    : working
      ? `▶ ${working} is working`
      : s.status === "running"
        ? "running"
        : s.status
  const counts =
    row.branches.length === 0
      ? "nothing opened"
      : [row.open && `${row.open} open`, closed && `${closed} closed`]
          .filter(Boolean)
          .join(" · ")
  return (
    <div className="grid border-b last:border-b-0 sm:grid-cols-[11rem_minmax(0,1fr)]">
      <div
        className={cn(
          "flex min-w-0 items-start gap-1 px-3 py-2",
          waiting && "bg-(--st-waiting)/8"
        )}
      >
        <button
          type="button"
          aria-expanded={!folded}
          aria-label={folded ? `Unfold ${s.name}` : `Fold ${s.name}`}
          title={folded ? "Show its sessions" : "Fold into one line"}
          onClick={() => onFold(!folded)}
          className="mt-0.5 grid size-4 shrink-0 place-items-center rounded-sm text-muted-foreground hover:bg-muted hover:text-foreground"
        >
          {folded ? (
            <CaretRightIcon className="size-3" />
          ) : (
            <CaretDownIcon className="size-3" />
          )}
        </button>
        <div className="grid min-w-0 gap-0.5">
          <Link
            to="/e/$env/sandboxes/$id"
            params={{ env, id: s.id }}
            className={cn(
              "truncate font-heading text-xs font-semibold hover:underline",
              fork && "text-(--acc-git)"
            )}
            style={{ paddingLeft: Math.min(row.depth, 3) * 8 }}
          >
            {fork && "↳ "}
            {s.name}
          </Link>
          <span
            className={cn(
              "truncate lines-font text-meta text-muted-foreground",
              waiting && "text-(--st-waiting-fg)"
            )}
          >
            {status}
          </span>
          {!folded && (
            <span className="truncate lines-font text-meta text-muted-foreground">
              {counts}
            </span>
          )}
          {!folded &&
            row.marks
              .filter((m) => m.kind === "fork")
              .map((m) => (
                <span
                  key={m.at + m.label}
                  className="truncate lines-font text-meta text-(--acc-git)"
                >
                  ◆ {clock(m.at)} {m.label}
                </span>
              ))}
        </div>
      </div>
      <div className="min-w-0 sm:border-l">
        {width > 0 &&
          (folded ? (
            <FoldedTrack
              row={row}
              width={width}
              x={x}
              right={right}
              since={since}
              now={now}
            />
          ) : (
            <Track
              env={env}
              row={row}
              width={width}
              x={x}
              right={right}
              since={since}
              now={now}
            />
          ))}
      </div>
    </div>
  )
})

type TrackProps = {
  row: Row
  width: number
  x: (t: number) => number
  right: number
  since: number
  now: number
}

/** The sandbox's own line: from its create or fork to now, dashed once it stopped. */
function MainLine({
  row,
  x,
  right,
  y,
}: Pick<TrackProps, "row" | "x" | "right"> & { y: number }) {
  const look = useLook()
  const from = row.born !== undefined ? x(row.born) : 0
  const running = row.sandbox.status === "running"
  const color = row.depth > 0 ? "var(--acc-git)" : "var(--line-main)"
  const solidTo = running ? right : row.last !== undefined ? x(row.last) : from
  return (
    <>
      {solidTo > from && (
        <line
          x1={from}
          y1={y}
          x2={solidTo}
          y2={y}
          stroke={color}
          strokeWidth={3}
          strokeLinecap={look.cap}
        />
      )}
      {solidTo < right && (
        <line
          x1={solidTo}
          y1={y}
          x2={right}
          y2={y}
          stroke={color}
          strokeWidth={2}
          strokeDasharray="3 5"
          strokeLinecap={look.cap}
          opacity={0.7}
        />
      )}
      {row.born !== undefined && row.depth > 0 && (
        <rect
          x={from - 5}
          y={y - 5}
          width={10}
          height={10}
          rx={look.rx}
          fill="var(--acc-git)"
          transform={`rotate(45 ${from} ${y})`}
        >
          <title>{`forked at ${clock(row.born)}`}</title>
        </rect>
      )}
      {row.marks.map((m) => (
        <MarkGlyph key={m.at + m.label} mark={m} x={x(m.at)} y={y} />
      ))}
      <line
        x1={right}
        y1={0}
        x2={right}
        y2="100%"
        stroke="var(--line-you)"
        strokeOpacity={0.45}
      />
    </>
  )
}

function MarkGlyph({ mark, x, y }: { mark: Mark; x: number; y: number }) {
  const { rx } = useLook()
  const title = <title>{`${clock(mark.at)} · ${mark.label}`}</title>
  switch (mark.kind) {
    case "fork":
      return (
        <rect
          x={x - 5}
          y={y - 5}
          width={10}
          height={10}
          rx={rx}
          fill="var(--acc-git)"
          transform={`rotate(45 ${x} ${y})`}
        >
          {title}
        </rect>
      )
    case "merge":
      return (
        <rect
          x={x - 5}
          y={y - 5}
          width={10}
          height={10}
          rx={rx}
          fill="var(--acc-git)"
        >
          {title}
        </rect>
      )
    case "checkpoint":
      return (
        <rect
          x={x - 5}
          y={y - 5}
          width={10}
          height={10}
          rx={rx}
          fill="var(--card)"
          stroke="var(--muted-foreground)"
          strokeWidth={2}
        >
          {title}
        </rect>
      )
    default:
      return (
        <circle cx={x} cy={y} r={4} fill="var(--muted-foreground)">
          {title}
        </circle>
      )
  }
}

function Grid({
  since,
  now,
  x,
}: {
  since: number
  now: number
  x: (t: number) => number
}) {
  return (
    <g stroke="var(--line-grid)">
      {ticks(since, now).map((t) => (
        <line key={t} x1={x(t)} y1={0} x2={x(t)} y2="100%" />
      ))}
    </g>
  )
}

/** How far a stretch's count swells its branch, either side: by the root of its share of the
 * window's busiest minute, so a little work still shows. */
function swell(p: Pulse, kind: BranchKind, look: Look) {
  if (!p.count) return 0
  const full = look.scale[kind] * (look.bucket / 60_000)
  return Math.max(1.5, PULSE * Math.sqrt(Math.min(1, p.count / full)))
}

/**
 * A branch's width along its lane from a to b: stepped where pixel's corners are square,
 * smooth where quiet's are round. Each stretch that saw work has its tooltip, and errors are a
 * red tick.
 */
function Envelope({
  branch: b,
  a,
  b: to,
  y,
  x,
  color,
}: {
  branch: Branch
  a: number
  b: number
  y: number
  x: (t: number) => number
  color: string
}) {
  const look = useLook()
  if (!b.beats.length || to - a < 2) return null
  const t0 = b.start
  const t1 = b.end ?? Date.now()
  const stretches = pulses(b.beats, 0, look.bucket, t0, t1)
    .map((p) => ({
      p,
      from: Math.max(a, x(p.from)),
      to: Math.min(to, x(p.to)),
      h: swell(p, b.kind, look),
    }))
    .filter((s) => s.to > s.from)
  if (!stretches.some((s) => s.h > 0)) return null
  let top: string
  let bottom: string
  if (look.quiet) {
    // Through each stretch's middle, rounded off between them.
    const pts = [
      [a, 0],
      ...stretches.map((s) => [(s.from + s.to) / 2, s.h]),
      [to, 0],
    ]
    const through = (list: number[][], sign: number) =>
      list
        .slice(1)
        .map(([px, h], i) => {
          const [qx, qh] = list[i]
          const mx = (qx + px) / 2
          return `C ${mx} ${y + sign * qh}, ${mx} ${y + sign * h}, ${px} ${y + sign * h}`
        })
        .join(" ")
    top = `M ${a} ${y} ${through(pts, -1)}`
    bottom = through([...pts].reverse(), 1)
  } else {
    top =
      `M ${a} ${y} ` +
      stretches
        .map((s) => `L ${s.from} ${y - s.h} L ${s.to} ${y - s.h}`)
        .join(" ") +
      ` L ${to} ${y}`
    bottom = [...stretches]
      .reverse()
      .map((s) => `L ${s.to} ${y + s.h} L ${s.from} ${y + s.h}`)
      .join(" ")
  }
  return (
    <g>
      <path
        d={`${top} ${bottom} Z`}
        fill={color}
        fillOpacity={0.45}
        strokeLinejoin="round"
      />
      {stretches.map(
        (s) =>
          s.p.errors > 0 && (
            <rect
              key={`e${s.p.from}`}
              x={(s.from + s.to) / 2 - 1}
              y={y - s.h - 2}
              width={2}
              height={2 * s.h + 4}
              rx={look.rx ? 1 : 0}
              fill="var(--st-failed)"
            />
          )
      )}
      {stretches.map(
        (s) =>
          s.p.count > 0 && (
            <rect
              key={s.p.from}
              x={s.from}
              y={y - PULSE - 2}
              width={s.to - s.from}
              height={2 * PULSE + 4}
              fill="transparent"
            >
              <title>
                {`${clock(s.p.from)}${look.bucket > 60_000 ? `–${clock(s.p.to)}` : ""} · ${counted[b.kind](s.p.count, s.p.errors)}`}
              </title>
            </rect>
          )
      )}
    </g>
  )
}

/** A folded sandbox: its line with a dot where each session opened, swelling with their work,
 * and a tick per flag. */
function FoldedTrack({ row, width, x, right, since, now }: TrackProps) {
  const y = FOLDED / 2
  const sessions = row.branches.flatMap(sessionsOf)
  return (
    <svg
      width={width}
      height={FOLDED}
      role="img"
      aria-label={`${row.sandbox.name}, folded: ${row.sessions} sessions`}
      className="block"
    >
      <Grid since={since} now={now} x={x} />
      {row.branches.map((b) => (
        <Envelope
          key={`w${b.id}`}
          branch={b}
          a={b.start < since ? 0 : x(b.start)}
          b={b.end === undefined ? right : x(b.end)}
          y={y}
          x={x}
          color={b.yours ? "var(--line-you)" : stroke[b.kind]}
        />
      ))}
      <MainLine row={row} x={x} right={right} y={y} />
      {sessions.map((b) => (
        <circle
          key={b.id}
          cx={x(b.start)}
          cy={y}
          r={4}
          fill={b.yours ? "var(--line-you)" : stroke[b.kind]}
        >
          <title>{`${clock(b.start)} · ${branchLabel(b)}`}</title>
        </circle>
      ))}
      {row.flags
        .filter((f) => f.at >= since)
        .map((f, i) => (
          <g key={`f${i}`}>
            <title>{`${clock(f.at)} · ${f.detail}`}</title>
            <line
              x1={x(f.at)}
              y1={y - 11}
              x2={x(f.at)}
              y2={y + 11}
              stroke={flagColor(f)}
              strokeWidth={2}
            />
            <rect
              x={x(f.at) - 4}
              y={y - 12}
              width={8}
              height={24}
              fill="transparent"
            />
          </g>
        ))}
    </svg>
  )
}

/** What a branch's label says. */
function branchLabel(b: Branch) {
  switch (b.kind) {
    case "chat":
      return `chat · ${b.who ?? "a teammate"} · ${b.title}`
    case "terminal":
      return `${b.title} · ${b.yours ? "you" : `${b.who}'s terminal`}`
    case "browser":
      return `browser · ${b.who ? `${b.who}'s session` : "session"} · ${b.title}`
    case "preview":
      return b.group
        ? `${b.title} · ${[...new Set(b.group.map((p) => p.title))].join(" ")}`
        : `preview ${b.title}`
  }
}

/** The lanes of a side: terminals, then browser sessions, then previews outermost. */
function lanesOf(
  branches: Branch[],
  kinds: BranchKind[],
  x: (t: number) => number,
  right: number
) {
  return kinds.flatMap((k) =>
    packLanes(
      branches.filter((b) => b.kind === k),
      x,
      right,
      CURVE * 3
    )
  )
}

/** Where a branch's label goes and how much of it fits. */
type Label = { x: number; shown: string }

/**
 * A branch's label starts where its branch reaches the lane, or further left, back to where
 * the lane's previous branch ended, when it doesn't fit before the next one.
 */
function labelOf(
  b: Branch,
  x: (t: number) => number,
  since: number,
  after: number,
  until: number,
  char: number
): Label {
  const before = b.start < since
  const x0 = before ? 0 : x(b.start)
  const text =
    (before ? `◂ ${clock(b.start)} · ` : "") +
    `${glyph[b.kind]} ${branchLabel(b)}`
  const natural = before ? 4 : x0 + CURVE + 4
  const labelX = Math.max(
    Math.min(natural, until - 10 - text.length * char),
    Math.min(natural, after + 8)
  )
  const room = Math.max(0, until - labelX - 10)
  const max = Math.floor(room / char)
  const shown =
    max <= 1 ? "" : text.length > max ? text.slice(0, max - 1) + "…" : text
  return { x: labelX, shown }
}

/**
 * The flags of a sandbox: each on its chat's branch, or on the sandbox's line, with a tick and
 * its words where they fit beside the labels already there. A question also draws its wait,
 * dashed, until it was answered.
 */
function Flags({
  flags,
  x,
  since,
  now,
  right,
  place,
  taken,
}: {
  flags: Flag[]
  x: (t: number) => number
  since: number
  now: number
  right: number
  /** The y of the lane a flag goes on, and of its words' baseline. */
  place: (f: Flag) => { y: number; row: number }
  /** What each row of words already holds, by baseline. */
  taken: Map<number, [number, number][]>
}) {
  const look = useLook()
  const out = []
  for (const [i, f] of flags.entries()) {
    const end = f.kind === "asked" ? (f.until ?? now) : f.at
    if (end < since) continue
    const { y, row } = place(f)
    const fx = x(f.at)
    const color = flagColor(f)
    // The words run from the tick to whatever holds the row next: the lane's label row, or
    // the row above it when they don't fit there.
    const start = fx + 3
    const room = (r: number) => {
      const held = taken.get(r) ?? []
      if (held.some(([a, b]) => start >= a - 2 && start < b + 4)) return 0
      const next = Math.min(
        right,
        ...held.filter(([a]) => a > start).map(([a]) => a - 6)
      )
      return Math.max(0, Math.floor((next - start) / look.char))
    }
    let at = row
    let max = room(row)
    if (max < Math.min(f.text.length, 12) && room(row - 12) > max) {
      at = row - 12
      max = room(at)
    }
    const shown =
      f.text.length <= max
        ? f.text
        : max >= 8
          ? f.text.slice(0, max - 1) + "…"
          : ""
    if (shown) {
      const held = taken.get(at) ?? []
      held.push([start, start + shown.length * look.char])
      taken.set(at, held)
    }
    out.push(
      <g key={i}>
        <title>{`${clock(f.at)} · ${f.detail}`}</title>
        {f.kind === "asked" && <Wait x0={fx} x1={x(end)} y={y} />}
        <Tick x={fx} y={y} row={at} color={color} />
        {shown && (
          <text
            x={start}
            y={at}
            className="lines-font text-meta"
            style={{ fill: color }}
          >
            {shown}
          </text>
        )}
        <rect
          x={fx - 3}
          y={at - 10}
          width={Math.max(6, shown.length * look.char + 6)}
          height={y - at + 12}
          fill="transparent"
        />
      </g>
    )
  }
  return <>{out}</>
}

/** A flag's pole: from its lane up beside its words. */
function Tick({
  x,
  y,
  row,
  color,
}: {
  x: number
  y: number
  row: number
  color: string
}) {
  const { cap } = useLook()
  return (
    <line
      x1={x}
      y1={y - 2}
      x2={x}
      y2={row - 9}
      stroke={color}
      strokeWidth={1.5}
      strokeLinecap={cap}
    />
  )
}

/** A question's wait on its lane, until it was answered. */
function Wait({ x0, x1, y }: { x0: number; x1: number; y: number }) {
  const { cap } = useLook()
  if (x1 - x0 < 2) return null
  return (
    <line
      x1={x0}
      y1={y}
      x2={x1}
      y2={y}
      stroke="var(--st-waiting)"
      strokeWidth={3}
      strokeDasharray="3 2"
      strokeLinecap={cap}
    />
  )
}

function Track({
  env,
  row,
  width,
  x,
  right,
  since,
  now,
}: TrackProps & { env: string }) {
  const look = useLook()
  const up = lanesOf(row.branches, ["chat"], x, right)
  const down = lanesOf(
    row.branches,
    ["terminal", "browser", "preview"],
    x,
    right
  )
  const main = TOP + up.length * LANE + (up.length ? 6 : 0)
  const height = main + down.length * LANE + BOTTOM + (down.length ? 0 : 6)
  const lane = (side: "up" | "down", i: number) =>
    side === "up" ? main - (i + 1) * LANE + 4 : main + (i + 1) * LANE - 4
  // Each lane's labels, so that flags' words go where they don't cover one.
  const taken = new Map<number, [number, number][]>()
  const placed: { b: Branch; y: number }[] = []
  const draw = (lanes: Lane[], side: "up" | "down") =>
    lanes.flatMap((l, i) =>
      l.map((b, j) => {
        const y = lane(side, i)
        // A label runs between the branches before and after it in its lane.
        const label = labelOf(
          b,
          x,
          since,
          j > 0 ? x(l[j - 1].end ?? now) : 0,
          l[j + 1] ? x(l[j + 1].start) : right,
          look.char
        )
        if (label.shown) {
          const held = taken.get(y - LABEL) ?? []
          held.push([label.x, label.x + label.shown.length * look.char])
          taken.set(y - LABEL, held)
        }
        placed.push({ b, y })
        return (
          <BranchPath
            key={b.id}
            env={env}
            sandboxId={row.sandbox.id}
            branch={b}
            main={main}
            y={y}
            x={x}
            right={right}
            since={since}
            label={label}
          />
        )
      })
    )
  const branches = [...draw(up, "up"), ...draw(down, "down")]
  const place = (f: Flag) => {
    const on =
      f.chatId &&
      placed.find(
        ({ b }) =>
          b.link.to === "chat" &&
          b.link.chatId === f.chatId &&
          b.start - 60_000 <= f.at &&
          f.at <= (b.end ?? now) + 60_000
      )
    return on ? { y: on.y, row: on.y - LABEL } : { y: main, row: main - 8 }
  }
  return (
    <svg
      width={width}
      height={height}
      role="img"
      aria-label={`${row.sandbox.name} with its sessions branching off`}
      className="block"
    >
      <Grid since={since} now={now} x={x} />
      {branches}
      <MainLine row={row} x={x} right={right} y={main} />
      <Flags
        flags={row.flags}
        x={x}
        since={since}
        now={now}
        right={right}
        place={place}
        taken={taken}
      />
    </svg>
  )
}

function BranchPath({
  env,
  sandboxId,
  branch: b,
  main,
  y,
  x,
  right,
  since,
  label,
}: {
  env: string
  sandboxId: string
  branch: Branch
  main: number
  y: number
  x: (t: number) => number
  right: number
  since: number
  label: Label
}) {
  const look = useLook()
  const color = b.yours ? "var(--line-you)" : stroke[b.kind]
  const before = b.start < since
  const x0 = before ? 0 : x(b.start)
  const x1 = b.end === undefined ? right : Math.max(x(b.end), x0 + 2 * CURVE)
  const leave = before
    ? `M0 ${y}`
    : `M${x0} ${main} C ${x0 + 14} ${main}, ${x0 + 6} ${y}, ${x0 + CURVE} ${y}`
  const back =
    b.end === undefined
      ? `L ${right} ${y}`
      : `L ${x1 - CURVE} ${y} C ${x1 - 6} ${y}, ${x1 - 14} ${main}, ${x1} ${main}`
  const laneFrom = x0 + (before ? 0 : CURVE)
  const laneTo = b.end === undefined ? right : x1 - CURVE
  const when = `${clock(b.start)}–${b.end === undefined ? "now" : clock(b.end)}`
  const target =
    b.link.to === "chat"
      ? {
          to: "/e/$env/teammates/$id" as const,
          params: { env, id: b.link.teammateId },
          search: { thread: b.link.chatId },
        }
      : {
          to: "/e/$env/sandboxes/$id" as const,
          params: { env, id: sandboxId },
          search: {
            tab: b.link.tab,
            ...(b.link.term ? { term: b.link.term } : {}),
            ...(b.link.port ? { port: b.link.port } : {}),
          },
        }
  return (
    <Link
      {...target}
      aria-label={`${branchLabel(b)}, ${when}`}
      className="group/branch outline-none"
    >
      <title>
        {[
          `${branchLabel(b)}`,
          when,
          ...b.blocks
            .filter((k) => k.label)
            .map(
              (k) =>
                `› ${k.label}  ${clock(k.start)}–${k.end === undefined ? "now" : clock(k.end)}`
            ),
          ...(b.group ?? []).map(
            (p) =>
              `${p.title}  ${clock(p.start)}–${p.end === undefined ? "now" : clock(p.end)}`
          ),
        ].join("\n")}
      </title>
      <Envelope branch={b} a={laneFrom} b={laneTo} y={y} x={x} color={color} />
      <path
        d={`${leave} ${back}`}
        fill="none"
        stroke={color}
        strokeWidth={1.5}
        strokeDasharray={b.kind === "preview" ? "2 3" : undefined}
        strokeLinecap={look.cap}
        className="group-hover/branch:[stroke-width:2.5] group-focus-visible/branch:[stroke-width:2.5]"
      />
      {/* A wider stroke to point at. */}
      <path
        d={`${leave} ${back}`}
        fill="none"
        stroke="transparent"
        strokeWidth={12}
      />
      {b.blocks.map((k, i) => {
        const bx = Math.max(laneFrom, x(k.start))
        const bw = Math.max(3, (k.end === undefined ? right : x(k.end)) - bx)
        const max = Math.floor((bw - 4) / 5.5)
        return (
          <g key={i}>
            {k.tone === "drive" ? (
              // The user's turn in a browser, outlined around the branch.
              <rect
                x={bx}
                y={y - PULSE - 2}
                width={bw}
                height={2 * PULSE + 4}
                rx={look.rx + 1}
                fill="none"
                stroke="var(--line-you)"
              />
            ) : (
              <rect
                x={bx}
                y={y - 2}
                width={bw}
                height={4}
                rx={look.rx}
                fill={blockFill[k.tone]}
              />
            )}
            {k.label && bw > 40 && (
              <text
                x={bx + 2}
                y={y + PULSE + 9}
                className="pointer-events-none fill-muted-foreground lines-font text-[9px]"
              >
                {k.label.length > max
                  ? k.label.slice(0, Math.max(1, max - 1)) + "…"
                  : k.label}
              </text>
            )}
          </g>
        )
      })}
      {/* Where each preview of a group opened, and a ring where it closed. */}
      {b.group?.flatMap((p) => [
        p.start >= since && (
          <circle
            key={`${p.id}:open`}
            cx={Math.max(laneFrom, x(p.start))}
            cy={y}
            r={2.5}
            fill={color}
          />
        ),
        p.end !== undefined && b.end === undefined && (
          <circle
            key={`${p.id}:close`}
            cx={x(p.end)}
            cy={y}
            r={2.5}
            fill="var(--card)"
            stroke={color}
          />
        ),
      ])}
      {b.end !== undefined && (
        <circle
          cx={x1}
          cy={main}
          r={4}
          fill="var(--card)"
          stroke={color}
          strokeWidth={1.5}
        />
      )}
      {label.shown && (
        <text
          x={label.x}
          y={y - LABEL}
          className={cn(
            "lines-font text-meta",
            b.yours ? "fill-(--line-you)" : "fill-foreground",
            b.kind === "preview" && "fill-muted-foreground",
            b.end !== undefined && "fill-muted-foreground"
          )}
        >
          {label.shown}
        </text>
      )}
    </Link>
  )
}
