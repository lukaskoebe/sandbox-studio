import { memo, useEffect, useMemo, useRef, useState } from "react"
import { Link } from "@tanstack/react-router"
import { CaretDownIcon, CaretRightIcon } from "@phosphor-icons/react"
import { $api, type Sandbox } from "@/lib/api/client"
import type { Chat } from "@/lib/chats"
import {
  buildBranches,
  buildMarks,
  packLanes,
  type Block,
  type Branch,
  type BranchKind,
  type Lane,
  type Mark,
} from "@/lib/session-lines"
import { cn } from "@/lib/utils"

/** The windows Home can show, in hours. */
const windows = [2, 8, 24] as const
type Hours = (typeof windows)[number]
const windowKey = "studio.lines-window"

// Pixels of a sandbox's track.
const LANE = 28 // from one lane to the next
const TOP = 22 // above the highest lane's label
const BOTTOM = 12
const CURVE = 20 // how far a branch runs to reach its lane
const RIGHT = 14 // from now to the track's right edge
const CHAR = 6.1 // the width of a label character
const FOLDED = 34 // a folded line's height

/** A branch's stroke, by kind; the user's own sessions have their own. */
const stroke: Record<BranchKind, string> = {
  chat: "var(--acc-chat)",
  terminal: "var(--line-terminal)",
  browser: "var(--acc-browser)",
  preview: "var(--line-preview)",
}
const blockFill: Record<Block["tone"], string> = {
  turn: "color-mix(in oklch, var(--acc-chat) 55%, transparent)",
  live: "var(--st-running)",
  waiting: "var(--st-waiting)",
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

/** The ticks of a window, on the local clock: every half hour for two hours, then every two
 * or four. */
function ticks(since: number, until: number) {
  const span = until - since
  const step = (span <= 3 * 3600e3 ? 0.5 : span <= 9 * 3600e3 ? 2 : 4) * 3600e3
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
  born?: number
  last?: number
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
    { params: { path: { env }, query: { hours } } },
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
        born: born.get(s.id),
        last: times.length ? Math.max(...times) : undefined,
        open: bs.filter((b) => b.end === undefined).length,
      }
    }
    const shown = (r: Row) =>
      r.sandbox.status === "running" ||
      r.branches.length > 0 ||
      r.marks.length > 0
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

  return (
    <section aria-labelledby="session-lines" className="grid min-w-0 gap-2.5">
      <div className="flex min-w-0 flex-wrap items-baseline gap-x-3 gap-y-1">
        <h2 id="session-lines" className="section-label text-muted-foreground">
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
              {h}h
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
            className="relative h-6 font-mono text-meta text-muted-foreground sm:border-l"
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
              : `No sandbox ran in the last ${hours} hours.`}
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
      <div className="flex flex-wrap items-center gap-x-4 gap-y-1 font-mono text-meta text-muted-foreground">
        <Legend color={stroke.chat}>chat</Legend>
        <Legend color={stroke.terminal}>an agent's terminal</Legend>
        <Legend color="var(--line-you)">yours</Legend>
        <Legend color={stroke.browser}>browser session</Legend>
        <Legend color={stroke.preview} dotted>
          preview
        </Legend>
        {rows.quiet > 0 && (
          <span className="ml-auto">
            {rows.quiet === 1
              ? "1 more sandbox had nothing open"
              : `${rows.quiet} more sandboxes had nothing open`}
          </span>
        )}
      </div>
    </section>
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
  const closed = row.branches.length - row.open
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
              "truncate font-mono text-xs font-semibold hover:underline",
              fork && "text-(--acc-git)"
            )}
            style={{ paddingLeft: Math.min(row.depth, 3) * 8 }}
          >
            {fork && "↳ "}
            {s.name}
          </Link>
          <span
            className={cn(
              "truncate font-mono text-meta text-muted-foreground",
              waiting && "text-(--st-waiting-fg)"
            )}
          >
            {status}
          </span>
          {!folded && (
            <span className="truncate font-mono text-meta text-muted-foreground">
              {counts}
            </span>
          )}
          {!folded &&
            row.marks
              .filter((m) => m.kind === "fork")
              .map((m) => (
                <span
                  key={m.at + m.label}
                  className="truncate font-mono text-meta text-(--acc-git)"
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
          opacity={0.7}
        />
      )}
      {row.born !== undefined && row.depth > 0 && (
        <rect
          x={from - 5}
          y={y - 5}
          width={10}
          height={10}
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
  const title = <title>{`${clock(mark.at)} · ${mark.label}`}</title>
  switch (mark.kind) {
    case "fork":
      return (
        <rect
          x={x - 5}
          y={y - 5}
          width={10}
          height={10}
          fill="var(--acc-git)"
          transform={`rotate(45 ${x} ${y})`}
        >
          {title}
        </rect>
      )
    case "merge":
      return (
        <rect x={x - 5} y={y - 5} width={10} height={10} fill="var(--acc-git)">
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

/** A folded sandbox: its line with a dot where each session opened. */
function FoldedTrack({ row, width, x, right, since, now }: TrackProps) {
  const y = FOLDED / 2
  return (
    <svg
      width={width}
      height={FOLDED}
      role="img"
      aria-label={`${row.sandbox.name}, folded: ${row.branches.length} sessions`}
      className="block"
    >
      <Grid since={since} now={now} x={x} />
      <MainLine row={row} x={x} right={right} y={y} />
      {row.branches.map((b) => (
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
      return `preview ${b.title}`
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

function Track({
  env,
  row,
  width,
  x,
  right,
  since,
  now,
}: TrackProps & { env: string }) {
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
  const draw = (lanes: Lane[], side: "up" | "down") =>
    lanes.flatMap((l, i) =>
      l.map((b, j) => (
        <BranchPath
          key={b.id}
          env={env}
          sandboxId={row.sandbox.id}
          branch={b}
          main={main}
          y={lane(side, i)}
          x={x}
          right={right}
          since={since}
          // A label runs between the branches before and after it in its lane.
          after={j > 0 ? x(l[j - 1].end ?? now) : 0}
          until={l[j + 1] ? x(l[j + 1].start) : right}
        />
      ))
    )
  return (
    <svg
      width={width}
      height={height}
      role="img"
      aria-label={`${row.sandbox.name} with its sessions branching off`}
      className="block"
    >
      <Grid since={since} now={now} x={x} />
      {draw(up, "up")}
      {draw(down, "down")}
      <MainLine row={row} x={x} right={right} y={main} />
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
  after,
  until,
}: {
  env: string
  sandboxId: string
  branch: Branch
  /** Where the lane's previous branch ended. */
  after: number
  main: number
  y: number
  x: (t: number) => number
  right: number
  since: number
  until: number
}) {
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
  const text =
    (before ? `◂ ${clock(b.start)} · ` : "") +
    `${glyph[b.kind]} ${branchLabel(b)}`
  // A label starts where its branch reaches the lane, or further left, back to where the
  // lane's previous branch ended, when it doesn't fit before the next one.
  const natural = before ? 4 : x0 + CURVE + 4
  const labelX = Math.max(
    Math.min(natural, until - 10 - text.length * CHAR),
    Math.min(natural, after + 8)
  )
  const room = Math.max(0, until - labelX - 10)
  const max = Math.floor(room / CHAR)
  const shown =
    max <= 1 ? "" : text.length > max ? text.slice(0, max - 1) + "…" : text
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
          },
        }
  return (
    <Link
      {...target}
      aria-label={`${branchLabel(b)}, ${when}`}
      className="group/branch outline-none"
    >
      <title>{`${branchLabel(b)}\n${when}`}</title>
      <path
        d={`${leave} ${back}`}
        fill="none"
        stroke={color}
        strokeWidth={1.5}
        strokeDasharray={b.kind === "preview" ? "2 3" : undefined}
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
        const bx = Math.max(x0 + (before ? 0 : CURVE), x(k.start))
        const bw = Math.max(3, (k.end === undefined ? right : x(k.end)) - bx)
        return (
          <g key={i}>
            <rect
              x={bx}
              y={y - 6}
              width={bw}
              height={12}
              fill={blockFill[k.tone]}
              stroke={k.tone === "drive" ? "var(--line-you)" : undefined}
            />
            {k.label && bw > 40 && (
              <text
                x={bx + 4}
                y={y + 3}
                className="pointer-events-none fill-foreground font-mono text-[9px]"
              >
                {k.label.length > (bw - 8) / 5.5
                  ? k.label.slice(
                      0,
                      Math.max(1, Math.floor((bw - 8) / 5.5) - 1)
                    ) + "…"
                  : k.label}
              </text>
            )}
          </g>
        )
      })}
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
      {shown && (
        <text
          x={labelX}
          y={y - 8}
          className={cn(
            "font-mono text-meta",
            b.yours ? "fill-(--line-you)" : "fill-foreground",
            b.kind === "preview" && "fill-muted-foreground",
            b.end !== undefined && "fill-muted-foreground"
          )}
        >
          {shown}
        </text>
      )}
    </Link>
  )
}
