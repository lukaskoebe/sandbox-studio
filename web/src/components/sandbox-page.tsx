import { createContext, useContext, type ReactNode } from "react"
import { Link } from "@tanstack/react-router"
import { PageHeader } from "@/components/page-header"
import { CheckpointButton, SandboxDetails } from "@/components/sandbox-details"
import { SandboxOptionsMenu } from "@/components/sandbox-options-menu"
import { $api, type Sandbox } from "@/lib/api/client"

type SandboxPage = { env: string; sandbox: Sandbox }

const SandboxPageContext = createContext<SandboxPage | null>(null)

/** Gives the sandbox's workspace the sandbox the layout loaded. */
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
 * The sandbox's header: whose it is and its name, a pill with its state that opens its details,
 * a checkpoint button and the menu of what is off the everyday path.
 */
export function SandboxHeader({ env, sandbox: sb }: SandboxPage) {
  const teammates = $api.useQuery(
    "get",
    "/api/environments/{env}/teammates",
    { params: { path: { env } } },
    { enabled: !!sb.teammateId }
  )
  const owner = teammates.data?.find((t) => t.id === sb.teammateId)
  return (
    <PageHeader>
      <div className="flex min-w-0 items-baseline gap-1.5">
        {owner && (
          <span className="shrink-0 text-xs text-muted-foreground max-sm:hidden">
            <Link
              to="/e/$env/teammates/$id"
              params={{ env, id: owner.id }}
              className="hover:text-foreground"
            >
              {owner.name}
            </Link>{" "}
            /
          </span>
        )}
        <h1 className="min-w-0 truncate font-mono text-sm font-medium">
          {sb.name}
        </h1>
      </div>
      <SandboxDetails env={env} sandbox={sb} />
      <span className="flex-1" />
      <CheckpointButton env={env} sandbox={sb} />
      <SandboxOptionsMenu env={env} sandbox={sb} />
    </PageHeader>
  )
}
