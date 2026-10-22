import { useEffect, useMemo, useState } from "react"
import { useQueryClient } from "@tanstack/react-query"
import { BroadcastIcon, FolderIcon, GitDiffIcon } from "@phosphor-icons/react"
import { ChangesPanel } from "@/components/chat/changes-panel"
import { FilesPanel } from "@/components/chat/files-panel"
import { PreviewPanel } from "@/components/chat/preview-panel"
import { ScrollArea } from "@/components/ui/scroll-area"
import { SidePanel, useWideScreen } from "@/components/side-panel"
import { Tabs, TabsContent, TabsList, TabsTrigger } from "@/components/ui/tabs"
import { changeCount, type Chat, type ChatTurn } from "@/lib/chats"
import { usePreviewSetting } from "@/lib/preview-setting"

export type PanelTab = "changes" | "files" | "preview"

const storeKey = "studio.chatPanel"

/**
 * Whether the workspace panel is open, which tab it shows and whose changes. Wide screens
 * remember whether it was open; narrow ones open it on demand as a sheet.
 */
export function useWorkspacePanel() {
  const wide = useWideScreen()
  const [state, setState] = useState<{
    open: boolean
    tab: PanelTab
    turnId?: string
  }>(() => ({
    open:
      window.matchMedia("(min-width: 1024px)").matches &&
      localStorage.getItem(storeKey) !== "closed",
    tab: "changes",
  }))
  const setOpen = (open: boolean) => {
    if (wide) localStorage.setItem(storeKey, open ? "open" : "closed")
    setState((s) => ({ ...s, open }))
  }
  return {
    ...state,
    wide,
    setOpen,
    setTab: (tab: PanelTab) => setState((s) => ({ ...s, tab })),
    setTurn: (turnId: string) => setState((s) => ({ ...s, turnId })),
    /** Opens the Changes tab on one request. */
    showChanges: (turnId: string) => {
      if (wide) localStorage.setItem(storeKey, "open")
      setState({ open: true, tab: "changes", turnId })
    },
  }
}

export type WorkspacePanelState = ReturnType<typeof useWorkspacePanel>

/**
 * The chat's side panel: Changes, Files and Preview of the chat's sandbox. Opened from the
 * chat header; the side panel sizes itself and goes full screen on narrow screens.
 */
export function WorkspacePanel({
  env,
  chat,
  turns,
  label,
  panel,
}: {
  env: string
  chat: Chat
  turns: ChatTurn[]
  label: (t: ChatTurn) => string
  panel: WorkspacePanelState
}) {
  if (!panel.open) return null
  return (
    <SidePanel
      label="Workspace"
      title="Workspace"
      storageKey="studio.panel.chat"
      onClose={() => panel.setOpen(false)}
    >
      <WorkspaceTabs
        env={env}
        sandboxId={chat.sandboxId}
        chat={chat}
        turns={turns}
        label={label}
        panel={panel}
      />
    </SidePanel>
  )
}

/** The tab state a workspace needs; the chat's panel state has it, so does the sandbox's. */
export type WorkspaceTabsState = Pick<
  WorkspacePanelState,
  "tab" | "setTab" | "turnId" | "setTurn"
>

/**
 * Changes, Files and Preview of a sandbox. Changes belong to a chat, so it is empty without
 * one; Files and Preview show the sandbox as it is.
 */
export function WorkspaceTabs({
  env,
  sandboxId,
  chat,
  turns = [],
  label,
  panel,
}: {
  env: string
  sandboxId: string
  chat?: Chat
  turns?: ChatTurn[]
  label: (t: ChatTurn) => string
  panel: WorkspaceTabsState
}) {
  const queryClient = useQueryClient()
  const previews = usePreviewSetting().enabled
  const [where, setWhere] = useState<{ dir: string; file?: string }>({
    dir: "",
  })

  const changedTurns = useMemo(
    () => turns.filter((t) => t.endedAt && changeCount(t) > 0).reverse(),
    [turns]
  )
  const turn =
    changedTurns.find((t) => t.id === panel.turnId) ?? changedTurns.at(0)
  const changed = useMemo(
    () => new Set((turn?.changes?.files ?? []).map((f) => f.path)),
    [turn]
  )

  // Files and the app may change whenever a request ends or is undone.
  const ended = turns.filter((t) => t.endedAt).length
  const undone = turns.filter((t) => t.undoneAt).length
  const reloadKey = `${ended}:${undone}`
  useEffect(() => {
    for (const path of [
      "/api/environments/{env}/sandboxes/{id}/files",
      "/api/environments/{env}/sandboxes/{id}/files/content",
    ]) {
      void queryClient.invalidateQueries({ queryKey: ["get", path] })
    }
  }, [reloadKey, queryClient])

  return (
    <Tabs
      value={panel.tab === "preview" && !previews ? "changes" : panel.tab}
      onValueChange={(v: PanelTab) => panel.setTab(v)}
      className="min-h-0 flex-1 gap-0"
    >
      <div className="flex h-10 shrink-0 items-center px-3">
        <TabsList>
          <TabsTrigger value="changes">
            <GitDiffIcon />
            Changes
          </TabsTrigger>
          <TabsTrigger value="files">
            <FolderIcon />
            Files
          </TabsTrigger>
          {previews && (
            <TabsTrigger value="preview">
              <BroadcastIcon />
              Preview
            </TabsTrigger>
          )}
        </TabsList>
      </div>
      <TabsContent value="changes" className="flex min-h-0 flex-col">
        <ScrollArea className="min-h-0 flex-1" viewportClassName="px-3 pb-3">
          {chat ? (
            <ChangesPanel
              env={env}
              chatId={chat.id}
              turns={changedTurns}
              turnId={turn?.id}
              onTurnChange={panel.setTurn}
              label={label}
            />
          ) : (
            <p className="px-3 py-6 text-center text-xs text-muted-foreground">
              Changes show up here for a chat, once its teammate edits files.
            </p>
          )}
        </ScrollArea>
      </TabsContent>
      <TabsContent value="files" className="flex min-h-0 flex-col">
        <ScrollArea className="min-h-0 flex-1" viewportClassName="px-3 pb-3">
          <FilesPanel
            env={env}
            sandboxId={sandboxId}
            dir={where.dir}
            file={where.file}
            onOpen={(dir, file) => setWhere({ dir, file })}
            changed={changed}
          />
        </ScrollArea>
      </TabsContent>
      {previews && (
        <TabsContent
          value="preview"
          className="flex min-h-0 flex-col px-3 pb-3"
        >
          <PreviewPanel env={env} sandboxId={sandboxId} reloadKey={reloadKey} />
        </TabsContent>
      )}
    </Tabs>
  )
}
