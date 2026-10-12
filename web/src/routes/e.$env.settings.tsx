import { Link, Outlet, createFileRoute } from "@tanstack/react-router"

export const Route = createFileRoute("/e/$env/settings")({
  component: SettingsLayout,
})

/**
 * Settings sections, each a page of its own. A side list on wide screens; on phones a row of
 * links that scrolls sideways.
 */
function SettingsLayout() {
  const { env } = Route.useParams()
  return (
    <div className="flex min-h-0 flex-1 flex-col md:flex-row">
      <nav
        aria-label="Settings"
        className="flex shrink-0 gap-1 overflow-x-auto border-b p-2 md:w-48 md:flex-col md:overflow-visible md:border-r-0 md:border-b-0 md:p-3"
      >
        <p className="hidden px-2 pb-2 text-sm font-medium md:block">
          Settings
        </p>
        <SettingsLink to="/e/$env/settings/general" env={env}>
          General
        </SettingsLink>
        <SettingsLink to="/e/$env/settings/providers" env={env}>
          Providers
        </SettingsLink>
        <SettingsLink to="/e/$env/settings/git" env={env}>
          Git
        </SettingsLink>
        <SettingsLink to="/e/$env/settings/templates" env={env}>
          Templates
        </SettingsLink>
        <SettingsLink to="/e/$env/settings/runtime" env={env}>
          Runtime
        </SettingsLink>
      </nav>
      <div className="flex min-h-0 min-w-0 flex-1 flex-col">
        <Outlet />
      </div>
    </div>
  )
}

function SettingsLink({
  to,
  env,
  children,
}: {
  to:
    | "/e/$env/settings/general"
    | "/e/$env/settings/providers"
    | "/e/$env/settings/git"
    | "/e/$env/settings/templates"
    | "/e/$env/settings/runtime"
  env: string
  children: string
}) {
  return (
    <Link
      to={to}
      params={{ env }}
      className="flex h-8 shrink-0 items-center rounded-md px-2 text-sm text-muted-foreground hover:bg-muted hover:text-foreground"
      activeProps={{ className: "bg-muted font-medium text-foreground" }}
    >
      {children}
    </Link>
  )
}
