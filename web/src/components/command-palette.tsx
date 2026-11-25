import {
  type ReactNode,
  useEffect,
  useId,
  useMemo,
  useRef,
  useState,
} from "react"
import { useMatches, useNavigate } from "@tanstack/react-router"
import {
  ArrowClockwiseIcon,
  ArrowsInLineHorizontalIcon,
  BrowserIcon,
  ChatCircleIcon,
  CheckSquareIcon,
  DesktopTowerIcon,
  FileIcon,
  FolderIcon,
  GearIcon,
  HouseIcon,
  LightningIcon,
  MagnifyingGlassIcon,
  PlusIcon,
  PowerIcon,
  SidebarSimpleIcon,
  SparkleIcon,
} from "@phosphor-icons/react"
import { Dialog, DialogContent, DialogTitle } from "@/components/ui/dialog"
import { Kbd } from "@/components/ui/kbd"
import { useSidebar } from "@/components/ui/sidebar"
import { StatusDot } from "@/components/status-dot"
import { EnvironmentIcon } from "@/components/environment-icon"
import { TeammateAvatar } from "@/components/teammate-avatar"
import { phase, phaseDot } from "@/components/status-badge"
import { resetStoredSizes } from "@/components/resize-handle"
import { tabMeta } from "@/components/computer/tab-meta"
import {
  environmentPages,
  settingsGroups,
  settingsPaths,
} from "@/components/settings/link"
import { useMentionIndex } from "@/components/chat/mention-picker"
import {
  approvalKind,
  approvalSource,
  approvalTeammateId,
} from "@/lib/approval-kinds"
import { loadVisits, type Visit } from "@/lib/places"
import { describeVisit } from "@/components/continue-places"
import {
  errorMessage,
  type Approval,
  type Sandbox,
  type Teammate,
} from "@/lib/api/client"
import { chatTitle, type Chat } from "@/lib/chats"
import {
  lastComputer,
  screenComputer,
  type ScreenTab,
  type TeammateSearch,
} from "@/lib/computer"
import { hasModKey } from "@/lib/keys"
import { rank } from "@/lib/mentions"
import { setPaletteOpen, usePaletteOpen } from "@/lib/palette"
import { newThreadWith } from "@/lib/new-thread"
import { askPower } from "@/lib/power"
import { useStartNewChat } from "@/lib/new-chat"
import { usePreviewSetting } from "@/lib/preview-setting"
import { teammateSettings } from "@/lib/teammate-settings"
import { setStyleName, useStyleName } from "@/lib/theme"
import { cn } from "@/lib/utils"

type Group =
  | "Continue"
  | "Needs you"
  | "Teammates"
  | "Computers"
  | "Threads"
  | "Environment"
  | "Settings"
  | "Actions"
  | "Files"

/** What the palette finds: where it lives says `place`, and `keys` are more words it answers to. */
type Item = {
  id: string
  group: Group
  title: string
  place: string
  keys: string
  lead: ReactNode
  mono?: boolean
  /** Added to the score, so the likelier action comes first. */
  boost?: number
  /**
   * A part of the teammate or computer its place names, such as a tab: found by its own name or
   * keys, which the place only narrows, so a name alone doesn't list every part of it.
   */
  part?: boolean
  run: () => void
}

/** The prefixes that keep a search to one group; / searches the files of the computer in view. */
const scopes: Partial<Record<string, Group>> = {
  ">": "Actions",
  "@": "Teammates",
}

const recentKey = "studio.palette-recent"

function loadRecent(): string[] {
  try {
    const v = JSON.parse(localStorage.getItem(recentKey) ?? "[]")
    return Array.isArray(v) ? v.filter((x) => typeof x === "string") : []
  } catch {
    return []
  }
}

function saveRecent(ids: string[]) {
  try {
    localStorage.setItem(recentKey, JSON.stringify(ids))
  } catch {
    // Without storage the palette forgets what was opened.
  }
}

