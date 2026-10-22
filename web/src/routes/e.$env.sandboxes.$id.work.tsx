import { createFileRoute } from "@tanstack/react-router"
import { BackToSetup, fromSetup } from "@/components/back-to-setup"
import {
  SandboxWorkspace,
  workspacePanels,
  type WorkspacePanelId,
} from "@/components/sandbox-workspace"

const text = (value: unknown) => (typeof value === "string" ? value : undefined)

/**
 * The sandbox's workspace: its chats, terminals and reviews with the side panels. ?session= is the
 * terminal's old name in links, so both open a terminal.
 */
export const Route = createFileRoute("/e/$env/sandboxes/$id/work")({
  validateSearch: (
    s: Record<string, unknown>
  ): {
    terminal?: string
    chat?: string
    review?: string
    panel?: WorkspacePanelId
    from?: "setup"
  } => ({
    terminal: text(s.terminal) ?? text(s.session),
    chat: text(s.chat),
    review: text(s.review),
    panel: workspacePanels.find((p) => p === s.panel),
    ...fromSetup(s),
  }),
  component: WorkspaceRoute,
})

function WorkspaceRoute() {
  const search = Route.useSearch()
  const navigate = Route.useNavigate()
  return (
    <>
      {search.from === "setup" && (
        <div className="shrink-0 border-b px-3 py-1.5">
          <BackToSetup step="sandbox" />
        </div>
      )}
      <SandboxWorkspace
        search={search}
        onPanel={(panel) =>
          void navigate({
            search: (prev) => ({ ...prev, panel }),
            replace: true,
          })
        }
        onTab={(tab) =>
          void navigate({
            search: (prev) => ({ panel: prev.panel, from: prev.from, ...tab }),
            replace: true,
          })
        }
      />
    </>
  )
}
