import { useEffect, type ReactNode } from "react"
import { Link, useNavigate, useSearch } from "@tanstack/react-router"
import {
  BrainIcon,
  BrowserIcon,
  ChatsCircleIcon,
  DesktopIcon,
  GitBranchIcon,
  GlobeIcon,
  MoonIcon,
  SunIcon,
  type Icon,
} from "@phosphor-icons/react"
import { TeammateAvatar } from "@/components/teammate-avatar"
import { cn } from "@/lib/utils"
import type { Presence, Status, TermSegment, Tone } from "./data"
import "./mockups.css"

export type Direction = "a" | "b"
export type Theme = "system" | "light" | "dark"
export const screens = ["home", "workspace", "dashboard", "approvals"] as const
export type Screen = (typeof screens)[number]

export const screenTitles: Record<Screen, string> = {
  home: "Home",
  workspace: "Sandbox workspace",
  dashboard: "Sandbox dashboard",
  approvals: "Approval cards",
}

export type MockupSearch = { theme?: Theme; menu?: boolean; bare?: boolean }

/** Applies a direction's tokens and the chosen theme to <html> while a mockup is shown. */
export function useMockupTheme(direction: Direction, theme: Theme = "system") {
  useEffect(() => {
    const root = document.documentElement
    const wasDark = root.classList.contains("dark")
    const media = window.matchMedia("(prefers-color-scheme: dark)")
    const apply = () =>
      root.classList.toggle(
        "dark",
        theme === "dark" || (theme === "system" && media.matches)
      )
    root.dataset.direction = direction
    apply()
    media.addEventListener("change", apply)
    return () => {
      media.removeEventListener("change", apply)
      delete root.dataset.direction
      root.classList.toggle("dark", wasDark)
    }
  }, [direction, theme])
}

export const toneIcons: Record<Tone, Icon> = {
  network: GlobeIcon,
  git: GitBranchIcon,
  browser: BrowserIcon,
  memory: BrainIcon,
  agent: ChatsCircleIcon,
}

export const toneLabels: Record<Tone, string> = {
  network: "Network",
  git: "Git",
  browser: "Browser",
  memory: "Memory",
  agent: "Agent",
}

export const statusLabels: Record<Status, string> = {
  running: "Running",
  waiting: "Waiting for you",
  failed: "Failed",
  stopped: "Stopped",
}

export const presenceLabels: Record<Presence, string> = {
  working: "Working",
  waiting: "Waiting for you",
  idle: "Idle",
}

/** A small dot in the fixed status colors. */
export function StatusDot({
  status,
  className,
}: {
  status: Status
  className?: string
}) {
  return (
    <span
      data-status={status}
      className={cn(
        "inline-block size-2 shrink-0 rounded-full bg-(--status)",
        status === "stopped" &&
          "bg-transparent ring-1 ring-(--status) ring-inset",
        className
      )}
    />
  )
}

/** A teammate's avatar with a presence dot on its corner. */
export function PresenceAvatar({
  name,
  presence,
  className,
  dotClassName,
}: {
  name: string
  presence: Presence
  className?: string
  dotClassName?: string
}) {
  return (
    <span className="relative inline-flex shrink-0">
      <TeammateAvatar name={name} className={className} />
      <span
        data-presence={presence}
        className={cn(
          "absolute -right-0.5 -bottom-0.5 size-2.5 rounded-full bg-(--status) ring-2 ring-(--presence-ring,var(--background))",
          dotClassName
        )}
        title={presenceLabels[presence]}
      />
    </span>
  )
}

