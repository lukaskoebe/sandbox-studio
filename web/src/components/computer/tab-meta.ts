import {
  BrowserIcon,
  FolderIcon,
  GitDiffIcon,
  InfoIcon,
  MonitorIcon,
  TerminalWindowIcon,
  type Icon,
} from "@phosphor-icons/react"
import type { ScreenTab } from "@/lib/computer"

/** The screen's tabs by name and icon, as the bezel and the palette show them. */
export const tabMeta: Record<ScreenTab, { label: string; icon: Icon }> = {
  terminal: { label: "Terminal", icon: TerminalWindowIcon },
  browser: { label: "Browser", icon: BrowserIcon },
  preview: { label: "Preview", icon: MonitorIcon },
  files: { label: "Files", icon: FolderIcon },
  changes: { label: "Changes", icon: GitDiffIcon },
  details: { label: "Details", icon: InfoIcon },
}
