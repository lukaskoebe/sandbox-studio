import { createContext, useContext, type ReactNode } from "react"
import { Link, useMatchRoute } from "@tanstack/react-router"
import { SquaresFourIcon, TerminalWindowIcon } from "@phosphor-icons/react"
import { PageHeader } from "@/components/page-header"
import { SandboxOptionsMenu } from "@/components/sandbox-options-menu"
import { StatusBadge } from "@/components/status-badge"
import { SandboxOwner } from "@/components/teammate-avatar"
import { type Sandbox } from "@/lib/api/client"
import { cn } from "@/lib/utils"

type SandboxPage = { env: string; sandbox: Sandbox }

const SandboxPageContext = createContext<SandboxPage | null>(null)

/** Gives the sandbox's dashboard and workspace the sandbox the layout loaded. */
export function SandboxPageProvider({
  value,
  children,
}: {
  value: SandboxPage
  children: ReactNode
}) {
  return (
    <SandboxPageContext.Provider value={value}>
      {children}
    </SandboxPageContext.Provider>
  )
}

export function useSandboxPage() {
  const page = useContext(SandboxPageContext)
  if (!page) throw new Error("useSandboxPage is used outside a sandbox page")
  return page
}

/**
 * The header of both sandbox pages: the name and ID in monospace, the status, the switch
 * between the dashboard and the workspace, and the options menu.
 */
export function SandboxHeader({ env, sandbox: sb }: SandboxPage) {
  const matchRoute = useMatchRoute()
  const onWorkspace = !!matchRoute({ to: "/e/$env/sandboxes/$id/work" })
  return (
    <PageHeader>
      <div className="flex min-w-0 flex-1 items-baseline gap-2">
        <h1 className="min-w-0 truncate font-mono text-sm font-medium">
          {sb.name}
        </h1>
        <span className="shrink-0 font-mono text-[0.6875rem] text-muted-foreground max-sm:hidden">
          {sb.id}
        </span>
      </div>
      <span className="shrink-0">
        <StatusBadge sandbox={sb} />
      </span>
      <SandboxOwner
        env={env}
        teammateId={sb.teammateId}
        className="hidden min-w-0 shrink-0 items-center text-xs text-muted-foreground md:inline-flex"
      />
      <nav
        aria-label="Sandbox pages"
        className="flex h-6 shrink-0 items-center rounded-[4px] bg-muted p-0.5 text-xs"
      >
        <PageLink
          current={!onWorkspace}
          to="/e/$env/sandboxes/$id"
          env={env}
          id={sb.id}
          label="Dashboard"
          icon={<SquaresFourIcon />}
        />
        <PageLink
          current={onWorkspace}
          to="/e/$env/sandboxes/$id/work"
          env={env}
          id={sb.id}
          label="Workspace"
          icon={<TerminalWindowIcon />}
        />
      </nav>
      <SandboxOptionsMenu env={env} sandbox={sb} />
    </PageHeader>
  )
}

function PageLink({
  current,
  to,
  env,
  id,
  label,
  icon,
}: {
  current: boolean
  to: "/e/$env/sandboxes/$id" | "/e/$env/sandboxes/$id/work"
  env: string
  id: string
  label: string
  icon: ReactNode
}) {
  return (
    <Link
      to={to}
      params={{ env, id }}
      aria-label={label}
      aria-current={current ? "page" : undefined}
      className={cn(
        "flex h-5 items-center gap-1.5 rounded-[3px] px-2 [&_svg]:size-3.5",
        current
          ? "bg-background font-medium shadow-xs"
          : "text-muted-foreground hover:text-foreground"
      )}
    >
      {icon}
      <span className="max-sm:hidden">{label}</span>
    </Link>
  )
}
