import { Link, useMatchRoute } from "@tanstack/react-router"
import {
  SidebarMenuButton,
  SidebarMenuItem,
  SidebarMenuSub,
  SidebarMenuSubButton,
  SidebarMenuSubItem,
} from "@/components/ui/sidebar"
import { $api } from "@/lib/api/client"
import { clock, dayName, leadToday, planOpen, planStateLabel } from "@/lib/lead"
import { LeadGlyph } from "./glyph"

/**
 * Lead at the top of Teammates (docs/lead.md): its name, and while it writes, that it does.
 * Its open plans are listed under it, and on its pages the last days it wrote. A plan starts
 * from the day's box, and Standing orders and Settings are in its name's menu in the crumbs.
 */
export function LeadSidebarRow({ env }: { env: string }) {
  const matchRoute = useMatchRoute()
  const lead = $api.useQuery("get", "/api/environments/{env}/lead", {
    params: { path: { env } },
  })
  const v = lead.data
  const name = v?.settings.name ?? "Lead"
  const t = leadToday(v)
  const open = matchRoute({ to: "/e/$env/lead/$day", params: { env } }) as
    false | { day?: string }
  const onToday = !!matchRoute({ to: "/e/$env/lead", params: { env } })
  const onSettings = !!matchRoute({
    to: "/e/$env/lead/settings",
    params: { env },
  })
  const onPlan = matchRoute({
    to: "/e/$env/lead/plans/$plan",
    params: { env },
  }) as false | { plan?: string }
  const onNew = !!matchRoute({ to: "/e/$env/lead/plans/new", params: { env } })
  const onOrders = !!matchRoute({ to: "/e/$env/lead/orders", params: { env } })
  const onPage =
    onToday || onSettings || !!open || !!onPlan || onNew || onOrders
  const days = (v?.days ?? []).filter((d) => d.day !== t).slice(0, 4)
  const plans = $api.useQuery("get", "/api/environments/{env}/lead/plans", {
    params: { path: { env } },
  })
  // Open plans show everywhere, the one open on the page even once it ended.
  const shown = (plans.data ?? [])
    .filter((p) => planOpen(p) || (!!onPlan && onPlan.plan === p.id))
    .slice(0, 4)
  return (
    <>
      <SidebarMenuItem>
        <SidebarMenuButton
          tooltip={name}
          className="group-data-[collapsible=icon]:p-1.5!"
          isActive={onToday}
          render={<Link to="/e/$env/lead" params={{ env }} />}
        >
          <LeadGlyph className="size-5" />
          <span className="min-w-0 flex-1 truncate">{name}</span>
          {v?.writing ? (
            <span className="flex items-center gap-1.5 text-caption text-muted-foreground">
              <span className="size-1.5 animate-pulse rounded-full bg-(--st-running)" />
              writing
            </span>
          ) : (
            v?.today.writtenAt && (
              <span className="text-caption text-muted-foreground">
                written {clock(v.today.writtenAt)}
              </span>
            )
          )}
        </SidebarMenuButton>
      </SidebarMenuItem>
      {shown.length > 0 && (
        <li className="list-none">
          <SidebarMenuSub
            aria-label={`${name}'s plans`}
            className="mr-0 gap-px pr-0"
          >
            {shown.map((p) => (
              <SidebarMenuSubItem key={p.id}>
                <SidebarMenuSubButton
                  size="sm"
                  isActive={!!onPlan && onPlan.plan === p.id}
                  render={
                    <Link
                      to="/e/$env/lead/plans/$plan"
                      params={{ env, plan: p.id }}
                    />
                  }
                  className="h-auto py-1"
                >
                  <span className="grid min-w-0 flex-1 leading-tight">
                    <span className="truncate">{p.title || "Plan"}</span>
                    <span
                      className={
                        p.state === "proposed" || p.state === "asking"
                          ? "truncate text-caption text-(--st-waiting-fg)"
                          : "truncate text-caption text-muted-foreground"
                      }
                    >
                      {planStateLabel(p.state)}
                    </span>
                  </span>
                </SidebarMenuSubButton>
              </SidebarMenuSubItem>
            ))}
          </SidebarMenuSub>
        </li>
      )}
      {onPage && days.length > 0 && (
        <li className="list-none">
          <SidebarMenuSub
            aria-label={`Days ${name} wrote`}
            className="mr-0 gap-px pr-0"
          >
            {days.map((d) => (
              <SidebarMenuSubItem key={d.day}>
                <SidebarMenuSubButton
                  size="sm"
                  isActive={!!open && open.day === d.day}
                  render={
                    <Link to="/e/$env/lead/$day" params={{ env, day: d.day }} />
                  }
                  className="h-auto py-1"
                >
                  <span className="grid min-w-0 flex-1 leading-tight">
                    <span className="truncate">
                      {dayName(d.day, { short: true, today: t })}
                    </span>
                    {d.heading && (
                      <span className="truncate text-caption text-muted-foreground">
                        {d.heading}
                      </span>
                    )}
                  </span>
                </SidebarMenuSubButton>
              </SidebarMenuSubItem>
            ))}
          </SidebarMenuSub>
        </li>
      )}
    </>
  )
}
