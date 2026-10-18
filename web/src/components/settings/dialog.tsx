import { useState, type ReactNode } from "react"
import { useNavigate } from "@tanstack/react-router"
import {
  CpuIcon,
  GearSixIcon,
  GitBranchIcon,
  GlobeIcon,
  PlugsIcon,
  StackIcon,
} from "@phosphor-icons/react"
import { InDialog } from "@/components/page-header"
import { Dialog, DialogContent, DialogTitle } from "@/components/ui/dialog"
import { GeneralSettings } from "@/components/settings/general"
import { GitSettings } from "@/components/settings/git"
import { SettingsLink, type SettingsSection } from "@/components/settings/link"
import { NetworkSettings } from "@/components/settings/network"
import { ProvidersSettings } from "@/components/settings/providers"
import { RuntimeSettings } from "@/components/settings/runtime"
import { TemplatesSettings } from "@/components/settings/templates"
import { cn } from "@/lib/utils"

const sections: { id: SettingsSection; label: string; icon: ReactNode }[] = [
  { id: "general", label: "General", icon: <GearSixIcon /> },
  { id: "providers", label: "Providers", icon: <PlugsIcon /> },
  { id: "git", label: "Git", icon: <GitBranchIcon /> },
  { id: "network", label: "Network", icon: <GlobeIcon /> },
  { id: "templates", label: "Templates", icon: <StackIcon /> },
  { id: "runtime", label: "Runtime", icon: <CpuIcon /> },
]

/**
 * Settings, as a dialog over the page on show. ?settings=<section> opens it, so a section can be
 * linked; closing it drops the parameter and leaves the page as it was. The sections sit in a
 * side column on wide screens and in a row that scrolls sideways on phones.
 */
export function SettingsDialog({
  env,
  section,
}: {
  env: string
  section?: SettingsSection
}) {
  const navigate = useNavigate()
  // The section stays drawn while the dialog closes.
  const [shown, setShown] = useState(section)
  if (section && section !== shown) setShown(section)
  const close = () =>
    void navigate({
      to: ".",
      search: (prev: Record<string, unknown>) => ({
        ...prev,
        settings: undefined,
      }),
    })

  return (
    <Dialog
      open={section !== undefined}
      onOpenChange={(open) => {
        if (!open) close()
      }}
    >
      <DialogContent
        bare
        className="flex h-[min(56rem,calc(100dvh-2rem))] flex-col overflow-hidden sm:max-w-[min(76rem,calc(100%-4rem))] md:flex-row"
      >
        <DialogTitle className="sr-only">Settings</DialogTitle>
        <div className="shrink-0 border-b md:w-48 md:border-r md:border-b-0">
          <p className="flex h-12 items-center border-b px-4 font-mono text-[0.625rem] tracking-wider text-muted-foreground uppercase">
            Settings
          </p>
          <nav
            aria-label="Settings"
            className="flex gap-1 overflow-x-auto p-2 md:flex-col md:overflow-visible"
          >
            {sections.map((s) => (
              <SettingsLink
                key={s.id}
                section={s.id}
                aria-current={s.id === shown ? "page" : undefined}
                className={cn(
                  "flex h-7 shrink-0 items-center gap-2 rounded-md px-2 text-[0.8125rem] text-muted-foreground hover:bg-foreground/5 hover:text-foreground [&_svg]:size-3.5",
                  s.id === shown &&
                    "bg-foreground/8 font-medium text-foreground"
                )}
              >
                {s.icon}
                {s.label}
              </SettingsLink>
            ))}
          </nav>
        </div>
        <div className="flex min-h-0 min-w-0 flex-1 flex-col">
          <InDialog.Provider value={true}>
            {shown && <Section env={env} section={shown} />}
          </InDialog.Provider>
        </div>
      </DialogContent>
    </Dialog>
  )
}

function Section({ env, section }: { env: string; section: SettingsSection }) {
  switch (section) {
    case "general":
      return <GeneralSettings env={env} />
    case "providers":
      return <ProvidersSettings env={env} />
    case "git":
      return <GitSettings env={env} />
    case "network":
      return <NetworkSettings env={env} />
    case "templates":
      return <TemplatesSettings env={env} />
    case "runtime":
      return <RuntimeSettings />
  }
}
