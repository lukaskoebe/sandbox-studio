import type { CSSProperties, ReactNode } from "react"
import { Link } from "@tanstack/react-router"
import {
  CaretUpDownIcon,
  GearIcon,
  HouseIcon,
  KeyIcon,
  MagnifyingGlassIcon,
  PlusIcon,
  type Icon,
} from "@phosphor-icons/react"
import {
  Sidebar,
  SidebarContent,
  SidebarFooter,
  SidebarGroup,
  SidebarGroupAction,
  SidebarGroupContent,
  SidebarGroupLabel,
  SidebarHeader,
  SidebarInset,
  SidebarMenu,
  SidebarMenuButton,
  SidebarMenuItem,
  SidebarProvider,
  SidebarTrigger,
} from "@/components/ui/sidebar"
import { TeammateAvatar } from "@/components/teammate-avatar"
import { cn } from "@/lib/utils"
import { environment, sandboxes, teammates, type Tone } from "../data"
import { StatusDot, ThemeToggle, toneIcons, type MockupSearch } from "../shared"

/** A small square tile holding an icon, tinted with an area's accent or neutral. */
export function Tile({
  tone,
  icon: IconComp,
  className,
}: {
  tone?: Tone
  icon?: Icon
  className?: string
}) {
  const I = IconComp ?? (tone ? toneIcons[tone] : HouseIcon)
  return (
    <span
      data-tone={tone}
      className={cn(
        "inline-flex size-5 shrink-0 items-center justify-center rounded-[4px]",
        tone
          ? "bg-(--tone)/14 text-(--tone)"
          : "bg-foreground/[0.07] text-foreground/70",
        className
      )}
    >
      <I weight="bold" className="size-3" />
    </span>
  )
}

/** Section header: icon tile, title, a mono count and actions on the right. */
export function Section({
  title,
  tone,
  icon,
  count,
  actions,
  children,
  className,
}: {
  title: string
  tone?: Tone
  icon?: Icon
  count?: ReactNode
  actions?: ReactNode
  children: ReactNode
  className?: string
}) {
  return (
    <section className={cn("min-w-0", className)}>
      <header className="mb-2 flex h-6 items-center gap-2">
        <Tile tone={tone} icon={icon} />
        <h2 className="text-[0.8125rem] font-semibold tracking-tight">
          {title}
        </h2>
        {count !== undefined && (
          <span className="font-mono text-[0.6875rem] text-muted-foreground">
            {count}
          </span>
        )}
        <div className="ml-auto flex items-center gap-1">{actions}</div>
      </header>
      {children}
    </section>
  )
}

/** Monospace chip for identifiers: ports, templates, ids. */
export function Mono({
  children,
  className,
}: {
  children: ReactNode
  className?: string
}) {
  return (
    <span
      className={cn(
        "inline-flex h-[1.125rem] items-center rounded-[3px] bg-muted px-1 font-mono text-[0.6875rem] text-foreground/80 ring-1 ring-border ring-inset",
        className
      )}
    >
      {children}
    </span>
  )
}

/** A count of pending approvals in the waiting color. */
export function WaitingCount({ n }: { n: number }) {
  if (!n) return null
  return (
    <span
      data-status="waiting"
      className="inline-flex h-4 min-w-4 items-center justify-center rounded-[3px] bg-(--status)/18 px-1 font-mono text-[0.625rem] font-semibold text-[color-mix(in_oklch,var(--status),var(--foreground)_35%)]"
    >
      {n}
    </span>
  )
}

export function Kbd({ children }: { children: ReactNode }) {
  return (
    <kbd className="inline-flex h-4 items-center rounded-[3px] border border-b-2 bg-background px-1 font-mono text-[0.625rem] text-muted-foreground">
      {children}
    </kbd>
  )
}

/** A tiny horizontal meter for CPU and memory. */
export function Meter({ pct, className }: { pct: number; className?: string }) {
  return (
    <span
      className={cn(
        "relative inline-block h-1 w-10 overflow-hidden rounded-full bg-foreground/10",
        className
      )}
    >
      <span
        className={cn(
          "absolute inset-y-0 left-0 rounded-full",
          pct > 80 ? "bg-(--st-failed)" : "bg-foreground/50"
        )}
        style={{ width: `${pct}%` }}
      />
    </span>
  )
}

