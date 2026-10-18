import { createContext, useContext, type ReactNode } from "react"
import { SidebarTrigger } from "@/components/ui/sidebar"
import { Badge } from "@/components/ui/badge"
import { Separator } from "@/components/ui/separator"
import { $api } from "@/lib/api/client"

/** Set where a page is shown in a dialog, such as a settings section: no sidebar toggle there. */
export const InDialog = createContext(false)

export function PageHeader({ children }: { children: ReactNode }) {
  if (useContext(InDialog))
    return (
      <header
        data-slot="page-header"
        className="flex h-12 shrink-0 items-center gap-2 border-b pr-12 pl-4"
      >
        {children}
      </header>
    )
  return (
    <header
      data-slot="page-header"
      className="flex h-12 shrink-0 items-center gap-2 border-b px-3"
    >
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
