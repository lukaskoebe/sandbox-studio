import { useEffect, useState, type ReactNode } from "react"
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
import { TeammateAvatar } from "@/components/teammate-avatar"
import { cn } from "@/lib/utils"
import { finn, type MockPresence } from "./data"
import "./teammate.css"

export const concepts = ["dm", "desk", "journal"] as const
export type Concept = (typeof concepts)[number]

export const conceptInfo: Record<Concept, { name: string; pitch: string }> = {
  dm: {
    name: "Conversation",
    pitch:
      "The teammate page is one ongoing conversation, like a DM with a colleague. Messages, what it did, what it learned and what it needs from you sit in one thread; a profile rail shows what it is doing now and what it knows.",
  },
  desk: {
    name: "Desk",
    pitch:
      "Visiting a colleague's desk: who they are and what they are doing right now, live, with big actions to hand something over, and a board of everything between you two.",
  },
  journal: {
    name: "Journal",
    pitch:
      "The teammate writes a daily journal from its sessions: what it did, what it shipped, what it learned and where it is stuck. You reply in place, and its memory is a map you can explore.",
  },
}

export type TeammateMockupSearch = {
  theme?: "light" | "dark"
  presence?: MockPresence
  bare?: boolean
}

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

/**
 * A slim copy of the app around a concept: the sidebar with the teammate selected, and a
 * floating switcher for the concept, the teammate's presence and the theme.
 */
export function TeammateFrame({
  concept,
  children,
}: {
  concept: Concept
  children: (presence: MockPresence) => ReactNode
}) {
  const search: TeammateMockupSearch = useSearch({ strict: false })
  const presence = search.presence ?? "working"
  useTheme(search.theme)
  return (
    <div className="flex h-svh bg-background text-foreground">
      <FakeSidebar />
      <main className="flex min-w-0 flex-1 flex-col">{children(presence)}</main>
      {!search.bare && <Switcher concept={concept} />}
    </div>
  )
}

function FakeSidebar() {
  const nav: [Icon, string][] = [
    [SquaresFourIcon, "Home"],
    [ChatsCircleIcon, "Chats"],
    [GlobeIcon, "Network"],
    [KeyIcon, "Secrets"],
    [BrainIcon, "Memory"],
  ]
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
          <span
            key={label}
            className="flex h-7 items-center gap-2 rounded-md px-2 text-sidebar-foreground/80"
          >
            <I className="size-4" />
            {label}
          </span>
        ))}
      </nav>
      <p className="px-4 pt-3 pb-1 font-mono text-[0.625rem] tracking-wider text-muted-foreground uppercase">
        Teammates
      </p>
      <div className="grid gap-0.5 px-2">
        <span className="flex h-7 items-center gap-2 rounded-md bg-sidebar-accent px-2 font-medium">
          <TeammateAvatar
            name={finn.name}
            avatar={finn.avatar}
            className="size-4"
          />
          Finn
        </span>
        <span className="flex h-7 items-center gap-2 rounded-md px-2 text-sidebar-foreground/80">
          <TeammateAvatar
            name="Iris"
            avatar="shape:hexagon"
            className="size-4"
          />
          Iris
        </span>
      </div>
      <div className="mt-auto border-t p-2">
        <span className="flex h-7 items-center gap-2 rounded-md px-2 text-sidebar-foreground/80">
          <GearSixIcon className="size-4" />
          Settings
        </span>
      </div>
    </aside>
  )
}

/** The floating pill: concepts, presence and theme. Hidden with `?bare=true` for screenshots. */
function Switcher({ concept }: { concept: Concept }) {
  const search: TeammateMockupSearch = useSearch({ strict: false })
  const navigate = useNavigate()
  const set = (patch: Partial<TeammateMockupSearch>) =>
    navigate({
      to: ".",
      search: (prev) => ({ ...prev, ...patch }),
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
  return (
    <nav className="fixed bottom-3 left-1/2 z-50 flex -translate-x-1/2 flex-wrap items-center justify-center gap-1 rounded-full bg-neutral-900/90 p-1 font-sans text-[11px] text-white shadow-lg backdrop-blur max-md:top-2 max-md:bottom-auto">
      <Link to="/mockups" className="px-2 text-neutral-400 hover:text-white">
        Mockups
      </Link>
      {concepts.map((c) => (
        <Link
          key={c}
          to="/mockups/teammate/$concept"
          params={{ concept: c }}
          search={true}
          className={pill(c === concept)}
        >
          {conceptInfo[c].name}
        </Link>
      ))}
      <span className="mx-1 h-3 w-px bg-white/20" />
      {(["idle", "working", "waiting"] as const).map((p) => (
        <button
          key={p}
          type="button"
          className={cn(
            pill((search.presence ?? "working") === p),
            "capitalize"
          )}
          onClick={() => set({ presence: p })}
        >
          {p}
        </button>
      ))}
      <span className="mx-1 h-3 w-px bg-white/20" />
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

export const presenceWords: Record<MockPresence, string> = {
  idle: "Free",
  working: "Working",
  waiting: "Waiting for you",
}

/**
 * The teammate's avatar with its presence as a ring: a slow breathing ring while it works,
 * a steady amber one while it waits for you, none while it is free.
 */
export function LiveAvatar({
  presence,
  size = "md",
}: {
  presence: MockPresence
  size?: "sm" | "md" | "lg"
}) {
  const box = { sm: "size-8", md: "size-11", lg: "size-20" }[size]
  const text = { sm: "text-base", md: "text-xl", lg: "text-4xl" }[size]
  return (
    <span className={cn("relative inline-flex shrink-0", box)}>
      {presence === "working" && (
        <span className="absolute -inset-1 animate-[tm-breathe_2.4s_ease-in-out_infinite] rounded-full bg-(--st-running)/25" />
      )}
      <span
        className={cn(
          "relative inline-flex size-full items-center justify-center rounded-full bg-muted ring-2",
          text,
          presence === "working"
            ? "ring-(--st-running)"
            : presence === "waiting"
              ? "ring-(--st-waiting)"
              : "ring-border"
        )}
      >
        {finn.avatar}
      </span>
    </span>
  )
}

/** A typing indicator: three dots that rise in turn. */
export function Typing() {
  return (
    <span className="inline-flex items-center gap-1" aria-label="typing">
      {[0, 1, 2].map((i) => (
        <span
          key={i}
          className="size-1.5 animate-[tm-dot_1.2s_ease-in-out_infinite] rounded-full bg-muted-foreground"
          style={{ animationDelay: `${i * 0.15}s` }}
        />
      ))}
    </span>
  )
}