type NavItem = {
  key: string
  label: string
  icon: Icon
  tone?: Tone
  count?: number
}

const nav: NavItem[] = [
  { key: "home", label: "Home", icon: HouseIcon },
  { key: "chats", label: "Chats", icon: toneIcons.agent, tone: "agent" },
  {
    key: "network",
    label: "Network",
    icon: toneIcons.network,
    tone: "network",
    count: 2,
  },
  { key: "secrets", label: "Secrets", icon: KeyIcon },
  { key: "memory", label: "Memory", icon: toneIcons.memory, tone: "memory" },
]

const keep = (prev: MockupSearch) => ({ theme: prev.theme, bare: prev.bare })

function WorkbenchSidebar({ active }: { active?: string }) {
  return (
    <Sidebar collapsible="icon">
      <SidebarHeader className="h-(--wb-header) justify-center border-b border-sidebar-border px-2 py-0">
        <button
          type="button"
          className="flex h-7 items-center gap-2 rounded-[4px] px-1.5 text-left hover:bg-sidebar-accent"
        >
          <span className="flex size-5 items-center justify-center rounded-[4px] bg-primary font-mono text-[0.625rem] font-bold text-primary-foreground">
            cp
          </span>
          <span className="min-w-0 flex-1 truncate font-mono text-xs font-medium">
            {environment.slug}
          </span>
          <CaretUpDownIcon className="size-3.5 text-muted-foreground" />
        </button>
      </SidebarHeader>
      <SidebarContent className="gap-0">
        <SidebarGroup className="py-2">
          <SidebarMenu>
            {nav.map((n) => (
              <SidebarMenuItem key={n.key}>
                <SidebarMenuButton
                  size="sm"
                  isActive={active === n.key}
                  tooltip={n.label}
                  data-tone={n.tone}
                  className={cn(
                    "rounded-[4px] data-active:font-semibold",
                    n.tone && "[&_svg]:text-(--tone)"
                  )}
                  render={
                    n.key === "home" ? (
                      <Link
                        to="/mockups/$direction/$screen"
                        params={{ direction: "a", screen: "home" }}
                        search={keep}
                      />
                    ) : undefined
                  }
                >
                  <n.icon weight={active === n.key ? "fill" : "regular"} />
                  <span>{n.label}</span>
                  {n.count ? (
                    <span className="ml-auto">
                      <WaitingCount n={n.count} />
                    </span>
                  ) : null}
                </SidebarMenuButton>
              </SidebarMenuItem>
            ))}
          </SidebarMenu>
        </SidebarGroup>
        <SidebarGroup className="py-1">
          <SidebarGroupLabel className="h-6 font-mono text-[0.625rem] tracking-wider text-muted-foreground uppercase">
            Teammates
          </SidebarGroupLabel>
          <SidebarGroupAction className="top-2" title="New teammate">
            <PlusIcon />
          </SidebarGroupAction>
          <SidebarGroupContent>
            <SidebarMenu>
              {teammates.map((t) => (
                <SidebarMenuItem key={t.id}>
                  <SidebarMenuButton
                    size="sm"
                    tooltip={t.name}
                    className="rounded-[4px]"
                  >
                    <TeammateAvatar
                      name={t.name}
                      className="size-4 rounded-[3px] text-[0.5rem]"
                    />
                    <span className="truncate">{t.name}</span>
                    <span
                      data-presence={t.presence}
                      className={cn(
                        "ml-auto size-1.5 shrink-0 rounded-full bg-(--status)",
                        t.presence === "working" && "animate-pulse"
                      )}
                      title={t.presence}
                    />
                  </SidebarMenuButton>
                </SidebarMenuItem>
              ))}
            </SidebarMenu>
          </SidebarGroupContent>
        </SidebarGroup>
        <SidebarGroup className="py-1">
          <SidebarGroupLabel className="h-6 font-mono text-[0.625rem] tracking-wider text-muted-foreground uppercase">
            Sandboxes
          </SidebarGroupLabel>
          <SidebarGroupAction className="top-2" title="New sandbox">
            <PlusIcon />
          </SidebarGroupAction>
          <SidebarGroupContent>
            <SidebarMenu>
              {sandboxes.map((sb) => (
                <SidebarMenuItem key={sb.id}>
                  <SidebarMenuButton
                    size="sm"
                    tooltip={sb.name}
                    isActive={active === sb.name}
                    className="rounded-[4px]"
                    render={
                      <Link
                        to="/mockups/$direction/$screen"
                        params={{ direction: "a", screen: "dashboard" }}
                        search={keep}
                      />
                    }
                  >
                    <span className="flex size-4 items-center justify-center">
                      <StatusDot status={sb.status} className="size-1.5" />
                    </span>
                    <span className="truncate font-mono">{sb.name}</span>
                    <span className="ml-auto">
                      <WaitingCount n={sb.approvals} />
                    </span>
                  </SidebarMenuButton>
                </SidebarMenuItem>
              ))}
            </SidebarMenu>
          </SidebarGroupContent>
        </SidebarGroup>
      </SidebarContent>
      <SidebarFooter className="gap-1 border-t border-sidebar-border p-2">
        <SidebarMenu>
          <SidebarMenuItem>
            <SidebarMenuButton
              size="sm"
              tooltip="Settings"
              className="rounded-[4px]"
            >
              <GearIcon />
              <span>Settings</span>
            </SidebarMenuButton>
          </SidebarMenuItem>
        </SidebarMenu>
        <div className="flex items-center justify-between px-2 group-data-[collapsible=icon]:hidden">
          <span className="font-mono text-[0.625rem] text-muted-foreground">
            studio v0.1.1
          </span>
          <ThemeToggle
            className="rounded-[4px] bg-sidebar-accent/70 p-0.5"
            itemClassName="size-5 rounded-[3px] text-muted-foreground data-active:bg-background data-active:text-foreground data-active:shadow-xs"
          />
        </div>
      </SidebarFooter>
    </Sidebar>
  )
}

