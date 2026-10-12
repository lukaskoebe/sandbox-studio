import type { ReactNode } from "react"
import { SidebarTrigger } from "@/components/ui/sidebar"
import { Badge } from "@/components/ui/badge"
import { Separator } from "@/components/ui/separator"
import { $api } from "@/lib/api/client"

export function PageHeader({ children }: { children: ReactNode }) {
  return (
    <header className="flex h-12 shrink-0 items-center gap-2 border-b px-3">
      <SidebarTrigger />
      <Separator
        orientation="vertical"
        className="mr-1 data-vertical:h-4 data-vertical:self-center"
      />
      {children}
    </header>
  )
}

/** The environment a page's settings apply to, named in its header. */
export function EnvironmentLabel({ env }: { env: string }) {
  const envs = $api.useQuery("get", "/api/environments")
  const name = envs.data?.find((e) => e.id === env)?.name
  return name ? <Badge variant="outline">{name}</Badge> : null
}
