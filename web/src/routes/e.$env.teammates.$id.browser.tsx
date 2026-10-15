import { useState } from "react"
import { createFileRoute } from "@tanstack/react-router"
import { BrowserRail, type RailTab } from "@/components/browser-rail"
import { BrowserWindow, type BrowserReplay } from "@/components/browser-window"
import { $api } from "@/lib/api/client"

export const Route = createFileRoute("/e/$env/teammates/$id/browser")({
  component: TeammateBrowserTab,
})

const explanation =
  "Each teammate has its own browser, a separate VM that only Studio reaches. Its agents drive it with the browser tools; new sites need your approval, and logins come from the vault without the agent seeing them. Type an address or press Back to take the wheel: agent calls wait until you hand back."

/** The teammate's browser, big, with its activity and sites beside it (docs/browser.md). */
function TeammateBrowserTab() {
  const { env, id } = Route.useParams()
  const tm = $api.useQuery("get", "/api/environments/{env}/teammates/{id}", {
    params: { path: { env, id } },
  })
  const [rail, setRail] = useState<RailTab>("activity")
  const [replay, setReplay] = useState<BrowserReplay | null>(null)
  return (
    <div className="flex min-h-0 flex-1 max-xl:flex-col max-xl:overflow-y-auto">
      <div className="flex min-h-[32rem] min-w-0 flex-1 flex-col gap-2 p-3">
        <p className="max-w-3xl px-1 text-xs text-muted-foreground">
          {explanation}
        </p>
        <BrowserWindow
          key={id}
          env={env}
          teammate={id}
          size="full"
          onSites={() => setRail("sites")}
          replay={replay}
          onReplay={setReplay}
        />
      </div>
      <BrowserRail
        env={env}
        teammate={id}
        name={tm.data?.name ?? "The teammate"}
        tab={rail}
        onTab={setRail}
        replay={replay}
        onReplay={setReplay}
      />
    </div>
  )
}
