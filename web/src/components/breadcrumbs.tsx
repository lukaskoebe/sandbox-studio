import {
  Fragment,
  useLayoutEffect,
  useRef,
  useState,
  type ReactNode,
} from "react"
import { useMatches, useNavigate } from "@tanstack/react-router"
import {
  CaretDownIcon,
  CheckIcon,
  DesktopTowerIcon,
  GearIcon,
  GitForkIcon,
  MagnifyingGlassIcon,
  PlusIcon,
  TrayIcon,
} from "@phosphor-icons/react"
import { CreateSandboxDialog } from "@/components/create-sandbox-dialog"
import { EnvironmentIcon } from "@/components/environment-icon"
import {
  environmentPages,
  settingsGroups,
  settingsPaths,
} from "@/components/settings/link"
import { phase, phaseDot } from "@/components/status-badge"
import { StatusDot } from "@/components/status-dot"
import { TeammateAvatar } from "@/components/teammate-avatar"
import { ThreadDot } from "@/components/teammate/home"
import { Button } from "@/components/ui/button"
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuGroup,
  DropdownMenuItem,
  DropdownMenuLabel,
  DropdownMenuSeparator,
  DropdownMenuTrigger,
} from "@/components/ui/dropdown-menu"
import { SidebarTrigger } from "@/components/ui/sidebar"
import { ApprovalsButton } from "@/components/approval-stack"
import { $api, type Sandbox, type Teammate } from "@/lib/api/client"
import { chatTitle } from "@/lib/chats"
import {
  forkTree,
  lastComputer,
  screenComputer,
  type TeammateSearch,
} from "@/lib/computer"
import { openPalette } from "@/lib/palette"
import { teammateSettings } from "@/lib/teammate-settings"
import { chatStatus } from "@/lib/threads"
import { cn } from "@/lib/utils"

/** One entry of a crumb's menu: a heading, a rule, or a place to go. */
type MenuEntry =
  | { heading: string }
  | { rule: true }
  | {
      label: string
      lead?: ReactNode
      right?: ReactNode
      mono?: boolean
      inset?: boolean
      current?: boolean
      run: () => void
    }

type SettingsId = (typeof settingsGroups)[number]["sections"][number]["id"]

type Crumb = {
  key: string
  label: string
  lead?: ReactNode
  mono?: boolean
  /** The crumb's neighbours, which its menu opens. */
  menu?: MenuEntry[]
  /** The menu button's name, which says what it switches rather than repeating the label. */
  switches?: string
  /** Where the crumb goes, for one without a menu. */
  run?: () => void
}

/** The computer a teammate's page shows, said after the crumbs: "on ● docs-site". */
type Chip = { sandbox: Sandbox; menu: MenuEntry[] }

const dot = (sb: Sandbox) => <StatusDot {...phaseDot(phase(sb))} compact />

/** How many threads the thread crumb's menu lists before "All threads". */
const menuThreads = 8

/**
 * The row above every page that says where it is (docs/design/teammate-computer.md): Home, an
 * environment page, Settings › a section, or a teammate › the open thread, followed on its desk
 * and in a thread by the computer it shows ("on ● docs-site"); each part with a caret opens its
 * neighbours. What the computer's tabs show is on the computer, not up here. On phones it holds
 * the sidebar's toggle and search too.
 */
