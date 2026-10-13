import type { ReactNode } from "react"
import { Link, Outlet, createFileRoute } from "@tanstack/react-router"
import {
  CpuIcon,
  GearSixIcon,
  GitBranchIcon,
  GlobeIcon,
  PlugsIcon,
  StackIcon,
} from "@phosphor-icons/react"

export const Route = createFileRoute("/e/$env/settings")({
  component: SettingsLayout,
})

/**
 * Settings sections, each a page of its own. A side column on wide screens, its title row as
 * tall as the page header so their rules meet; on phones a row of links that scrolls sideways.
 */
function SettingsLayout() {
  const { env } = Route.useParams()
  return (
    <div className="flex min-h-0 flex-1 flex-col md:flex-row">
      <div className="shrink-0 border-b md:w-48 md:border-r md:border-b-0">
        <p className="hidden h-12 items-center border-b px-4 font-mono text-[0.625rem] tracking-wider text-muted-foreground uppercase md:flex">
          Settings
        </p>
        <nav
          aria-label="Settings"
          className="flex gap-1 overflow-x-auto p-2 md:flex-col md:overflow-visible"
        >
          <SettingsLink
            to="/e/$env/settings/general"
            env={env}
            icon={<GearSixIcon />}
          >
            General
          </SettingsLink>
          <SettingsLink
            to="/e/$env/settings/providers"
            env={env}
            icon={<PlugsIcon />}
          >
            Providers
          </SettingsLink>
          <SettingsLink
            to="/e/$env/settings/git"
            env={env}
            icon={<GitBranchIcon />}
          >
            Git
          </SettingsLink>
          <SettingsLink
            to="/e/$env/settings/network"
            env={env}
            icon={<GlobeIcon />}
          >
            Network
          </SettingsLink>
          <SettingsLink
            to="/e/$env/settings/templates"
            env={env}
            icon={<StackIcon />}
          >
            Templates
          </SettingsLink>
          <SettingsLink
            to="/e/$env/settings/runtime"
            env={env}
            icon={<CpuIcon />}
          >
            Runtime
          </SettingsLink>
        </nav>
      </div>
      <div className="flex min-h-0 min-w-0 flex-1 flex-col">
        <Outlet />
      </div>
    </div>
  )
}

function SettingsLink({
  to,
  env,
  icon,
  children,
}: {
  to:
    | "/e/$env/settings/general"
    | "/e/$env/settings/providers"
    | "/e/$env/settings/git"
    | "/e/$env/settings/network"
    | "/e/$env/settings/templates"
    | "/e/$env/settings/runtime"
  env: string
  icon: ReactNode
  children: string
}) {
  return (
    <Link
      to={to}
      params={{ env }}
      className="flex h-7 shrink-0 items-center gap-2 rounded-md px-2 text-[0.8125rem] text-muted-foreground hover:bg-muted hover:text-foreground [&_svg]:size-3.5"
      activeProps={{ className: "bg-muted font-medium text-foreground" }}
    >
      {icon}
      {children}
    </Link>
  )
}