export function TermLine({ line }: { line: TermSegment[] }) {
  return (
    <div className="min-h-[1.5em] whitespace-pre-wrap">
      {line.map((s, i) => (
        <span
          key={i}
          className={cn(
            s.role === "prompt" && "font-semibold text-(--term-prompt)",
            s.role === "path" && "text-(--term-path)",
            s.role === "dim" && "opacity-60",
            s.role === "ok" && "text-(--term-ok)",
            s.role === "warn" && "text-(--term-warn)",
            s.role === "accent" && "text-(--term-accent) underline",
            s.role === "add" && "text-(--term-add)",
            s.role === "del" && "text-(--term-del)"
          )}
        >
          {s.text}
        </span>
      ))}
    </div>
  )
}

export function Cursor() {
  return (
    <span className="inline-block h-[1.1em] w-[0.6em] translate-y-[0.15em] animate-pulse bg-current opacity-70" />
  )
}

/** System / light / dark, as the N4 brief asks for. Drives the mockup's `theme` search param. */
export function ThemeToggle({
  className,
  itemClassName,
}: {
  className?: string
  itemClassName?: string
}) {
  const search: MockupSearch = useSearch({ strict: false })
  const navigate = useNavigate()
  const current = search.theme ?? "system"
  const options: { value: Theme; icon: Icon; label: string }[] = [
    { value: "system", icon: DesktopIcon, label: "System" },
    { value: "light", icon: SunIcon, label: "Light" },
    { value: "dark", icon: MoonIcon, label: "Dark" },
  ]
  return (
    <div role="radiogroup" aria-label="Theme" className={cn("flex", className)}>
      {options.map((o) => (
        <button
          key={o.value}
          type="button"
          role="radio"
          aria-checked={current === o.value}
          title={o.label}
          data-active={current === o.value || undefined}
          className={cn(
            "inline-flex items-center justify-center",
            itemClassName
          )}
          onClick={() =>
            navigate({
              to: ".",
              search: (prev: MockupSearch) => ({
                ...prev,
                theme: o.value === "system" ? undefined : o.value,
              }),
              replace: true,
            })
          }
        >
          <o.icon className="size-3.5" />
        </button>
      ))}
    </div>
  )
}

/** A floating switcher between directions and screens; hidden with `?bare=true` for screenshots. */
export function MockupSwitcher({
  direction,
  screen,
}: {
  direction: Direction
  screen: Screen
}) {
  const search: MockupSearch = useSearch({ strict: false })
  if (search.bare) return null
  const pill = (active: boolean) =>
    cn(
      "rounded-full px-2 py-0.5",
      active ? "bg-white text-neutral-900" : "text-neutral-300 hover:text-white"
    )
  return (
    <nav className="fixed bottom-3 left-1/2 z-50 flex -translate-x-1/2 items-center gap-1 rounded-full bg-neutral-900/90 p-1 font-sans text-[11px] text-white shadow-lg backdrop-blur max-md:top-2 max-md:bottom-auto">
      <Link to="/mockups" className="px-2 text-neutral-400 hover:text-white">
        Mockups
      </Link>
      {(["a", "b"] as const).map((d) => (
        <Link
          key={d}
          to="/mockups/$direction/$screen"
          params={{ direction: d, screen }}
          search={(prev: MockupSearch) => prev}
          className={pill(d === direction)}
        >
          {d.toUpperCase()}
        </Link>
      ))}
      <span className="mx-1 h-3 w-px bg-white/20" />
      {screens.map((s) => (
        <Link
          key={s}
          to="/mockups/$direction/$screen"
          params={{ direction, screen: s }}
          search={(prev: MockupSearch) => prev}
          className={cn(pill(s === screen), "capitalize max-sm:px-1.5")}
        >
          {s}
        </Link>
      ))}
    </nav>
  )
}

/** Wraps a screen; `children` is the direction's own shell. */
export function MockupPage({
  direction,
  screen,
  children,
}: {
  direction: Direction
  screen: Screen
  children: ReactNode
}) {
  const search: MockupSearch = useSearch({ strict: false })
  useMockupTheme(direction, search.theme)
  return (
    <>
      {children}
      <MockupSwitcher direction={direction} screen={screen} />
    </>
  )
}
