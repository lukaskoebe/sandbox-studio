import type { CSSProperties, ReactNode } from "react"
import { Link } from "@tanstack/react-router"
import {
  CaretUpDownIcon,
  GearSixIcon,
  HouseIcon,
  KeyIcon,
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
import {
  PresenceAvatar,
  StatusDot,
  ThemeToggle,
  toneIcons,
  toneLabels,
  type MockupSearch,
} from "../shared"

export const keep = (prev: MockupSearch) => ({
  theme: prev.theme,
  bare: prev.bare,
})

/** A soft pill naming an area, in its hue. */
export function KindChip({ tone, label }: { tone: Tone; label?: string }) {
  const I = toneIcons[tone]
  return (
    <span
      data-tone={tone}
      className="inline-flex h-6 items-center gap-1.5 rounded-full bg-(--tone)/12 pr-2.5 pl-2 text-xs font-medium text-[color-mix(in_oklch,var(--tone),var(--foreground)_25%)]"
    >
      <I weight="duotone" className="size-3.5 text-(--tone)" />
      {label ?? toneLabels[tone]}
    </span>
  )
}

/** Large page title with a one-line intro, the B direction's signature. */
export function PageTitle({
  title,
  intro,
  actions,
  eyebrow,
}: {
  title: ReactNode
  intro: ReactNode
  actions?: ReactNode
  eyebrow?: ReactNode
}) {
  return (
    <header className="flex flex-wrap items-end gap-x-6 gap-y-4">
      <div className="min-w-0 flex-1">
        {eyebrow && (
          <div className="mb-2 text-[0.8125rem] text-muted-foreground">
            {eyebrow}
          </div>
        )}
        <h1 className="text-[1.75rem] leading-tight font-semibold tracking-[-0.02em] text-balance">
          {title}
        </h1>
        <p className="mt-1.5 text-[0.9375rem] text-muted-foreground">{intro}</p>
      </div>
      {actions && <div className="flex items-center gap-2">{actions}</div>}
    </header>
  )
}

export function SectionTitle({
  title,
  count,
  actions,
}: {
  title: string
  count?: number
  actions?: ReactNode
}) {
  return (
    <div className="mb-3 flex items-center gap-2">
      <h2 className="text-[0.9375rem] font-semibold tracking-[-0.01em]">
        {title}
      </h2>
      {count !== undefined && (
        <span className="rounded-full bg-muted px-1.5 text-xs text-muted-foreground tabular-nums">
          {count}
        </span>
      )}
      <div className="ml-auto flex items-center gap-1">{actions}</div>
    </div>
  )
}

type NavItem = {
  key: string
  label: string
  icon: Icon
  tone?: Tone
  badge?: number
}

const nav: NavItem[] = [
  { key: "home", label: "Home", icon: HouseIcon },
  { key: "chats", label: "Chats", icon: toneIcons.agent, tone: "agent" },
  {
    key: "network",
    label: "Network",
    icon: toneIcons.network,
    tone: "network",
    badge: 2,
  },
  { key: "secrets", label: "Secrets", icon: KeyIcon },
  { key: "memory", label: "Memory", icon: toneIcons.memory, tone: "memory" },
  { key: "settings", label: "Settings", icon: GearSixIcon },
]

function CalmSidebar({ active }: { active?: string }) {
  return (
    <Sidebar
      variant="inset"
      collapsible="icon"
      className="[&_[data-slot=sidebar-inner]]:bg-transparent"
    >
      <SidebarHeader className="px-2 pt-2.5">
        <button
          type="button"
          className="flex items-center gap-2.5 rounded-xl p-1.5 text-left hover:bg-sidebar-accent"
        >
          <span className="flex size-8 items-center justify-center rounded-[0.6rem] bg-primary text-sm font-semibold text-primary-foreground shadow-sm">
            C
          </span>
          <span className="min-w-0 flex-1 leading-tight">
            <span className="block truncate text-[0.8125rem] font-semibold">
              {environment.name}
            </span>
            <span className="block truncate text-[0.6875rem] text-muted-foreground">
              3 teammates · 4 sandboxes
            </span>
          </span>
          <CaretUpDownIcon className="size-3.5 text-muted-foreground" />
        </button>
      </SidebarHeader>
      <SidebarContent className="gap-1">
        <SidebarGroup>
          <SidebarMenu className="gap-0.5">
            {nav.map((n) => (
              <SidebarMenuItem key={n.key}>
                <SidebarMenuButton
                  isActive={active === n.key}
                  tooltip={n.label}
                  data-tone={n.tone}
                  className={cn(
                    "h-8 rounded-lg text-[0.8125rem] data-active:bg-card data-active:shadow-[0_1px_2px_oklch(0_0_0/0.06)]",
                    n.tone && "[&_svg]:text-(--tone)"
                  )}
                  render={
                    n.key === "home" ? (
                      <Link
                        to="/mockups/$direction/$screen"
                        params={{ direction: "b", screen: "home" }}
                        search={keep}
                      />
                    ) : undefined
                  }
                >
                  <n.icon weight="duotone" />
                  <span>{n.label}</span>
                  {n.badge ? (
                    <span
                      data-status="waiting"
                      className="ml-auto flex size-4.5 items-center justify-center rounded-full bg-(--status) text-[0.625rem] font-semibold text-black/75"
                    >
                      {n.badge}
                    </span>
                  ) : null}
                </SidebarMenuButton>
              </SidebarMenuItem>
            ))}
          </SidebarMenu>
        </SidebarGroup>
        <SidebarGroup>
          <SidebarGroupLabel className="text-[0.75rem] font-medium text-muted-foreground">
            Teammates
          </SidebarGroupLabel>
          <SidebarGroupAction title="New teammate">
            <PlusIcon />
          </SidebarGroupAction>
          <SidebarGroupContent>
            <SidebarMenu className="gap-0.5">
              {teammates.map((t) => (
                <SidebarMenuItem key={t.id}>
                  <SidebarMenuButton
                    tooltip={t.name}
                    className="h-11 rounded-lg [--presence-ring:var(--sidebar)] hover:[--presence-ring:var(--sidebar-accent)]"
                  >
                    <PresenceAvatar
                      name={t.name}
                      presence={t.presence}
                      className="size-7 text-[0.625rem]"
                      dotClassName="size-2.5"
                    />
                    <span className="min-w-0 leading-tight">
                      <span className="block truncate text-[0.8125rem] font-medium">
                        {t.name}
                      </span>
                      <span
                        data-presence={t.presence}
                        className={cn(
                          "block truncate text-[0.6875rem] text-muted-foreground",
                          t.presence === "waiting" &&
                            "text-[color-mix(in_oklch,var(--status),var(--foreground)_40%)]"
                        )}
                      >
                        {t.activity}
                      </span>
                    </span>
                  </SidebarMenuButton>
                </SidebarMenuItem>
              ))}
            </SidebarMenu>
          </SidebarGroupContent>
        </SidebarGroup>
        <SidebarGroup>
          <SidebarGroupLabel className="text-[0.75rem] font-medium text-muted-foreground">
            Sandboxes
          </SidebarGroupLabel>
          <SidebarGroupAction title="New sandbox">
            <PlusIcon />
          </SidebarGroupAction>
          <SidebarGroupContent>
            <SidebarMenu className="gap-0.5">
              {sandboxes.map((sb) => (
                <SidebarMenuItem key={sb.id}>
                  <SidebarMenuButton
                    tooltip={sb.name}
                    isActive={active === sb.name}
                    className="h-8 rounded-lg text-[0.8125rem] data-active:bg-card data-active:shadow-[0_1px_2px_oklch(0_0_0/0.06)]"
                    render={
                      <Link
                        to="/mockups/$direction/$screen"
                        params={{ direction: "b", screen: "dashboard" }}
                        search={keep}
                      />
                    }
                  >
                    <span className="flex size-4 items-center justify-center">
                      <StatusDot status={sb.status} />
                    </span>
                    <span className="truncate">{sb.name}</span>
                    {sb.owner && (
                      <TeammateAvatar
                        name={sb.owner}
                        className="ml-auto size-4 text-[0.4375rem] opacity-80"
                      />
                    )}
                  </SidebarMenuButton>
                </SidebarMenuItem>
              ))}
            </SidebarMenu>
          </SidebarGroupContent>
        </SidebarGroup>
      </SidebarContent>
      <SidebarFooter className="flex-row items-center justify-between px-3 pb-3 group-data-[collapsible=icon]:hidden">
        <span className="text-[0.6875rem] text-muted-foreground">
          Sandbox Studio 0.1.1
        </span>
        <ThemeToggle
          className="gap-0.5 rounded-full bg-sidebar-accent p-0.5"
          itemClassName="size-6 rounded-full text-muted-foreground data-active:bg-card data-active:text-foreground data-active:shadow-sm"
        />
      </SidebarFooter>
    </Sidebar>
  )
}

/** Mobile-only top bar; on desktop the page title carries the context. */
export function MobileBar({ title }: { title: string }) {
  return (
    <div className="flex h-12 shrink-0 items-center gap-2 px-3 md:hidden">
      <SidebarTrigger />
      <span className="text-sm font-medium">{title}</span>
    </div>
  )
}

export function CalmShell({
  active,
  children,
}: {
  active?: string
  children: ReactNode
}) {
  return (
    <SidebarProvider
      className="bg-sidebar"
      style={{ "--sidebar-width": "16.5rem" } as CSSProperties}
    >
      <CalmSidebar active={active} />
      <SidebarInset className="min-w-0 overflow-hidden text-sm max-md:h-svh md:h-[calc(100svh-1rem)] md:border md:border-sidebar-border md:shadow-[var(--cs-shadow)]">
        {children}
      </SidebarInset>
    </SidebarProvider>
  )
}
