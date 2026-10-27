import { useEffect, useRef, useState } from "react"
import {
  Link,
  Outlet,
  createFileRoute,
  useLocation,
  useMatchRoute,
} from "@tanstack/react-router"
import { EnvironmentLabel } from "@/components/page-header"
import { settingsGroups, settingsPaths } from "@/components/settings/link"
import { Input } from "@/components/ui/input"
import { SidebarTrigger } from "@/components/ui/sidebar"
import { cn } from "@/lib/utils"

export const Route = createFileRoute("/e/$env/settings")({
  component: SettingsLayout,
})

/**
 * Settings as one page: a nav of sections grouped by what they belong to (this environment,
 * the accounts Studio uses, this computer) with a search over them, and the section in view.
 * On phones the nav is a row above the section that scrolls sideways.
 */
function SettingsLayout() {
  const { env } = Route.useParams()
  const matchRoute = useMatchRoute()
  const [query, setQuery] = useState("")
  const nav = useRef<HTMLElement>(null)
  const pathname = useLocation({ select: (l) => l.pathname })
  // On phones the nav scrolls sideways; keep the open section in view.
  useEffect(() => {
    const el = nav.current
    const link = el?.querySelector<HTMLElement>("[aria-current=page]")
    if (!el || !link || el.scrollWidth <= el.clientWidth) return
    const left = link.offsetLeft - el.offsetLeft
    if (
      left < el.scrollLeft ||
      left + link.offsetWidth > el.scrollLeft + el.clientWidth
    )
      el.scrollLeft = left - 8
  }, [pathname])
  const q = query.trim().toLowerCase()
  const groups = settingsGroups.map((g) => ({
    ...g,
    sections: g.sections.filter(
      (s) => !q || `${s.label} ${s.keywords}`.toLowerCase().includes(q)
    ),
  }))

  return (
    <div className="flex min-h-0 flex-1 flex-col md:flex-row">
      <div className="flex shrink-0 flex-col border-b md:w-56 md:border-r md:border-b-0">
        <header
          data-slot="page-header"
          className="flex h-14 shrink-0 items-center gap-2 px-3 md:px-4"
        >
          <SidebarTrigger className="md:hidden" />
          <h1 className="text-sm">Settings</h1>
          <EnvironmentLabel env={env} />
        </header>
        <div className="p-2 pb-0 max-md:hidden">
          <Input
            type="search"
            aria-label="Search settings"
            placeholder="Search settings"
            value={query}
            onChange={(e) => setQuery(e.target.value)}
            className="h-8"
          />
        </div>
        <nav
          ref={nav}
          aria-label="Settings"
          className="flex gap-1 overflow-x-auto p-2 md:min-h-0 md:flex-1 md:flex-col md:overflow-y-auto"
        >
          {groups.map(
            (g) =>
              g.sections.length > 0 && (
                <div key={g.title} className="contents md:block md:pb-3">
                  <p className="px-2 pt-1 pb-1.5 section-label text-muted-foreground max-md:hidden">
                    {g.title}
                  </p>
                  {g.sections.map((s) => {
                    const active = !!matchRoute({
                      to: settingsPaths[s.id],
                      params: { env },
                    })
                    return (
                      <Link
                        key={s.id}
                        to={settingsPaths[s.id]}
                        params={{ env }}
                        aria-current={active ? "page" : undefined}
                        className={cn(
                          "flex h-7 shrink-0 items-center rounded-md px-2 font-control text-title whitespace-nowrap text-muted-foreground hover:bg-foreground/5 hover:text-foreground",
                          active &&
                            "bg-foreground/8 font-medium text-foreground"
                        )}
                      >
                        {s.label}
                      </Link>
                    )
                  })}
                </div>
              )
          )}
          {groups.every((g) => g.sections.length === 0) && (
            <p className="px-2 text-xs text-muted-foreground">
              No setting matches.
            </p>
          )}
        </nav>
      </div>
      <div className="flex min-h-0 min-w-0 flex-1 flex-col">
        <Outlet />
      </div>
    </div>
  )
}
