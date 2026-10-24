import { PresenceDot, TeammateAvatar } from "@/components/teammate-avatar"
import { Readiness } from "@/components/teammate-readiness"
import { SidebarTrigger } from "@/components/ui/sidebar"
import { presenceLabels, usePresence } from "@/hooks/use-teammate-presence"
import { $api, type Teammate } from "@/lib/api/client"
import { harnessLabels } from "@/lib/teammates"

/**
 * The top of the teammate's home: who it is, the agent it runs and what it does now. The name
 * opens its profile.
 */
export function TeammateHeader({
  env,
  teammate,
  onProfile,
}: {
  env: string
  teammate: Teammate
  onProfile: (view: "card") => void
}) {
  const providers = $api.useQuery("get", "/api/environments/{env}/providers", {
    params: { path: { env } },
  })
  const state = usePresence(env)(teammate.id)
  return (
    <header className="flex h-14 shrink-0 items-center gap-2 px-3 md:px-4">
      <SidebarTrigger className="md:hidden" />
      <button
        type="button"
        onClick={() => onProfile("card")}
        aria-label={`${teammate.name}'s profile`}
        className="-ml-1.5 flex min-w-0 items-center gap-2.5 rounded-lg px-1.5 py-1 text-left outline-none hover:bg-muted focus-visible:ring-2 focus-visible:ring-ring/40"
      >
        <TeammateAvatar
          name={teammate.name}
          avatar={teammate.avatar}
          className="size-7 text-[0.6875rem]"
        />
        <span className="grid min-w-0 leading-tight">
          <span className="truncate font-heading text-sm font-semibold">
            {teammate.name}
          </span>
          <span className="truncate text-[0.6875rem] text-muted-foreground">
            {[teammate.role, harnessLabels[teammate.harness]]
              .filter(Boolean)
              .join(" · ")}
          </span>
        </span>
      </button>
      <div className="ml-auto flex shrink-0 items-center gap-3">
        <Readiness
          teammate={teammate}
          provider={providers.data?.find((p) => p.id === teammate.providerId)}
          problemsOnly
        />
        <span className="flex items-center gap-1.5 text-xs text-muted-foreground">
          <PresenceDot presence={state} />
          {presenceLabels[state]}
        </span>
      </div>
    </header>
  )
}
