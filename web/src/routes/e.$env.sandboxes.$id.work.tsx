import { createFileRoute } from "@tanstack/react-router"
import {
  SandboxWorkspace,
  workspacePanels,
  type WorkspacePanelId,
} from "@/components/sandbox-workspace"

const text = (value: unknown) => (typeof value === "string" ? value : undefined)

/**
 * The sandbox's workspace: its terminals and chats with the side panels. ?session= is the
 * terminal's old name in links, so both open a terminal.
 */
export const Route = createFileRoute("/e/$env/sandboxes/$id/work")({
  validateSearch: (
    s: Record<string, unknown>
  ): { terminal?: string; chat?: string; panel?: WorkspacePanelId } => ({
    terminal: text(s.terminal) ?? text(s.session),
    chat: text(s.chat),
    panel: workspacePanels.find((p) => p === s.panel),
  }),
  component: WorkspaceRoute,
})

function WorkspaceRoute() {
  const search = Route.useSearch()
  const navigate = Route.useNavigate()
  return (
    <SandboxWorkspace
      search={search}
      onPanel={(panel) =>
        void navigate({ search: (prev) => ({ ...prev, panel }), replace: true })
      }
    />
  )
}