const wordStart = /[\s/\-_.'’›:]/

/**
 * Whether a and b are at most one typo apart: a letter changed, added or dropped, or two
 * swapped.
 */
function oneTypo(a: string, b: string) {
  let i = 0
  while (i < a.length && i < b.length && a[i] === b[i]) i++
  if (a.length === b.length)
    return (
      a.slice(i + 1) === b.slice(i + 1) ||
      (a[i] === b[i + 1] &&
        a[i + 1] === b[i] &&
        a.slice(i + 2) === b.slice(i + 2))
    )
  if (Math.abs(a.length - b.length) > 1) return false
  return a.length > b.length
    ? a.slice(i + 1) === b.slice(i)
    : a.slice(i) === b.slice(i + 1)
}

/**
 * How well one word of the query matches s: as a run of letters, best at the start of s or of a
 * word in it, else as its letters in order and close together, else, for a word of six letters
 * or more, as the start of a word in s one typo away ("netwrok", "secerts"), last. The indexes
 * are the matched letters, for highlighting.
 */
function fuzzy(q: string, s: string): { score: number; idx: number[] } | null {
  const t = s.toLowerCase()
  const at = t.indexOf(q)
  if (at >= 0) {
    const starts = at === 0 || wordStart.test(t[at - 1])
    return {
      score: (at === 0 ? 100 : starts ? 82 : 60) - at * 0.2 - t.length * 0.05,
      idx: [...q].map((_, i) => at + i),
    }
  }
  if (q.length < 2) return null
  let best: { score: number; idx: number[] } | null = null
  for (let st = t.indexOf(q[0]); st >= 0; st = t.indexOf(q[0], st + 1)) {
    const idx: number[] = []
    let qi = 0
    let score = 18
    let prev = -2
    for (let i = st; i < t.length && qi < q.length; i++) {
      if (t[i] !== q[qi]) continue
      score +=
        (i === prev + 1 ? 4 : 0) + (i === 0 || wordStart.test(t[i - 1]) ? 6 : 0)
      idx.push(i)
      prev = i
      qi++
    }
    if (qi < q.length) break
    const span = idx[idx.length - 1] - idx[0]
    if (span > q.length * 2 + 3) continue
    score -= span * 0.3 + idx[0] * 0.05
    if (!best || score > best.score) best = { score, idx }
  }
  if (best || q.length < 6) return best
  for (let w = 0; w < t.length; w++) {
    if (w > 0 && !wordStart.test(t[w - 1])) continue
    if (t[w] !== q[0]) continue
    for (const n of [q.length, q.length - 1, q.length + 1]) {
      const seg = t.slice(w, w + n)
      if (seg.length === n && !wordStart.test(seg) && oneTypo(q, seg))
        return { score: 10, idx: [...seg].map((_, i) => w + i) }
    }
  }
  return null
}

/**
 * Every word of the query must match the title, or start a word of the keys or the place; a
 * part needs a word of its own, in the title or the keys.
 */
function matchItem(q: string, it: Item) {
  let score = 0
  let own = !it.part
  const hi = new Set<number>()
  const keys = ` ${it.keys}`.toLowerCase()
  const place = ` ${it.place}`.toLowerCase()
  for (const word of q.split(/\s+/).filter(Boolean)) {
    const a = fuzzy(word, it.title)
    const inKeys = keys.includes(` ${word}`)
    const kw = inKeys || place.includes(` ${word}`) ? 34 : 0
    if (a && a.score >= kw) {
      score += a.score
      own = true
      a.idx.forEach((i) => hi.add(i))
    } else if (kw) {
      score += kw
      own ||= inKeys
    } else return null
  }
  return own ? { score: score + (it.boost ?? 0), hi } : null
}

function Highlight({ text, hi }: { text: string; hi?: Set<number> }) {
  if (!hi?.size) return text
  return [...text].map((ch, i) =>
    hi.has(i) ? (
      <mark key={i} className="bg-transparent font-semibold text-primary">
        {ch}
      </mark>
    ) : (
      ch
    )
  )
}

/**
 * The search palette (⌘K, Ctrl+K elsewhere): teammates with their browsers, tasks, memory and
 * settings rows, computers with their tabs, threads, Home and the environment's pages, settings
 * and actions, grouped by kind with where each lives on the right, so a search also teaches the
 * map. With nothing typed it lists what was opened last, then the places last worked in, and
 * what waits for you, each with whose it is.
 * `>` keeps a search to actions, `@` to teammates, and `/` searches the files of the computer in
 * view, opening the pick in its Files tab.
 */
export function CommandPalette({
  env,
  teammates,
  sandboxes,
  chats,
  pending,
  onNewTeammate,
  onNewComputer,
}: {
  env: string
  teammates: Teammate[]
  sandboxes: Sandbox[]
  chats: Chat[]
  pending: Approval[]
  onNewTeammate: () => void
  onNewComputer: () => void
}) {
  const open = usePaletteOpen()
  const navigate = useNavigate()
  const { toggleSidebar } = useSidebar()
  const style = useStyleName()
  const previews = usePreviewSetting().enabled
  const startNewChat = useStartNewChat(env)
  const [query, setQuery] = useState("")
  const [sel, setSel] = useState(0)
  const [recent, setRecent] = useState(loadRecent)
  const [visits, setVisits] = useState<Visit[]>([])
  const listId = useId()
  const listRef = useRef<HTMLDivElement>(null)

  // ⌘K opens and closes it from anywhere but a terminal, where Ctrl+K is the shell's.
  useEffect(() => {
    const onKey = (e: KeyboardEvent) => {
      if (e.key.toLowerCase() !== "k" || !hasModKey(e)) return
      if (e.target instanceof Element && e.target.closest(".xterm")) return
      e.preventDefault()
      setPaletteOpen(!open)
    }
    window.addEventListener("keydown", onKey, true)
    return () => window.removeEventListener("keydown", onKey, true)
  }, [open])
  useEffect(() => {
    if (open) {
      setQuery("")
      setSel(0)
      setRecent(loadRecent())
      setVisits(loadVisits(env))
    }
  }, [open, env])

  const items = useMemo(() => {
    const out: Item[] = []
    const byId = new Map(teammates.map((t) => [t.id, t]))
    for (const t of teammates) {
      out.push({
        id: `tm:${t.id}`,
        group: "Teammates",
        title: t.name,
        place: "teammate",
        keys: "teammate agent",
        lead: (
          <TeammateAvatar
            name={t.name}
            avatar={t.avatar}
            className="size-4 text-[0.5rem]"
          />
        ),
        run: () =>
          navigate({ to: "/e/$env/teammates/$id", params: { env, id: t.id } }),
      })
      out.push({
        id: `tmset:${t.id}`,
        group: "Teammates",
        title: `${t.name}'s settings`,
        place: t.name,
        // Its rows answer to what they hold, below.
        keys: "settings profile",
        lead: <GearIcon />,
        run: () =>
          navigate({
            to: "/e/$env/teammates/$id",
            params: { env, id: t.id },
            search: { profile: "settings" },
          }),
      })
      // The browser is on the teammate's screen, which a teammate without a computer hasn't.
      if (sandboxes.some((sb) => sb.teammateId === t.id))
        out.push({
          id: `tmbr:${t.id}`,
          group: "Teammates",
          title: `${t.name}'s browser`,
          place: t.name,
          keys: "browser web page tab devtools",
          lead: <BrowserIcon />,
          run: () =>
            navigate({
              to: "/e/$env/teammates/$id",
              params: { env, id: t.id },
              search: { tab: "browser" },
            }),
        })
      for (const [view, label, keys, Icon] of [
        ["tasks", "tasks", "tasks todo backlog", CheckSquareIcon],
        ["memory", "memory", "memory facts knowledge learned", SparkleIcon],
      ] as const)
        out.push({
          id: `tm${view}:${t.id}`,
          group: "Teammates",
          title: `${t.name}'s ${label}`,
          place: t.name,
          keys,
          lead: <Icon />,
          run: () =>
            navigate({
              to: "/e/$env/teammates/$id",
              params: { env, id: t.id },
              search: { profile: view },
            }),
        })
      // Its settings rows, but not Delete, nor Memory and Tasks, which open as the two above.
      for (const g of teammateSettings)
        for (const r of g.rows) {
          if (r.id === "delete" || r.id === "memory" || r.id === "tasks")
            continue
          out.push({
            id: `tmrow:${t.id}:${r.id}`,
            group: "Teammates",
            title: r.label,
            place: `${t.name}'s settings`,
            keys: `${r.keywords} settings`,
            part: true,
            lead: <GearIcon />,
            run: () =>
              navigate({
                to: "/e/$env/teammates/$id",
                params: { env, id: t.id },
                search: { profile: "settings", open: r.id },
              }),
          })
        }
    }
    for (const sb of sandboxes) {
      const owner = sb.teammateId ? byId.get(sb.teammateId) : undefined
      const dot = phaseDot(phase(sb))
      out.push({
        id: `pc:${sb.id}`,
        group: "Computers",
        title: sb.name,
        place: `${owner ? `${owner.name}'s computer` : "your computer"} · ${dot.label.toLowerCase()}`,
        keys: `computer sandbox vm ${sb.templateId ?? ""} ${sb.forkDefaults ? "base" : ""}`,
        mono: true,
        lead: (
          <span className="flex size-4 items-center justify-center">
            <StatusDot {...dot} compact />
          </span>
        ),
        run: () =>
          navigate({ to: "/e/$env/sandboxes/$id", params: { env, id: sb.id } }),
      })
      // Its tabs, as its screen shows them: Terminal and Details while it is off too, and the
      // browser not, which is its teammate's (above).
      const p = phase(sb)
      if (p === "creating" || p === "failed") continue
      const tabs: ScreenTab[] = [
        "terminal",
        ...(p === "ready"
          ? ([
              ...(previews ? (["preview"] as const) : []),
              "files",
              "changes",
            ] as const)
          : []),
        "details",
      ]
      for (const tab of tabs) {
        const { label, icon: Icon } = tabMeta[tab]
        out.push({
          id: `tab:${sb.id}:${tab}`,
          group: "Computers",
          title: label,
          place: sb.name,
          keys: `${tab} tab ${tab === "preview" ? "ports app dev server" : ""}`,
          part: true,
          lead: <Icon />,
          run: () =>
            owner
              ? navigate({
                  to: "/e/$env/teammates/$id",
                  params: { env, id: owner.id },
                  search: { computer: sb.id, tab },
                })
              : navigate({
                  to: "/e/$env/sandboxes/$id",
                  params: { env, id: sb.id },
                  search: { tab },
                }),
        })
      }
    }
    const names = new Map(sandboxes.map((sb) => [sb.id, sb.name]))
    for (const c of [...chats].sort((a, b) =>
      b.updatedAt.localeCompare(a.updatedAt)
    )) {
      const who = byId.get(c.teammateId)?.name
      out.push({
        id: `th:${c.id}`,
        group: "Threads",
        title: chatTitle(c),
        place: [who, names.get(c.sandboxId)].filter(Boolean).join(" · "),
        keys: "thread chat conversation",
        lead: <ChatCircleIcon className="text-(--acc-chat)" />,
        run: () =>
          navigate({ to: "/e/$env/chats/$chat", params: { env, chat: c.id } }),
      })
    }
    out.push({
      id: "env:home",
      group: "Environment",
      title: "Home",
      place: "Studio",
      keys: "start overview dashboard approvals requests lines timeline",
      lead: <HouseIcon />,
      run: () => navigate({ to: "/e/$env", params: { env } }),
    })
    out.push({
      id: "env:computers",
      group: "Environment",
      title: "Computers",
      place: "Environment",
      keys: "sandboxes vms forks bases inventory rebase",
      lead: <DesktopTowerIcon />,
      run: () => navigate({ to: "/e/$env/computers", params: { env } }),
    })
    for (const p of environmentPages)
      out.push({
        id: `env:${p.id}`,
        group: "Environment",
        title: p.label,
        place: "Environment",
        keys: p.keywords,
        lead: <EnvironmentIcon page={p.id} />,
        run: () => navigate({ to: settingsPaths[p.id], params: { env } }),
      })
    for (const g of settingsGroups)
      for (const s of g.sections)
        out.push({
          id: `set:${s.id}`,
          group: "Settings",
          title: s.label,
          place: `Settings › ${g.title}`,
          keys: `${s.keywords} settings`,
          lead: <GearIcon />,
          run: () => navigate({ to: settingsPaths[s.id], params: { env } }),
        })
    const act = (
      id: string,
      title: string,
      place: string,
      keys: string,
      run: () => void,
      lead: ReactNode = <LightningIcon />
    ) =>
      out.push({
        id: `act:${id}`,
        group: "Actions",
        title,
        place,
        keys,
        lead,
        run,
      })
    act(
      "new-teammate",
      "New teammate…",
      "Teammates",
      "add create agent",
      onNewTeammate
    )
    if (teammates.length > 1)
      act(
        "new-chat",
        "New chat…",
        "Who should do it?",
        "start thread ask task begin",
        startNewChat,
        <PlusIcon />
      )
    for (const t of teammates)
      act(
        `new-thread:${t.id}`,
        `New thread with ${t.name}`,
        t.name,
        "start work chat ask task begin",
        () => {
          newThreadWith(t.id)
          void navigate({
            to: "/e/$env/teammates/$id",
            params: { env, id: t.id },
          })
        },
        <PlusIcon />
      )
    act(
      "new-computer",
      "New computer…",
      "Computers",
      "add create sandbox vm",
      onNewComputer
    )
    act(
      "style",
      style === "quiet"
        ? "Switch to the Pixel style"
        : "Switch to the Quiet style",
      "Settings › Appearance",
      "style look theme pixel quiet",
      () => setStyleName(style === "quiet" ? "pixel" : "quiet")
    )
    act(
      "sidebar",
      "Fold or unfold the sidebar",
      "⌘B",
      "sidebar collapse hide",
      toggleSidebar,
      <SidebarSimpleIcon />
    )
    act(
      "reset-widths",
      "Reset panel widths",
      "this browser",
      "layout width split default",
      resetStoredSizes,
      <ArrowsInLineHorizontalIcon />
    )
    act(
      "restart",
      "Restart Studio…",
      "Studio",
      "reboot reload server",
      () => askPower("restart"),
      <ArrowClockwiseIcon />
    )
    act(
      "quit",
      "Quit Studio…",
      "Studio",
      "shut down shutdown stop exit close app server",
      () => askPower("quit"),
      <PowerIcon />
    )
    return out
  }, [
    env,
    teammates,
    sandboxes,
    chats,
    navigate,
    onNewTeammate,
    onNewComputer,
    style,
    toggleSidebar,
    previews,
    startNewChat,
  ])

  const waiting = useMemo(
    () =>
      pending.slice(0, 5).map((a): Item => {
        const kind = approvalKind(a)
        const Icon = kind.icon
        const who = teammates.find((t) => t.id === approvalTeammateId(a))
        // Whose it is and where it came from, so two alike are told apart.
        const permission = a.chatPermission
        const source = permission
          ? permission.chatTitle || "New chat"
          : approvalSource(a)
        return {
          id: `ap:${a.id}`,
          group: "Needs you",
          title: kind.summary(a),
          place: [who?.name, source !== who?.name ? source : undefined]
            .filter(Boolean)
            .join(" · "),
          keys: "",
          mono: kind.mono,
          lead: <Icon />,
          run: () => navigate({ to: "/e/$env", params: { env } }),
        }
      }),
    [pending, teammates, navigate, env]
  )

  // The places last worked in, as Home's Continue lists them.
  const places = useMemo(
    () =>
      visits.flatMap((v): Item[] => {
        const row = describeVisit(v, teammates, chats, sandboxes)
        if (!row) return []
        const Icon = row.icon
        return [
          {
            id: `visit:${v.href}`,
            group: "Continue",
            title: row.title,
            place: row.detail ?? "",
            keys: "",
            lead: <Icon />,
            run: () => void navigate({ href: v.href }),
          },
        ]
      }),
    [visits, teammates, chats, sandboxes, navigate]
  )

  // The computer whose files / searches: the one the page shows.
  const leaf = useMatches().at(-1)
  const routeId = (leaf?.routeId ?? "") as string
  const params = (leaf?.params ?? {}) as Record<string, string>
  const search = (leaf?.search ?? {}) as TeammateSearch
  const inView =
    routeId === "/e/$env/sandboxes/$id"
      ? sandboxes.find((s) => s.id === params.id)
      : routeId === "/e/$env/teammates/$id"
        ? screenComputer(
            sandboxes.filter((s) => s.teammateId === params.id),
            chats.filter((c) => c.teammateId === params.id),
            search.computer,
            chats.find((c) => c.id === search.thread)?.sandboxId,
            lastComputer(env, params.id)
          )
        : undefined
  const filesMode = query.startsWith("/")
  const { index, all } = useMentionIndex(
    env,
    inView?.id ?? "",
    open && filesMode && !!inView
  )
  const fileItems = useMemo((): (Item & { hi?: Set<number> })[] => {
    if (!filesMode || !inView || !all) return []
    return rank(all, query.slice(1).trim(), 12).shown.map((c) => {
      const hi = new Set<number>(c.hits)
      if (c.match)
        for (let i = c.match[0]; i < c.match[1]; i++) hi.add(c.dir.length + i)
      const dir = c.folder ? c.path.slice(0, -1) : c.dir.slice(0, -1)
      return {
        id: `file:${c.path}`,
        group: "Files",
        title: c.path,
        place: inView.name,
        keys: "",
        mono: true,
        lead: c.folder ? <FolderIcon /> : <FileIcon />,
        hi,
        run: () => {
          const files = {
            tab: "files" as const,
            files: dir || undefined,
            file: c.folder ? undefined : c.path,
          }
          if (routeId === "/e/$env/teammates/$id")
            void navigate({
              to: "/e/$env/teammates/$id",
              params: { env, id: params.id },
              search: { computer: inView.id, ...files },
            })
          else
            void navigate({
              to: "/e/$env/sandboxes/$id",
              params: { env, id: inView.id },
              search: files,
            })
        },
      }
    })
  }, [filesMode, inView, all, query, routeId, params.id, env, navigate])

  const scope = scopes[query[0]]
  const q = (scope ? query.slice(1) : query).trim().toLowerCase()
  const groups = useMemo((): [Group, (Item & { hi?: Set<number> })[]][] => {
    if (filesMode) return [["Files", fileItems]]
    if (!q && !scope) {
      const byId = new Map(items.map((i) => [i.id, i]))
      const picked = recent
        .map((id) => byId.get(id))
        .filter((i): i is Item => !!i)
      const titles = new Set(picked.map((i) => i.title))
      const last = [...picked, ...places.filter((p) => !titles.has(p.title))]
        .slice(0, 5)
        .map((i) => ({ ...i, group: "Continue" as const }))
      return [
        ["Continue", last],
        ["Needs you", waiting],
      ]
    }
    const hits = items
      .filter((i) => !scope || i.group === scope)
      .map((i) => ({
        i,
        m: q ? matchItem(q, i) : { score: 0, hi: new Set<number>() },
      }))
      .filter((h): h is { i: Item; m: NonNullable<typeof h.m> } => !!h.m)
      .sort((a, b) => b.m.score - a.m.score)
    const by = new Map<Group, (Item & { hi?: Set<number> })[]>()
    for (const h of hits) {
      const g = by.get(h.i.group) ?? []
      by.set(h.i.group, g)
      if (g.length < (scope ? 12 : 5)) g.push({ ...h.i, hi: h.m.hi })
    }
    return [...by.entries()]
  }, [items, waiting, recent, places, q, scope, filesMode, fileItems])
  const rows = groups.flatMap(([, g]) => g)
  const at = Math.min(sel, Math.max(0, rows.length - 1))

  useEffect(() => {
    listRef.current
      ?.querySelector('[aria-selected="true"]')
      ?.scrollIntoView({ block: "nearest" })
  }, [at, query])

  const run = (it: Item | undefined) => {
    if (!it) return
    setPaletteOpen(false)
    if (!/^(act|ap|file|visit):/.test(it.id)) {
      const next = [it.id, ...recent.filter((id) => id !== it.id)].slice(0, 6)
      setRecent(next)
      saveRecent(next)
    }
    it.run()
  }

  let n = 0
  return (
    <Dialog open={open} onOpenChange={setPaletteOpen}>
      <DialogContent
        bare
        showCloseButton={false}
        className="top-[12vh] flex max-h-[76vh] translate-y-0 flex-col overflow-hidden sm:max-w-xl data-open:zoom-in-100"
      >
        <DialogTitle className="sr-only">Search Studio</DialogTitle>
        <div className="flex shrink-0 items-center gap-2.5 border-b px-3.5 text-muted-foreground">
          <MagnifyingGlassIcon className="size-4 shrink-0" />
          <input
            autoFocus
            type="text"
            role="combobox"
            aria-label="Search Studio"
            aria-expanded="true"
            aria-controls={listId}
            aria-autocomplete="list"
            aria-activedescendant={rows.length ? `${listId}-${at}` : undefined}
            autoComplete="off"
            spellCheck={false}
            placeholder="Search teammates, computers, threads, settings and actions"
            className="min-w-0 flex-1 bg-transparent py-3 text-sm text-foreground outline-none placeholder:text-muted-foreground"
            value={query}
            onChange={(e) => {
              setQuery(e.target.value)
              setSel(0)
            }}
            onKeyDown={(e) => {
              if (e.key === "ArrowDown" || e.key === "ArrowUp") {
                e.preventDefault()
                if (rows.length)
                  setSel(
                    (at + (e.key === "ArrowDown" ? 1 : rows.length - 1)) %
                      rows.length
                  )
              } else if (e.key === "Enter") {
                e.preventDefault()
                run(rows[at])
              }
            }}
          />
          <Kbd>esc</Kbd>
        </div>
        <div
          ref={listRef}
          id={listId}
          role="listbox"
          aria-label="Results"
          className="min-h-0 flex-1 overflow-y-auto px-1.5 pt-1 pb-2"
        >
          {rows.length === 0 ? (
            <p className="px-3 py-6 text-center text-muted-foreground">
              {filesMode
                ? !inView
                  ? "Open a computer to search its files."
                  : index.isError
                    ? `Can't list ${inView.name}'s files: ${errorMessage(index.error)}`
                    : !all
                      ? `Listing ${inView.name}'s files…`
                      : `No file of ${inView.name} matches “${query.slice(1).trim()}”.`
                : query.trim()
                  ? `Nothing matches “${query.trim()}”. Try a teammate, a computer, a thread, a setting or an action.`
                  : "Nothing opened yet, and nothing needs you."}
            </p>
          ) : (
            groups
              .filter(([, g]) => g.length)
              .map(([group, g]) => (
                <div key={group} role="group" aria-label={group}>
                  <span className="block px-2 pt-2.5 pb-1 section-label text-(length:--label-size) text-muted-foreground">
                    {group}
                  </span>
                  {g.map((it) => {
                    const i = n++
                    return (
                      <div
                        key={it.id}
                        id={`${listId}-${i}`}
                        role="option"
                        aria-selected={i === at}
                        className={cn(
                          "flex h-8 cursor-pointer items-center gap-2.5 rounded-md px-2 text-xs whitespace-nowrap [&_svg]:size-4 [&_svg]:shrink-0",
                          i === at && "bg-accent text-accent-foreground"
                        )}
                        onPointerMove={() => i !== at && setSel(i)}
                        onClick={() => run(it)}
                      >
                        {it.lead}
                        <span
                          className={cn(
                            "min-w-0 shrink truncate",
                            it.mono && "font-mono"
                          )}
                        >
                          <Highlight text={it.title} hi={it.hi} />
                        </span>
                        {/* A title that names its place (Ada's tasks) doesn't repeat it. */}
                        {!it.title
                          .toLowerCase()
                          .includes(it.place.toLowerCase()) && (
                          <span className="ml-auto min-w-0 shrink-[2] truncate pl-3 text-meta text-muted-foreground">
                            {it.place}
                          </span>
                        )}
                      </div>
                    )
                  })}
                </div>
              ))
          )}
        </div>
        <div className="flex shrink-0 flex-wrap items-center gap-3.5 border-t bg-background px-3.5 py-1.5 text-meta text-muted-foreground">
          {/* A phone has no arrows or Enter to point out, and the row keeps to one line. */}
          <span className="max-sm:hidden">
            <Kbd>↑</Kbd> <Kbd>↓</Kbd> move
          </span>
          <span className="max-sm:hidden">
            <Kbd>↵</Kbd> open
          </span>
          <span className="flex-1" />
          <span>
            <Kbd>&gt;</Kbd> actions
          </span>
          <span>
            <Kbd>@</Kbd> teammates
          </span>
          {inView && (
            <span>
              <Kbd>/</Kbd> files
            </span>
          )}
        </div>
      </DialogContent>
    </Dialog>
  )
}
