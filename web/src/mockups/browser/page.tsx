import { useEffect, useState } from "react"
import { Link, useNavigate, useSearch } from "@tanstack/react-router"
import {
  BrainIcon,
  ChatsCircleIcon,
  GearSixIcon,
  GlobeIcon,
  KeyIcon,
  SquaresFourIcon,
  type Icon,
} from "@phosphor-icons/react"
import { StatusDot } from "@/components/status-dot"
import { PresenceDot } from "@/components/teammate-avatar"
import { cn } from "@/lib/utils"
import { sandboxes, teammate } from "./data"
import {
  ConversationPlace,
  TeammatePlace,
  WorkspacePlace,
  placeNames,
  places,
  presenceOf,
  type Place,
} from "./places"
import {
  presetNames,
  presets,
  useBrowserSim,
  type Preset,
  type Sim,
} from "./sim"
import "../teammate/teammate.css"

export type BrowserMockupSearch = {
  place?: Place
  state?: Preset
  theme?: "light" | "dark"
  bare?: boolean
}

export const browserPitch =
  "One browser window for a teammate, the same on its page, beside a sandbox's terminal and in the conversation: tabs for throwaway sessions, an address bar that takes the wheel, one line that says who drives, Finn's cursor and questions on the page, and a step log you can replay."

/** Light or dark from the search; the system's otherwise. */
function useTheme(theme?: "light" | "dark") {
  useEffect(() => {
    const root = document.documentElement
    const was = root.classList.contains("dark")
    const media = window.matchMedia("(prefers-color-scheme: dark)")
    const apply = () =>
      root.classList.toggle("dark", theme ? theme === "dark" : media.matches)
    apply()
    media.addEventListener("change", apply)
    return () => {
      media.removeEventListener("change", apply)
      root.classList.toggle("dark", was)
    }
  }, [theme])
}

/** The browser revamp: Finn's browser in three places, from one of four starting states. */
export function BrowserMockup() {
  const search: BrowserMockupSearch = useSearch({ strict: false })
  useTheme(search.theme)
  const preset = search.state ?? "agent"
  // A new starting state starts the run over.
  return <Run key={preset} preset={preset} search={search} />
}

function Run({
  preset,
  search,
}: {
  preset: Preset
  search: BrowserMockupSearch
}) {
  const navigate = useNavigate()
  const [sim, dispatch] = useBrowserSim(preset)
  const place = search.place ?? "teammate"
  const go = (p: Place) =>
    navigate({
      to: "/mockups/browser",
      search: { ...search, place: p },
      replace: true,
    })
  const props = { sim, dispatch, go }
  return (
    <div className="flex h-svh bg-background text-foreground">
      <FakeSidebar sim={sim} place={place} />
      <main className="flex min-w-0 flex-1 flex-col">
        {place === "teammate" ? (
          <TeammatePlace {...props} />
        ) : place === "workspace" ? (
          <WorkspacePlace {...props} />
        ) : (
          <ConversationPlace {...props} />
        )}
      </main>
      {!search.bare && <Switcher place={place} preset={preset} />}
    </div>
  )
}