export function Breadcrumbs({ env }: { env: string }) {
  const matches = useMatches()
  const navigate = useNavigate()
  const [giving, setGiving] = useState<string>()
  const teammates = $api.useQuery("get", "/api/environments/{env}/teammates", {
    params: { path: { env } },
  })
  const sandboxes = $api.useQuery(
    "get",
    "/api/environments/{env}/sandboxes",
    { params: { path: { env } } },
    { refetchInterval: 5000 }
  )
  const chats = $api.useQuery(
    "get",
    "/api/environments/{env}/chats",
    { params: { path: { env } } },
    { refetchInterval: 10000 }
  )
  const leaf = matches.at(-1)
  const routeId = (leaf?.routeId ?? "") as string
  const params = (leaf?.params ?? {}) as Record<string, string>
  const search = (leaf?.search ?? {}) as TeammateSearch
  const allTeammates = teammates.data ?? []
  const allSandboxes = sandboxes.data ?? []
  const allChats = chats.data ?? []

  const goTeammate = (id: string, s: TeammateSearch = {}) =>
    void navigate({
      to: "/e/$env/teammates/$id",
      params: { env, id },
      search: s,
    })
  const goComputer = (sb: Sandbox) => {
    if (sb.teammateId && allTeammates.some((t) => t.id === sb.teammateId))
      goTeammate(sb.teammateId, { computer: sb.id, tab: "terminal" })
    else
      void navigate({
        to: "/e/$env/sandboxes/$id",
        params: { env, id: sb.id },
        search: {},
      })
  }
  const go = (to: string) => void navigate({ to, params: { env } })
  const goComputers = () =>
    void navigate({ to: "/e/$env/computers", params: { env } })
  const environmentMenu = (current: string): MenuEntry[] => [
    {
      label: "Computers",
      lead: <DesktopTowerIcon />,
      current: current === "computers",
      run: goComputers,
    },
    ...environmentPages.map((p) => ({
      label: p.label,
      lead: <EnvironmentIcon page={p.id} />,
      current: p.id === current,
      run: () => go(settingsPaths[p.id]),
    })),
  ]

  const teammateCrumb = (t: Teammate): Crumb => ({
    key: "teammate",
    switches: "Switch teammate",
    label: t.name,
    lead: <TeammateAvatar name={t.name} avatar={t.avatar} className="size-4" />,
    menu: [
      { heading: "Teammates" },
      ...allTeammates.map((x) => ({
        label: x.name,
        lead: (
          <TeammateAvatar name={x.name} avatar={x.avatar} className="size-4" />
        ),
        current: x.id === t.id,
        run: () => goTeammate(x.id),
      })),
      { rule: true as const },
      { heading: t.name },
      {
        label: "Threads",
        right: String(allChats.filter((c) => c.teammateId === t.id).length),
        run: () => goTeammate(t.id),
      },
      {
        label: "Settings",
        lead: <GearIcon />,
        current: search.profile === "settings",
        run: () => goTeammate(t.id, { profile: "settings" }),
      },
    ],
  })

  // A fork sits under the computer it came from, as on the Computers page.
  const computerItem = (
    sb: Sandbox,
    current: Sandbox,
    depth = 0,
    owner?: Teammate
  ) => ({
    label: sb.name,
    mono: true,
    lead:
      depth > 0 ? <GitForkIcon className="text-muted-foreground" /> : dot(sb),
    inset: depth > 0,
    current: sb.id === current.id,
    right: owner?.name ?? phaseDot(phase(sb)).label,
    run: () => goComputer(sb),
  })

  /** A computer of the user's own, as the crumb after Computers; its menu has the others. */
  const plainCrumb = (sb: Sandbox): Crumb => {
    const others = allSandboxes.filter(
      (s) => !s.teammateId || !allTeammates.some((t) => t.id === s.teammateId)
    )
    return {
      key: "computer",
      switches: "Switch computer",
      label: sb.name,
      mono: true,
      lead: dot(sb),
      menu: [
        { heading: "Your computers" },
        ...forkTree(others).map(({ sandbox: s, depth }) =>
          computerItem(s, sb, depth)
        ),
        { rule: true },
        {
          label: "All computers",
          lead: <DesktopTowerIcon />,
          run: goComputers,
        },
      ],
    }
  }
  const computersCrumb: Crumb = {
    key: "computers",
    label: "Computers",
    lead: <DesktopTowerIcon />,
    run: goComputers,
  }
  let chip: Chip | undefined

  const crumbs: Crumb[] = (() => {
    if (routeId === "/e/$env/")
      return [{ key: "home", label: "Home", lead: <TrayIcon /> }]
    if (routeId === "/e/$env/computers")
      return [
        { key: "env", label: "Environment" },
        {
          key: "page",
          switches: "Switch page",
          label: "Computers",
          lead: <DesktopTowerIcon />,
          menu: environmentMenu("computers"),
        },
      ]
    const page = environmentPages.find(
      (p) => routeId === `/e/$env/${p.id}` || routeId === `/e/$env/${p.id}/`
    )
    if (page || routeId === "/e/$env/memory/conflicts/$id")
      return [
        { key: "env", label: "Environment" },
        {
          key: "page",
          switches: "Switch page",
          label: page?.label ?? "Memory",
          lead: <EnvironmentIcon page={page?.id ?? "memory"} />,
          menu: environmentMenu(page?.id ?? "memory"),
        },
        ...(page ? [] : [{ key: "item", label: "Conflict" }]),
      ]
    if (
      routeId.startsWith("/e/$env/settings") ||
      routeId === "/e/$env/builds/$id"
    ) {
      const all = settingsGroups.flatMap<{ id: SettingsId; label: string }>(
        (g) => [...g.sections]
      )
      const section =
        routeId === "/e/$env/builds/$id"
          ? all.find((s) => s.id === "templates")
          : all.find((s) => routeId === `/e/$env/settings/${s.id}`)
      return [
        { key: "settings", label: "Settings", lead: <GearIcon /> },
        {
          key: "section",
          switches: "Switch section",
          label: section?.label ?? "General",
          menu: settingsGroups.flatMap((g) => [
            { heading: g.title },
            ...g.sections.map((s) => ({
              label: s.label,
              current: s.id === (section?.id ?? "general"),
              run: () => go(settingsPaths[s.id]),
            })),
          ]),
        },
        ...(routeId === "/e/$env/builds/$id"
          ? [{ key: "item", label: "Build" }]
          : []),
      ]
    }
    if (routeId === "/e/$env/teammates/$id") {
      const t = allTeammates.find((x) => x.id === params.id)
      if (!t) return []
      const head = teammateCrumb(t)
      if (search.profile === "settings") {
        const row = teammateSettings
          .flatMap((g) => g.rows)
          .find((r) => r.id === search.open)
        return [
          head,
          {
            key: "settings",
            switches: "Switch section",
            label: "Settings",
            lead: <GearIcon />,
            menu: teammateSettings.flatMap((g, i) => [
              ...(i ? [{ rule: true as const }] : []),
              ...(g.title ? [{ heading: g.title }] : []),
              ...g.rows.map(({ id, label }) => ({
                label: id === "delete" ? `Delete ${t.name}` : label,
                current: id === search.open,
                run: () => goTeammate(t.id, { profile: "settings", open: id }),
              })),
            ]),
          },
          ...(row
            ? [
                {
                  key: "item",
                  label: row.id === "delete" ? `Delete ${t.name}` : row.label,
                },
              ]
            : []),
        ]
      }
      if (search.profile === "tasks" || search.profile === "memory")
        return [
          head,
          {
            key: "profile",
            label: search.profile === "tasks" ? "Tasks" : "Memory",
          },
        ]
      const owned = allSandboxes.filter((s) => s.teammateId === t.id)
      const mine = [...allChats]
        .filter((c) => c.teammateId === t.id)
        .sort((a, b) => b.updatedAt.localeCompare(a.updatedAt))
      const thread = mine.find((c) => c.id === search.thread)
      const sb = screenComputer(
        owned,
        mine,
        search.computer,
        thread?.sandboxId,
        lastComputer(env, t.id)
      )
      // The home (no thread, the screen folded) lists its computers itself.
      if (sb && (search.thread || (search.tab && !search.fold)))
        chip = {
          sandbox: sb,
          menu: [
            { heading: `${t.name}'s computers` },
            ...forkTree(owned).map(({ sandbox: s, depth }) => ({
              ...computerItem(s, sb, depth),
              right:
                s.id === thread?.sandboxId && s.id !== sb.id
                  ? "this thread's"
                  : phaseDot(phase(s)).label,
              run: () =>
                void navigate({
                  to: "/e/$env/teammates/$id",
                  params: { env, id: t.id },
                  search: (prev: TeammateSearch) => ({
                    ...prev,
                    computer: s.id,
                    fold: undefined,
                    tab: prev.tab ?? "terminal",
                  }),
                }),
            })),
            { rule: true },
            {
              label: `Give ${t.name} another computer…`,
              lead: <PlusIcon />,
              run: () => setGiving(t.id),
            },
            {
              label: "All computers",
              lead: <DesktopTowerIcon />,
              run: goComputers,
            },
          ],
        }
      if (!search.thread) return [head]
      return [
        head,
        {
          key: "thread",
          switches: "Switch thread",
          label: thread ? chatTitle(thread) : "Thread",
          menu: [
            { heading: `${t.name}'s threads` },
            ...mine.slice(0, menuThreads).map((c) => ({
              label: chatTitle(c),
              lead: <ThreadDot status={chatStatus(c, 0)} />,
              current: c.id === search.thread,
              run: () => goTeammate(t.id, { thread: c.id }),
            })),
            ...(mine.length > menuThreads
              ? [
                  { rule: true as const },
                  {
                    label: `All ${mine.length} threads`,
                    run: () => goTeammate(t.id),
                  },
                ]
              : []),
          ],
        },
      ]
    }
    if (routeId === "/e/$env/sandboxes/$id") {
      const sb = allSandboxes.find((s) => s.id === params.id)
      return sb ? [computersCrumb, plainCrumb(sb)] : [computersCrumb]
    }
    const named: Record<string, string> = {
      "/e/$env/teammates/": "Teammates",
      "/e/$env/chats/": "Chats",
      "/e/$env/chats/new": "New chat",
      "/e/$env/chats/$chat": "Chat",
      "/e/$env/git/reviews/$id": "Git review",
      "/e/$env/merges/$merge": "Memory merge",
    }
    return named[routeId] ? [{ key: "page", label: named[routeId] }] : []
  })()

  // On a narrow window the row scrolls; keep its end, where you are, in view.
  const row = useRef<HTMLOListElement>(null)
  const trail = [...crumbs.map((c) => c.label), chip?.sandbox.name].join(
    "\u0000"
  )
  // The row narrows too, as the approvals button comes and goes.
  useLayoutEffect(() => {
    const ol = row.current
    if (!ol) return
    const end = () => (ol.scrollLeft = ol.scrollWidth)
    end()
    const resized = new ResizeObserver(end)
    resized.observe(ol)
    return () => resized.disconnect()
  }, [trail])

  return (
    <nav
      aria-label="Where you are"
      data-slot="breadcrumbs"
      className="flex h-(--wb-header) shrink-0 items-center gap-1 border-b px-1 text-xs font-medium md:px-2.5"
    >
      <SidebarTrigger className="md:hidden" />
      <ol
        ref={row}
        className="flex h-full min-w-0 flex-1 [scrollbar-width:none] items-center gap-px overflow-x-auto whitespace-nowrap"
      >
        {crumbs.map((c, i) => {
          const last = i === crumbs.length - 1
          const inner = (
            <>
              {c.lead}
              <span className={cn("truncate", c.mono && "font-mono")}>
                {c.label}
              </span>
            </>
          )
          const cls = cn(
            "inline-flex h-6 max-w-80 shrink-0 items-center gap-1.5 rounded-md px-1.5 text-muted-foreground [&_svg]:size-3.5 [&_svg]:shrink-0",
            last && "font-semibold text-foreground"
          )
          return (
            <Fragment key={c.key}>
              {i > 0 && (
                <li
                  aria-hidden
                  className="shrink-0 px-px text-muted-foreground/55"
                >
                  ›
                </li>
              )}
              <li
                className="flex shrink-0"
                aria-current={last ? "page" : undefined}
              >
                {c.menu ? (
                  <DropdownMenu>
                    <DropdownMenuTrigger
                      aria-label={`${c.switches}: ${c.label}`}
                      className={cn(
                        cls,
                        "outline-none hover:bg-accent hover:text-foreground focus-visible:ring-2 focus-visible:ring-ring/50 data-popup-open:bg-accent data-popup-open:text-foreground"
                      )}
                    >
                      {inner}
                      <CaretDownIcon className="size-2.5! opacity-70" />
                    </DropdownMenuTrigger>
                    <CrumbMenu entries={c.menu} />
                  </DropdownMenu>
                ) : c.run ? (
                  <button
                    type="button"
                    onClick={c.run}
                    className={cn(
                      cls,
                      "outline-none hover:bg-accent hover:text-foreground focus-visible:ring-2 focus-visible:ring-ring/50"
                    )}
                  >
                    {inner}
                  </button>
                ) : (
                  <span className={cls}>{inner}</span>
                )}
              </li>
            </Fragment>
          )
        })}
        {chip && (
          <li className="ml-1.5 flex shrink-0">
            <DropdownMenu>
              <DropdownMenuTrigger
                aria-label={`Switch computer: ${chip.sandbox.name}`}
                className="inline-flex h-6 max-w-64 items-center gap-1.5 rounded-full border px-2 text-muted-foreground outline-none hover:bg-accent hover:text-foreground focus-visible:ring-2 focus-visible:ring-ring/50 data-popup-open:bg-accent data-popup-open:text-foreground"
              >
                <span>on</span>
                {dot(chip.sandbox)}
                <span className="truncate font-mono text-foreground">
                  {chip.sandbox.name}
                </span>
                <CaretDownIcon className="size-2.5 shrink-0 opacity-70" />
              </DropdownMenuTrigger>
              <CrumbMenu entries={chip.menu} />
            </DropdownMenu>
          </li>
        )}
      </ol>
      {/* On a phone the approvals wait up here, where they cover nothing. */}
      <ApprovalsButton className="sm:hidden" />
      <Button
        variant="ghost"
        size="icon-sm"
        className="md:hidden"
        aria-label="Search"
        onClick={openPalette}
      >
        <MagnifyingGlassIcon />
      </Button>
      {giving && (
        <CreateSandboxDialog
          env={env}
          teammate={giving}
          open
          onOpenChange={(open) => !open && setGiving(undefined)}
        />
      )}
    </nav>
  )
}

