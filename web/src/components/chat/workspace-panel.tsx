import { useEffect, useMemo, useState, useSyncExternalStore } from "react"
import { useQueryClient } from "@tanstack/react-query"
import {
  BroadcastIcon,
  FolderIcon,
  GitDiffIcon,
  XIcon,
} from "@phosphor-icons/react"
import { ChangesPanel } from "@/components/chat/changes-panel"
import { FilesPanel } from "@/components/chat/files-panel"
import { PreviewPanel } from "@/components/chat/preview-panel"
import { Button } from "@/components/ui/button"
import { ScrollArea } from "@/components/ui/scroll-area"
import {
  Sheet,
  SheetContent,
  SheetDescription,
  SheetHeader,
  SheetTitle,
} from "@/components/ui/sheet"
import { Tabs, TabsContent, TabsList, TabsTrigger } from "@/components/ui/tabs"
import { changeCount, type Chat, type ChatTurn } from "@/lib/chats"

export type PanelTab = "changes" | "files" | "preview"

const wideQuery = "(min-width: 1024px)"
const storeKey = "studio.chatPanel"

function useWide() {
  return useSyncExternalStore(
    (onChange) => {
      const mql = window.matchMedia(wideQuery)
      mql.addEventListener("change", onChange)
      return () => mql.removeEventListener("change", onChange)
    },
    () => window.matchMedia(wideQuery).matches
  )
}

/**
 * Whether the workspace panel is open, which tab it shows and whose changes. Wide screens
 * remember whether it was open; narrow ones open it on demand as a sheet.
 */
export function useWorkspacePanel() {
  const wide = useWide()
  const [state, setState] = useState<{
    open: boolean
    tab: PanelTab
    turnId?: string
  }>(() => ({
    open:
      window.matchMedia(wideQuery).matches &&
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
 * The chat's side panel: Changes, Files and Preview of the chat's sandbox. A column next to
 * the chat on wide screens, a sheet on narrow ones.
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
  const body = (
    <PanelTabs
      env={env}
      chat={chat}
      turns={turns}
      label={label}
      panel={panel}
    />
  )
  if (!panel.wide) {
    return (
      <Sheet open={panel.open} onOpenChange={panel.setOpen}>
        <SheetContent className="w-full data-[side=right]:sm:max-w-lg">
          <SheetHeader className="pb-0">
            <SheetTitle>Workspace</SheetTitle>
            <SheetDescription>
              What your teammate changed, the sandbox's files, and its running
              app.
            </SheetDescription>
          </SheetHeader>
          {body}
        </SheetContent>
      </Sheet>
    )
  }
  if (!panel.open) return null
  return (
    <aside
      aria-label="Workspace"
      className="relative flex min-h-0 w-96 shrink-0 flex-col border-l xl:w-[30rem] 2xl:w-[38rem]"
    >
      <Button
        variant="ghost"
        size="icon-xs"
        title="Close the panel"
        className="absolute top-2.5 right-2"
        onClick={() => panel.setOpen(false)}
      >
        <XIcon />
      </Button>
      {body}
    </aside>
  )
}

function PanelTabs({
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
  const queryClient = useQueryClient()
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
      value={panel.tab}
      onValueChange={(v: PanelTab) => panel.setTab(v)}
      className="min-h-0 flex-1 gap-0"
    >
      <div className="flex h-12 shrink-0 items-center px-3 pr-10">
        <TabsList>
          <TabsTrigger value="changes">
            <GitDiffIcon />
            Changes
          </TabsTrigger>
          <TabsTrigger value="files">
            <FolderIcon />
            Files
          </TabsTrigger>
          <TabsTrigger value="preview">
            <BroadcastIcon />
            Preview
          </TabsTrigger>
        </TabsList>
      </div>
      <TabsContent value="changes" className="flex min-h-0 flex-col">
        <ScrollArea className="min-h-0 flex-1" viewportClassName="px-3 pb-3">
          <ChangesPanel
            env={env}
            chatId={chat.id}
            turns={changedTurns}
            turnId={turn?.id}
            onTurnChange={panel.setTurn}
            label={label}
          />
        </ScrollArea>
      </TabsContent>
      <TabsContent value="files" className="flex min-h-0 flex-col">
        <ScrollArea className="min-h-0 flex-1" viewportClassName="px-3 pb-3">
          <FilesPanel
            env={env}
            sandboxId={chat.sandboxId}
            dir={where.dir}
            file={where.file}
            onOpen={(dir, file) => setWhere({ dir, file })}
            changed={changed}
          />
        </ScrollArea>
      </TabsContent>
      <TabsContent value="preview" className="flex min-h-0 flex-col px-3 pb-3">
        <PreviewPanel
          env={env}
          sandboxId={chat.sandboxId}
          reloadKey={reloadKey}
        />
      </TabsContent>
    </Tabs>
  )
}