function FakeSidebar({ sim, place }: { sim: Sim; place: Place }) {
  const nav: [Icon, string][] = [
    [SquaresFourIcon, "Home"],
    [ChatsCircleIcon, "Chats"],
    [GlobeIcon, "Network"],
    [KeyIcon, "Secrets"],
    [BrainIcon, "Memory"],
  ]
  const item = "flex h-7 items-center gap-2 rounded-md px-2"
  return (
    <aside className="flex w-56 shrink-0 flex-col border-r bg-sidebar text-[0.8125rem] max-lg:hidden">
      <div className="flex h-12 items-center gap-2 border-b px-3">
        <span className="inline-flex size-5 items-center justify-center rounded-[4px] bg-primary font-mono text-[0.625rem] text-primary-foreground">
          D
        </span>
        <span className="font-mono text-xs">Default</span>
      </div>
      <nav className="grid gap-0.5 p-2">
        {nav.map(([I, label]) => (
          <span key={label} className={cn(item, "text-sidebar-foreground/80")}>
            <I className="size-4" />
            {label}
          </span>
        ))}
      </nav>
      <p className="px-4 pt-3 pb-1 font-mono text-[0.625rem] tracking-wider text-muted-foreground uppercase">
        Teammates
      </p>
      <div className="grid gap-0.5 px-2">
        <span
          className={cn(
            item,
            place !== "workspace" && "bg-sidebar-accent font-medium"
          )}
        >
          <span className="text-xs leading-none">{teammate.avatar}</span>
          Finn
          <PresenceDot presence={presenceOf(sim)} className="ml-auto" />
        </span>
        <span className={cn(item, "text-sidebar-foreground/80")}>
          <span className="text-xs leading-none">⬡</span>
          Iris
          <PresenceDot presence="idle" className="ml-auto" />
        </span>
      </div>
      <p className="px-4 pt-4 pb-1 font-mono text-[0.625rem] tracking-wider text-muted-foreground uppercase">
        Sandboxes
      </p>
      <div className="grid gap-0.5 px-2">
        {sandboxes.map((s, i) => (
          <span
            key={s}
            className={cn(
              item,
              "font-mono text-xs",
              place === "workspace" && i === 0
                ? "bg-sidebar-accent font-medium"
                : "text-sidebar-foreground/80"
            )}
          >
            <StatusDot status={i === 0 ? "running" : "stopped"} compact />
            {s}
          </span>
        ))}
      </div>
      <div className="mt-auto border-t p-2">
        <span className={cn(item, "text-sidebar-foreground/80")}>
          <GearSixIcon className="size-4" />
          Settings
        </span>
      </div>
    </aside>
  )
}

/** The floating pill: place, starting state and theme. Hidden with `?bare=true`. */
function Switcher({ place, preset }: { place: Place; preset: Preset }) {
  const search: BrowserMockupSearch = useSearch({ strict: false })
  const navigate = useNavigate()
  const set = (patch: Partial<BrowserMockupSearch>) =>
    navigate({
      to: "/mockups/browser",
      search: { ...search, ...patch },
      replace: true,
    })
  const pill = (active: boolean) =>
    cn(
      "rounded-full px-2 py-0.5",
      active ? "bg-white text-neutral-900" : "text-neutral-300 hover:text-white"
    )
  const [open, setOpen] = useState(true)
  if (!open)
    return (
      <button
        type="button"
        onClick={() => setOpen(true)}
        className="fixed bottom-3 left-3 z-50 rounded-full bg-neutral-900/90 px-3 py-1 text-[11px] text-white shadow-lg"
      >
        Mockup controls
      </button>
    )
  const sep = <span className="mx-1 h-3 w-px bg-white/20" />
  return (
    <nav className="fixed bottom-3 left-1/2 z-50 flex max-w-[calc(100vw-1rem)] -translate-x-1/2 flex-wrap items-center justify-center gap-1 rounded-full bg-neutral-900/90 p-1 font-sans text-[11px] text-white shadow-lg backdrop-blur max-md:top-2 max-md:bottom-auto max-md:rounded-2xl">
      <Link to="/mockups" className="px-2 text-neutral-400 hover:text-white">
        Mockups
      </Link>
      {places.map((p) => (
        <button
          key={p}
          type="button"
          className={pill(p === place)}
          onClick={() => set({ place: p })}
        >
          {placeNames[p]}
        </button>
      ))}
      {sep}
      {presets.map((p) => (
        <button
          key={p}
          type="button"
          title="Start over from this state"
          className={pill(p === preset)}
          onClick={() => set({ state: p })}
        >
          {presetNames[p]}
        </button>
      ))}
      {sep}
      {(["light", "dark"] as const).map((t) => (
        <button
          key={t}
          type="button"
          className={cn(pill(search.theme === t), "capitalize")}
          onClick={() => set({ theme: search.theme === t ? undefined : t })}
        >
          {t}
        </button>
      ))}
      <button
        type="button"
        aria-label="Hide the controls"
        className="px-2 text-neutral-400 hover:text-white"
        onClick={() => setOpen(false)}
      >
        ×
      </button>
    </nav>
  )
}
