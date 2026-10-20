import { createContext, useContext, type ReactNode } from "react"
import { SidebarTrigger } from "@/components/ui/sidebar"
import { Badge } from "@/components/ui/badge"
import { Separator } from "@/components/ui/separator"
import { $api } from "@/lib/api/client"
import { cn } from "@/lib/utils"

/**
 * Set inside the Settings page, whose nav has the sidebar toggle on phones: a section's own
 * header has it only from the md breakpoint on.
 */
export const InSettings = createContext(false)

export function PageHeader({ children }: { children: ReactNode }) {
  const inSettings = useContext(InSettings)
  return (
    <header
      data-slot="page-header"
      className="flex h-12 shrink-0 items-center gap-2 border-b px-3"
    >
      <SidebarTrigger className={cn(inSettings && "max-md:hidden")} />
      <Separator
        orientation="vertical"
        className={cn(
          "mr-1 data-vertical:h-4 data-vertical:self-center",
          inSettings && "max-md:hidden"
        )}
      />
      {children}
    </header>
  )
}

/** The environment a page's settings apply to, named in its header. */
export function EnvironmentLabel({ env }: { env: string }) {
  // The Settings page names the environment once, above its nav.
  const inSettings = useContext(InSettings)
  const envs = $api.useQuery("get", "/api/environments")
  const name = envs.data?.find((e) => e.id === env)?.name
  return name && !inSettings ? <Badge variant="outline">{name}</Badge> : null
}
