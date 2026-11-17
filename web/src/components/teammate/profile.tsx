import { Fragment, useState } from "react"
import { ArrowLeftIcon, ChatCircleIcon } from "@phosphor-icons/react"
import { TeammateMemory } from "@/components/teammate/memory"
import { TeammateSettings } from "@/components/teammate/settings"
import { TeammateTasks } from "@/components/teammate/tasks"
import { TeammateAvatar } from "@/components/teammate-avatar"
import { Button } from "@/components/ui/button"
import { ScrollArea } from "@/components/ui/scroll-area"
import {
  Sheet,
  SheetContent,
  SheetDescription,
  SheetHeader,
  SheetTitle,
} from "@/components/ui/sheet"
import { presenceLabels, usePresence } from "@/hooks/use-teammate-presence"
import type { Teammate } from "@/lib/api/client"
import type { ProfileView } from "@/lib/computer"
import { harnessLabels } from "@/lib/teammates"
import { cn } from "@/lib/utils"

type Open = (view: ProfileView | undefined, section?: string) => void

const sheets: Record<
  ProfileView,
  { title: (name: string) => string; description: string }
> = {
  tasks: {
    title: (name) => `${name}'s tasks`,
    description: "What it works on, and what its check-ins and schedules ran.",
  },
  memory: {
    title: (name) => `What ${name} remembers`,
    description:
      "Only this teammate sees its own memory, and your edits outrank anything inferred.",
  },
  settings: {
    title: (name) => `${name}'s settings`,
    description: "Who it is, the agent it runs, and what it may do.",
  },
}

/**
 * A teammate's settings as a page of its own, in place of the conversation and the screen:
 * Profile, Knowledge, and Routine and access, as rows that open one at a time. Its memory and
 * tasks open over it as sheets, which lead back here.
 */
export function ProfileSettingsPage({
  env,
  teammate,
  section,
  fromSetup,
  onOpen,
  onView,
}: {
  env: string
  teammate: Teammate
  section?: string
  fromSetup?: boolean
  onOpen: Open
  /** Opens the teammate's memory or tasks, from the row of that name. */
  onView: (view: "memory" | "tasks") => void
}) {
  const meta = sheets.settings
  return (
    <ScrollArea
      role="region"
      aria-label={meta.title(teammate.name)}
      className="min-h-0 flex-1"
      viewportClassName="px-4 pt-6 pb-10 md:px-8"
    >
      <div className="mx-auto grid w-full max-w-3xl min-w-0 gap-5">
        <header className="flex min-w-0 flex-wrap items-start gap-x-4 gap-y-2">
          <div className="grid min-w-0 flex-1 gap-1">
            <h1 className="flex min-w-0 items-center gap-2.5 text-title font-semibold">
              <TeammateAvatar
                name={teammate.name}
                avatar={teammate.avatar}
                className="size-7 text-xs"
              />
              <span className="truncate">{meta.title(teammate.name)}</span>
            </h1>
            <Identity env={env} teammate={teammate} />
          </div>
          <Button variant="outline" size="sm" onClick={() => onOpen(undefined)}>
            <ChatCircleIcon />
            Conversation
          </Button>
        </header>
        <TeammateSettings
          env={env}
          id={teammate.id}
          open={section}
          onOpen={(s) => onOpen("settings", s)}
          onView={onView}
          fromSetup={fromSetup}
        />
      </div>
    </ScrollArea>
  )
}

/** The whole of one part of the profile, wide, over the page. */
export function ProfileSheet({
  env,
  teammate,
  view,
  section,
  back,
  onOpen,
}: {
  env: string
  teammate: Teammate
  view?: ProfileView
  /** The memory's page or group that is open. */
  section?: string
  /** The settings row the sheet was opened from, which its back button returns to. */
  back?: string
  onOpen: Open
}) {
  // The sheet keeps its content while it slides out.
  const [shown, setShown] = useState<ProfileView>()
  // The settings are a page of their own (ProfileSettingsPage).
  const open = view === "tasks" || view === "memory"
  if (open && view !== shown) setShown(view)
  const meta = shown ? sheets[shown] : undefined

  return (
    <Sheet open={open} onOpenChange={(on) => !on && onOpen(undefined)}>
      <SheetContent
        side="right"
        className={cn(
          "gap-0 p-0 data-[side=right]:w-full data-[side=right]:sm:max-w-2xl",
          shown === "memory" && "data-[side=right]:sm:max-w-4xl"
        )}
      >
        {meta && (
          <>
            <SheetHeader className="border-b px-5 py-4">
              <SheetTitle className="flex items-center gap-2.5 text-sm">
                {back !== undefined && (
                  <Button
                    variant="ghost"
                    size="icon-sm"
                    className="-ml-1.5"
                    aria-label={`Back to ${teammate.name}'s settings`}
                    onClick={() => onOpen("settings", back)}
                  >
                    <ArrowLeftIcon />
                  </Button>
                )}
                <TeammateAvatar
                  name={teammate.name}
                  avatar={teammate.avatar}
                  className="size-6 text-meta"
                />
                {meta.title(teammate.name)}
              </SheetTitle>
              <SheetDescription>{meta.description}</SheetDescription>
            </SheetHeader>
            <ScrollArea className="min-h-0 flex-1" viewportClassName="p-5">
              {shown === "tasks" && (
                <TeammateTasks
                  env={env}
                  id={teammate.id}
                  onCheckins={() => onOpen("settings", "checkins")}
                />
              )}
              {shown === "memory" && (
                <TeammateMemory
                  env={env}
                  id={teammate.id}
                  open={section}
                  onOpen={(s) => onOpen("memory", s)}
                />
              )}
            </ScrollArea>
          </>
        )}
      </SheetContent>
    </Sheet>
  )
}

/** Who the teammate is at a glance: its role, agent and model, and what it does right now. */
function Identity({ env, teammate }: { env: string; teammate: Teammate }) {
  const state = usePresence(env)(teammate.id)
  const parts = [
    teammate.role,
    harnessLabels[teammate.harness],
    teammate.model && <span className="font-mono">{teammate.model}</span>,
    presenceLabels[state],
  ].filter(Boolean)
  // Each dot ends the part before it, so a part that wraps starts its line.
  return (
    <p className="flex min-w-0 flex-wrap gap-x-1.5 text-xs text-muted-foreground">
      {parts.map((part, i) => (
        <Fragment key={i}>
          {i > 0 && " "}
          <span className="min-w-0 truncate">
            {part}
            {i < parts.length - 1 && " ·"}
          </span>
        </Fragment>
      ))}
    </p>
  )
}
