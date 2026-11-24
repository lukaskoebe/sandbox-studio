import { useEffect, useRef } from "react"
import {
  Link,
  Outlet,
  createFileRoute,
  useLocation,
  useMatchRoute,
} from "@tanstack/react-router"
import { EnvironmentLabel } from "@/components/page-header"
import { settingsGroups, settingsPaths } from "@/components/settings/link"
import { cn } from "@/lib/utils"

export const Route = createFileRoute("/e/$env/settings")({
  component: SettingsLayout,
})

/**
 * Settings as one page: a nav of sections grouped by what they apply to (all of Studio, the
 * environment on show, this browser), and the section in view. Only the Environment group
 * names the environment. On phones the nav is a row above the section that scrolls sideways.
 */
function SettingsLayout() {
  const { env } = Route.useParams()
  const matchRoute = useMatchRoute()
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

  return (
    <div className="flex min-h-0 flex-1 flex-col md:flex-row">
      <div className="flex shrink-0 flex-col border-b md:w-56 md:border-r md:border-b-0">
        <header
          data-slot="page-header"
          className="flex h-14 shrink-0 items-center gap-2 px-3 md:px-4"
        >
          <h1 className="text-sm">Settings</h1>
        </header>
        <nav
          ref={nav}
          aria-label="Settings"
          className="flex gap-1 overflow-x-auto p-2 md:min-h-0 md:flex-1 md:flex-col md:overflow-y-auto"
        >
          {settingsGroups.map(
            (g, i) =>
              g.sections.length > 0 && (
                <div key={g.title} className="contents md:block md:pb-3">
                  {i > 0 && (
                    <span
                      aria-hidden
                      className="my-1.5 w-px shrink-0 bg-border md:hidden"
                    />
                  )}
                  <p className="flex items-center gap-2 px-2 pt-1 pb-1.5 section-label text-muted-foreground max-md:hidden">
                    {g.title}
                    {g.scope === "environment" && (
                      <EnvironmentLabel env={env} />
                    )}
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
                          "flex h-7 shrink-0 items-center rounded-md px-2 font-control text-title whitespace-nowrap hover:bg-foreground/5",
                          active && "bg-foreground/8 font-medium"
                        )}
                      >
                        {s.label}
                      </Link>
                    )
                  })}
                </div>
              )
          )}
        </nav>
      </div>
      <div className="flex min-h-0 min-w-0 flex-1 flex-col">
        <Outlet />
      </div>
    </div>
  )
}
