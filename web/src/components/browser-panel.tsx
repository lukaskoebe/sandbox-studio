import { useState } from "react"
import { Link, useNavigate } from "@tanstack/react-router"
import { ArrowsOutSimpleIcon, BrowserIcon } from "@phosphor-icons/react"
import { BrowserSteps, useBrowserRuns } from "@/components/browser-rail"
import { BrowserWindow, type BrowserReplay } from "@/components/browser-window"
import { SidePanel } from "@/components/side-panel"
import { $api } from "@/lib/api/client"
import { tileClass } from "@/lib/accents"
import { cn } from "@/lib/utils"

/**
 * The teammate's browser as a side panel, beside a sandbox's terminal or its conversation: the
 * same window as on its Browser tab, with the last session's steps folded under it.
 */
export function BrowserPanel({
  env,
  teammate,
  storageKey,
  onClose,
}: {
  env: string
  teammate: string
  storageKey: string
  onClose: () => void
}) {
  const navigate = useNavigate()
  const tm = $api.useQuery("get", "/api/environments/{env}/teammates/{id}", {
    params: { path: { env, id: teammate } },
  })
  const name = tm.data?.name ?? "The teammate"
  const { runs } = useBrowserRuns(env, teammate)
  const steps = runs[0]?.steps ?? []
  const [replay, setReplay] = useState<BrowserReplay | null>(null)
  const [showSteps, setShowSteps] = useState(false)
  const expand = () =>
    void navigate({
      to: "/e/$env/teammates/$id/browser",
      params: { env, id: teammate },
    })
  return (
    <SidePanel
      label="Teammate browser"
      title={
        <span className="flex items-center gap-2">
          <span
            className={cn(
              "inline-flex size-5 items-center justify-center rounded",
              tileClass("browser")
            )}
          >
            <BrowserIcon className="size-3" weight="bold" />
          </span>
          {tm.data ? `${name}'s browser` : "Browser"}
        </span>
      }
      actions={
        <Link
          to="/e/$env/teammates/$id/browser"
          params={{ env, id: teammate }}
          aria-label={`Open on ${name}'s page`}
          title={`Open on ${name}'s page, with its activity and sites`}
          className="inline-flex size-7 items-center justify-center rounded-md text-muted-foreground hover:bg-muted hover:text-foreground focus-visible:ring-2 focus-visible:ring-ring/50 focus-visible:outline-none"
        >
          <ArrowsOutSimpleIcon className="size-3.5" />
        </Link>
      }
      storageKey={storageKey}
      onClose={onClose}
    >
      <BrowserWindow
        env={env}
        teammate={teammate}
        size="panel"
        onExpand={expand}
        onSites={expand}
        replay={replay}
        onReplay={setReplay}
      />
      {steps.length > 0 && (
        <div className="shrink-0 border-t text-xs">
          <button
            type="button"
            aria-expanded={showSteps}
            onClick={() => setShowSteps(!showSteps)}
            className="flex h-9 w-full items-center gap-2 px-3 text-left hover:bg-muted/60"
          >
            <span className="font-medium">Steps</span>
            <span className="min-w-0 flex-1 truncate text-muted-foreground">
              {steps.at(-1)?.action} {steps.at(-1)?.target}
            </span>
            <span className="font-mono text-muted-foreground">
              {steps.length}
            </span>
          </button>
          {showSteps && (
            <div className="max-h-56 overflow-y-auto px-2 pb-2">
              <BrowserSteps
                env={env}
                teammate={teammate}
                name={name}
                steps={steps}
                replay={replay}
                onReplay={setReplay}
              />
            </div>
          )}
        </div>
      )}
    </SidePanel>
  )
}
