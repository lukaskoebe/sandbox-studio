import type { components } from "@/lib/api/schema.gen"

export type SessionLinesData = components["schemas"]["SessionLines"]
type Span = components["schemas"]["SessionSpan"]
type Turn = components["schemas"]["ChatTurnSpan"]
type LineageEvent = components["schemas"]["SandboxEvent"]

/** What a branch is: which side of the line it takes, its color and its label follow. */
export type BranchKind = "chat" | "terminal" | "browser" | "preview"

/** A stretch on a branch: a chat's turn, a program in a terminal, the user driving a browser. */
export type Block = {
  start: number
  /** Unset while it runs. */
  end?: number
  tone: "turn" | "live" | "waiting" | "stopped" | "run" | "drive"
  label?: string
}

/** A session as a branch off its sandbox's line, from when it opened to when it closed. */
export type Branch = {
  id: string
  kind: BranchKind
  start: number
  /** Unset while it is open. */
  end?: number
  title: string
  /** Who works in it: a teammate's name, or "you". */
  who?: string
  /** The user's own session, such as a shell they opened. */
  yours: boolean
  blocks: Block[]
  /** A group's previews, which opened around the same time (groupPreviews). */
  group?: Branch[]
  /** What a click opens. */
  link:
    | { to: "chat"; teammateId: string; chatId: string }
    | {
        to: "screen"
        tab: "terminal" | "browser" | "preview"
        term?: string
        port?: number
      }
}

/** A lineage event on the sandbox's own line. */
export type Mark = {
  at: number
  kind: "checkpoint" | "fork" | "merge" | "restore" | "rebase" | "create"
  label: string
}

/** A chat stays out on its branch this long after a turn, then rejoins the line. */
export const chatGap = 30 * 60 * 1000

const ms = (iso?: string) => (iso ? Date.parse(iso) : undefined)

/** The name of a terminal from its span key, "name@created". */
export function terminalName(key: string) {
  const at = key.lastIndexOf("@")
  return at > 0 ? key.slice(0, at) : key
}

/** A URL as a branch shows it: its host and path. */
function shortURL(url: string) {
  try {
    const u = new URL(url)
    return u.host + (u.pathname === "/" ? "" : u.pathname)
  } catch {
    return url
  }
}

/**
 * The branches of each sandbox: chat turns grouped into a branch while they follow each other
 * within chatGap, terminals with the programs that ran in them, browser sessions with the
 * user's turns, and ports.
 */
export function buildBranches(
  data: SessionLinesData,
  {
    now,
    teammateName,
    waiting,
  }: {
    now: number
    teammateName: (id: string) => string | undefined
    /** Sandboxes with an approval waiting, whose running turns wait on the user. */
    waiting: ReadonlySet<string>
  }
): Map<string, Branch[]> {
  const out = new Map<string, Branch[]>()
  const add = (sandbox: string, b: Branch) => {
    const list = out.get(sandbox)
    if (list) list.push(b)
    else out.set(sandbox, [b])
  }
  const chatOwner = new Map<string, string>()

  // Chats: one branch per run of turns.
  const byChat = new Map<string, Turn[]>()
  for (const t of data.turns ?? []) {
    chatOwner.set(t.chatId, t.teammateId)
    const list = byChat.get(t.chatId)
    if (list) list.push(t)
    else byChat.set(t.chatId, [t])
  }
  for (const [chatId, turns] of byChat) {
    let group: Turn[] = []
    let n = 0
    const flush = () => {
      if (!group.length) return
      const first = group[0]
      const ends = group.map((t) => ms(t.endedAt))
      const running = ends.some((e) => e === undefined)
      const last = running ? undefined : Math.max(...(ends as number[]))
      const open = running || (last !== undefined && now - last < chatGap)
      add(first.sandboxId, {
        id: `chat:${chatId}:${n++}`,
        kind: "chat",
        start: ms(first.startedAt)!,
        end: open ? undefined : last,
        title: first.title || "New chat",
        who: teammateName(first.teammateId),
        yours: false,
        blocks: group.map((t) => {
          const end = ms(t.endedAt)
          const stopped =
            t.stopReason === "cancelled" || t.stopReason === "interrupted"
          return {
            start: ms(t.startedAt)!,
            end,
            tone:
              end === undefined
                ? waiting.has(t.sandboxId)
                  ? "waiting"
                  : "live"
                : stopped
                  ? "stopped"
                  : "turn",
          }
        }),
        link: { to: "chat", teammateId: first.teammateId, chatId },
      })
      group = []
    }
    let lastEnd = -Infinity
    for (const t of turns) {
      const start = ms(t.startedAt)!
      if (group.length && start - lastEnd > chatGap) flush()
      group.push(t)
      lastEnd = ms(t.endedAt) ?? Infinity
    }
    flush()
  }

  // Runs and takeovers go on the branch of their terminal or browser session.
  const children = new Map<string, Span[]>()
  for (const s of data.spans ?? []) {
    if ((s.kind === "run" || s.kind === "drive") && s.parent) {
      const k = `${s.sandboxId}\x00${s.parent}`
      const list = children.get(k)
      if (list) list.push(s)
      else children.set(k, [s])
    }
  }
  const blocksOf = (s: Span, tone: Block["tone"]) =>
    (children.get(`${s.sandboxId}\x00${s.key}`) ?? []).map((c) => ({
      start: ms(c.openedAt)!,
      end: ms(c.closedAt),
      tone,
      label: c.label,
    }))

  for (const s of data.spans ?? []) {
    const start = ms(s.openedAt)!
    const end = ms(s.closedAt)
    switch (s.kind) {
      case "terminal": {
        const name = terminalName(s.key)
        const owner = s.chatId ? chatOwner.get(s.chatId) : undefined
        const agent = !!(s.harness || s.chatId)
        add(s.sandboxId, {
          id: `terminal:${s.id}`,
          kind: "terminal",
          start,
          end,
          title: s.label && s.label !== name ? `${name} · ${s.label}` : name,
          who: agent
            ? ((owner && teammateName(owner)) ?? s.harness ?? "agent")
            : "you",
          yours: !agent,
          blocks: blocksOf(s, "run"),
          link: { to: "screen", tab: "terminal", term: name },
        })
        break
      }
      case "browser":
        add(s.sandboxId, {
          id: `browser:${s.id}`,
          kind: "browser",
          start,
          end,
          title: s.label ? shortURL(s.label) : "browser",
          who: s.teammateId ? teammateName(s.teammateId) : undefined,
          yours: false,
          blocks: blocksOf(s, "drive"),
          link: { to: "screen", tab: "browser" },
        })
        break
      case "preview":
        add(s.sandboxId, {
          id: `preview:${s.id}`,
          kind: "preview",
          start,
          end,
          title: s.label || `:${s.key}`,
          yours: false,
          blocks: [],
          link: {
            to: "screen",
            tab: "preview",
            port: Number(s.key) || undefined,
          },
        })
        break
    }
  }
  for (const [sandbox, list] of out) {
    list.sort((a, b) => a.start - b.start)
    out.set(sandbox, groupPreviews(list))
  }
  return out
}