function CrumbMenu({ entries }: { entries: MenuEntry[] }) {
  // Headings belong to the items under them, as a group each.
  const groups: { heading?: string; items: MenuEntry[]; rule?: boolean }[] = []
  for (const e of entries) {
    if ("heading" in e) groups.push({ heading: e.heading, items: [] })
    else if ("rule" in e) groups.push({ items: [], rule: true })
    else {
      if (!groups.length) groups.push({ items: [] })
      groups[groups.length - 1].items.push(e)
    }
  }
  return (
    <DropdownMenuContent className="w-auto max-w-80 min-w-48">
      {groups.map((g, i) => (
        <Fragment key={i}>
          {g.rule && <DropdownMenuSeparator />}
          <DropdownMenuGroup>
            {g.heading && <DropdownMenuLabel>{g.heading}</DropdownMenuLabel>}
            {g.items.map(
              (e, j) =>
                "run" in e && (
                  <DropdownMenuItem
                    key={j}
                    onClick={e.run}
                    className={cn(e.inset && "pl-5")}
                  >
                    {e.lead}
                    <span
                      className={cn(
                        "min-w-0 flex-1 truncate",
                        e.mono && "font-mono"
                      )}
                    >
                      {e.label}
                    </span>
                    {e.current ? (
                      <CheckIcon className="text-muted-foreground" />
                    ) : (
                      e.right && (
                        <span className="text-muted-foreground">{e.right}</span>
                      )
                    )}
                  </DropdownMenuItem>
                )
            )}
          </DropdownMenuGroup>
        </Fragment>
      ))}
    </DropdownMenuContent>
  )
}
