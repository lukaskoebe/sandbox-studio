import type { ReactNode } from "react"
import { SidebarTrigger } from "@/components/ui/sidebar"
import { Badge } from "@/components/ui/badge"
import { $api } from "@/lib/api/client"

/**
 * A page's title row, without a rule under it, as the teammate page's. The sidebar's toggle is
 * here on phones; on wider screens the sidebar's own rail folds it.
 */
export function PageHeader({ children }: { children: ReactNode }) {
  return (
    <header
      data-slot="page-header"
      className="flex h-14 shrink-0 items-center gap-2 px-3 md:px-4"
    >
      <SidebarTrigger className="md:hidden" />
      {children}
    </header>
  )
}

/** The environment a page applies to, named in its header. */
export function EnvironmentLabel({ env }: { env: string }) {
  const envs = $api.useQuery("get", "/api/environments")
  const name = envs.data?.find((e) => e.id === env)?.name
  return name ? <Badge variant="outline">{name}</Badge> : null
}