/** The page header: breadcrumbs on the left, actions on the right. */
export function Bar({
  crumbs,
  children,
  className,
}: {
  crumbs: ReactNode[]
  children?: ReactNode
  className?: string
}) {
  return (
    <header
      className={cn(
        "flex h-(--wb-header) shrink-0 items-center gap-2 border-b px-2 md:px-3",
        className
      )}
    >
      <SidebarTrigger className="md:hidden" />
      <nav className="flex min-w-0 items-center gap-1.5 font-mono text-xs">
        {crumbs.map((c, i) => (
          <span key={i} className="flex min-w-0 items-center gap-1.5">
            {i > 0 && <span className="text-muted-foreground/60">/</span>}
            <span
              className={cn(
                "truncate",
                i < crumbs.length - 1
                  ? "text-muted-foreground max-sm:hidden"
                  : "font-medium"
              )}
            >
              {c}
            </span>
          </span>
        ))}
      </nav>
      <div className="ml-auto flex shrink-0 items-center gap-1.5">
        {children}
      </div>
    </header>
  )
}

export function SearchBox() {
  return (
    <button
      type="button"
      className="hidden h-6 w-52 items-center gap-1.5 rounded-[4px] border bg-muted/60 px-1.5 text-xs text-muted-foreground lg:flex"
    >
      <MagnifyingGlassIcon className="size-3.5" />
      Jump to…
      <span className="ml-auto flex gap-0.5">
        <Kbd>⌘</Kbd>
        <Kbd>K</Kbd>
      </span>
    </button>
  )
}

export function WorkbenchShell({
  active,
  children,
}: {
  active?: string
  children: ReactNode
}) {
  return (
    <SidebarProvider style={{ "--sidebar-width": "14.5rem" } as CSSProperties}>
      <WorkbenchSidebar active={active} />
      <SidebarInset className="h-svh min-w-0 overflow-hidden text-xs">
        {children}
      </SidebarInset>
    </SidebarProvider>
  )
}