/** Previews that open within this long of each other's span may go on one branch. */
export const previewGap = 10 * 60 * 1000
/** How many previews at once make a group. */
export const previewGroup = 3

/**
 * Previews come in crowds (a dev server, its HMR socket, a database console), each its own
 * branch down to the outermost lane. Previews whose spans overlap or follow within previewGap go
 * on one branch when they are previewGroup or more, its label naming the ports and a click
 * opening the newest one still open. Branches stay sorted by start.
 */
export function groupPreviews(branches: Branch[]): Branch[] {
  const out: Branch[] = []
  let crowd: Branch[] = []
  let end = -Infinity
  const flush = () => {
    if (crowd.length >= previewGroup) {
      // Ports that opened together, in a pass, go in order.
      crowd.sort(
        (a, b) =>
          a.start - b.start ||
          a.title.localeCompare(b.title, undefined, { numeric: true })
      )
      const open = crowd.filter((b) => b.end === undefined)
      const newest = (open.length ? open : crowd).at(-1)!
      out.push({
        id: `previews:${crowd[0].id}`,
        kind: "preview",
        start: crowd[0].start,
        end: open.length ? undefined : Math.max(...crowd.map((b) => b.end!)),
        title: `${crowd.length} previews`,
        yours: false,
        blocks: [],
        group: crowd,
        link: newest.link,
      })
    } else out.push(...crowd)
    crowd = []
  }
  for (const b of branches) {
    if (b.kind !== "preview") {
      out.push(b)
      continue
    }
    if (crowd.length && b.start - end > previewGap) flush()
    crowd.push(b)
    end = Math.max(end, b.end ?? Infinity)
  }
  flush()
  return out.sort((a, b) => a.start - b.start)
}

/** A branch's sessions: a group's previews, or the branch itself. */
export const sessionsOf = (b: Branch) => b.group ?? [b]

/** The marks of each sandbox's line, and when a fork's line began. */
export function buildMarks(
  events: LineageEvent[],
  forkOf: ReadonlyMap<string, string | undefined>,
  name: (id: string) => string
) {
  const marks = new Map<string, Mark[]>()
  const born = new Map<string, number>()
  const add = (id: string, m: Mark) => {
    const list = marks.get(id)
    if (list) list.push(m)
    else marks.set(id, [m])
  }
  for (const e of events) {
    const at = Date.parse(e.createdAt)
    switch (e.kind) {
      case "create":
        born.set(e.sandboxId, at)
        break
      case "fork":
        if (e.otherSandboxId && forkOf.get(e.sandboxId) === e.otherSandboxId)
          born.set(e.sandboxId, at)
        else
          add(e.sandboxId, {
            at,
            kind: "fork",
            label: `forked → ${name(e.otherSandboxId ?? "")}`,
          })
        break
      case "checkpoint":
        add(e.sandboxId, {
          at,
          kind: "checkpoint",
          label: e.checkpointName
            ? `checkpoint “${e.checkpointName}”`
            : "checkpoint",
        })
        break
      case "merge":
        add(e.sandboxId, {
          at,
          kind: "merge",
          label: `merged ${name(e.otherSandboxId ?? "")}`,
        })
        break
      case "restore":
        add(e.sandboxId, {
          at,
          kind: "restore",
          label: e.checkpointName
            ? `restored “${e.checkpointName}”`
            : "restored",
        })
        break
      case "rebase":
        add(e.sandboxId, {
          at,
          kind: "rebase",
          label: "moved to a new template",
        })
        break
    }
  }
  return { marks, born }
}

/** A lane of branches on one side of the line; branches in a lane don't overlap. */
export type Lane = Branch[]

/**
 * Packs branches into lanes, first fit: a branch takes the first lane whose last branch ended
 * at least gap before it opens, where x places a time. Open branches run to the right edge.
 */
export function packLanes(
  branches: Branch[],
  x: (t: number) => number,
  right: number,
  gap: number
): Lane[] {
  const lanes: { end: number; branches: Branch[] }[] = []
  for (const b of branches) {
    const from = x(b.start)
    const to = b.end === undefined ? right : x(b.end)
    const lane = lanes.find((l) => l.end + gap <= from)
    if (lane) {
      lane.branches.push(b)
      lane.end = to
    } else lanes.push({ end: to, branches: [b] })
  }
  return lanes.map((l) => l.branches)
}
