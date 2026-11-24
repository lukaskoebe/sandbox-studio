import { useEffect, useMemo, useState } from "react"
import { useNavigate } from "@tanstack/react-router"
import {
  ArrowRightIcon,
  ChatCircleIcon,
  DesktopTowerIcon,
  GitPullRequestIcon,
  type Icon,
} from "@phosphor-icons/react"
import type { Sandbox } from "@/lib/api/client"
import { chatTitle, type Chat } from "@/lib/chats"
import { type ScreenTab } from "@/lib/computer"
import {
  forgetVisit,
  placeAge,
  useVisits,
  type Place,
  type Visit,
} from "@/lib/places"
import { formatAge } from "@/lib/utils"

/** How many places Home offers. */
const shown = 3

const tabNames: Record<ScreenTab, string> = {
  terminal: "Terminal",
  browser: "Browser",
  preview: "Preview",
  files: "Files",
  changes: "Changes",
  details: "Details",
}

type Row = {
  visit: Visit
  icon: Icon
  title: string
  detail?: string
}

/**
 * The places last worked in from this browser, newest first: a thread comes back on the tab
 * it was left on. A thread, teammate or computer that is gone is left out and forgotten.
 */
export function ContinuePlaces({
  env,
  teammates,
  chats,
  sandboxes,
}: {
  env: string
  /** Undefined while loading: nothing shows until all three are in. */
  teammates?: { id: string; name: string }[]
  chats?: Chat[]
  sandboxes?: Sandbox[]
}) {
  const visits = useVisits(env)
  const navigate = useNavigate()
  const loaded = !!teammates && !!chats && !!sandboxes
  const [now] = useState(() => Date.now())

  const { rows, gone } = useMemo(() => {
    const found: Row[] = []
    const lost: Place[] = []
    if (!loaded) return { rows: found, gone: lost }
    for (const visit of visits) {
      if (now - visit.at > placeAge) continue
      const row = describeVisit(visit, teammates, chats, sandboxes)
      if (row) found.push(row)
      else lost.push(visit.place)
    }
    return { rows: found.slice(0, shown), gone: lost }
  }, [loaded, visits, teammates, chats, sandboxes, now])

  useEffect(() => {
    for (const place of gone) forgetVisit(env, place)
  }, [env, gone])

  if (rows.length === 0) return null
  return (
    <section
      aria-labelledby="continue"
      className="grid min-w-0 content-start gap-2.5"
    >
      <h2 id="continue" className="section-label text-muted-foreground">
        Continue where you left off
      </h2>
      <ul className="grid gap-1.5">
        {rows.map(({ visit, icon: RowIcon, title, detail }) => (
          <li key={visit.href} className="min-w-0">
            <a
              href={visit.href}
              onClick={(e) => {
                if (
                  e.button !== 0 ||
                  e.metaKey ||
                  e.ctrlKey ||
                  e.shiftKey ||
                  e.altKey
                )
                  return
                e.preventDefault()
                void navigate({ href: visit.href })
              }}
              className="group flex min-w-0 items-center gap-3 rounded-(--radius) border bg-card px-3 py-2 text-sm hover:bg-muted focus-visible:ring-2 focus-visible:ring-ring focus-visible:outline-none"
            >
              <RowIcon
                aria-hidden
                className="size-4 shrink-0 text-muted-foreground"
              />
              <span className="flex min-w-0 flex-1 items-baseline gap-2">
                <span className="truncate font-medium">{title}</span>
                {detail && (
                  <span className="truncate text-xs text-muted-foreground">
                    {detail}
                  </span>
                )}
              </span>
              <span
                className="shrink-0 font-mono text-xs text-muted-foreground"
                title={new Date(visit.at).toLocaleString()}
              >
                {formatAge(new Date(visit.at).toISOString(), now)}
              </span>
              <ArrowRightIcon
                aria-hidden
                className="size-3.5 shrink-0 text-muted-foreground opacity-0 transition-opacity group-hover:opacity-100 group-focus-visible:opacity-100"
              />
            </a>
          </li>
        ))}
      </ul>
    </section>
  )
}

/** A visit's row, or none when what it showed is gone. */
export function describeVisit(
  v: Visit,
  teammates: { id: string; name: string }[] = [],
  chats: Chat[] = [],
  sandboxes: Sandbox[] = []
): Row | undefined {
  const { place } = v
  const tab = "tab" in place && place.tab ? tabNames[place.tab] : undefined
  const join = (...parts: (string | undefined)[]) =>
    parts.filter(Boolean).join(" · ") || undefined
  switch (place.kind) {
    case "teammate": {
      const teammate = teammates.find((t) => t.id === place.id)
      if (!teammate) return undefined
      if (!place.thread)
        return {
          visit: v,
          icon: ChatCircleIcon,
          title: teammate.name,
          detail: join("Desk", tab),
        }
      const chat = chats.find((c) => c.id === place.thread)
      if (!chat) return undefined
      return {
        visit: v,
        icon: ChatCircleIcon,
        title: `“${chatTitle(chat)}”`,
        detail: join(teammate.name, tab),
      }
    }
    case "sandbox": {
      const sandbox = sandboxes.find((s) => s.id === place.id)
      if (!sandbox) return undefined
      return {
        visit: v,
        icon: DesktopTowerIcon,
        title: sandbox.name,
        detail: join("Computer", tab),
      }
    }
    case "review":
      return { visit: v, icon: GitPullRequestIcon, title: "Push review" }
    case "merge":
      return { visit: v, icon: GitPullRequestIcon, title: "Merge" }
    case "build":
      return { visit: v, icon: DesktopTowerIcon, title: "Template build" }
    case "conflict":
      return { visit: v, icon: ChatCircleIcon, title: "Memory conflict" }
  }
}
