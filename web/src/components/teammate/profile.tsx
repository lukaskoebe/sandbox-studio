import { useEffect, useRef, useState, type ReactNode } from "react"
import {
  CaretRightIcon,
  ClockIcon,
  CopyIcon,
  GlobeIcon,
  KeyIcon,
  PlusIcon,
  RobotIcon,
  SlidersHorizontalIcon,
  SparkleIcon,
  TrashIcon,
  XIcon,
  type Icon,
} from "@phosphor-icons/react"
import { CreateSandboxDialog } from "@/components/create-sandbox-dialog"
import { phase, phaseDot } from "@/components/status-badge"
import { StatusDot } from "@/components/status-dot"
import { TaskDialog } from "@/components/task-dialog"
import { TeammateMemory } from "@/components/teammate/memory"
import { LevelSummary, TeammateSettings } from "@/components/teammate/settings"
import { TeammateTasks } from "@/components/teammate/tasks"
import { TeammateAvatar } from "@/components/teammate-avatar"
import { CopyTeammateDialog } from "@/components/teammate-options-menu"
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
import { $api, type Sandbox, type Teammate } from "@/lib/api/client"
import type { ProfileView } from "@/lib/computer"
import { heartbeatSentence } from "@/lib/tasks"
import { harnessLabels } from "@/lib/teammates"
import { cn } from "@/lib/utils"

type Open = (view: ProfileView | undefined, section?: string) => void

/**
 * The teammate's profile card, from its name: its open tasks, what it remembers, its computers
 * and the settings that matter day to day. Each heading opens the whole of it in a sheet.
 */
export function ProfileCard({
  env,
  teammate,
  computers,
  onOpen,
  onComputer,
}: {
  env: string
  teammate: Teammate
  computers: Sandbox[]
  onOpen: Open
  onComputer: (id: string) => void
}) {
  const id = teammate.id
  const state = usePresence(env)(id)
  const tasks = $api.useQuery(
    "get",
    "/api/environments/{env}/teammates/{id}/tasks",
    { params: { path: { env, id } } }
  )
  const facts = $api.useQuery("get", "/api/environments/{env}/memory/facts", {
    params: {
      path: { env },
      query: { scope: `teammate:${id}`, status: "active" },
    },
  })
  const open = (tasks.data ?? []).filter((t) => t.status !== "done")
  const known = facts.data ?? []
  const [adding, setAdding] = useState<"task" | "computer" | "copy">()
  const close = useRef<HTMLButtonElement>(null)
  useEffect(() => close.current?.focus(), [])

  return (
    <div className="absolute inset-0 z-30" onClick={() => onOpen(undefined)}>
      <div className="absolute inset-0 animate-in bg-background/50 duration-150 fade-in-0" />
      <div
        role="dialog"
        aria-label={`${teammate.name}'s profile`}
        onClick={(e) => e.stopPropagation()}
        className="absolute top-2 left-2 flex max-h-[calc(100%-1rem)] w-[min(24rem,calc(100%-1rem))] animate-in flex-col overflow-hidden rounded-xl border bg-popover text-popover-foreground shadow-lg duration-150 fade-in-0 zoom-in-95"
      >
        <div className="flex items-start gap-3 border-b px-4 py-3.5">
          <TeammateAvatar
            name={teammate.name}
            avatar={teammate.avatar}
            className="size-11 text-base"
          />
          <div className="grid min-w-0 flex-1 gap-0.5 leading-tight">
            <span className="truncate font-heading text-base font-semibold">
              {teammate.name}
            </span>
            <span className="truncate text-xs text-muted-foreground">
              {[teammate.role, harnessLabels[teammate.harness]]
                .filter(Boolean)
                .join(" · ")}
            </span>
            <span className="truncate text-xs text-muted-foreground">
              {teammate.model && (
                <span className="font-mono">{teammate.model} · </span>
              )}
              {presenceLabels[state]}
            </span>
          </div>
          <Button
            ref={close}
            variant="ghost"
            size="icon-sm"
            aria-label="Close"
            className="-mt-1 -mr-1.5 text-muted-foreground"
            onClick={() => onOpen(undefined)}
          >
            <XIcon />
          </Button>
        </div>
        <ScrollArea className="min-h-0 flex-1">
          <Section
            title="Tasks"
            count={tasks.data ? `${open.length} open` : undefined}
            onTitle={() => onOpen("tasks")}
            action={<Add onClick={() => setAdding("task")}>New task</Add>}
          >
            {open.slice(0, 3).map((t) => (
              <Item key={t.id} onClick={() => onOpen("tasks")}>
                <span className="size-3 shrink-0 rounded-sm border border-foreground/30" />
                <span className="min-w-0 flex-1 truncate">{t.title}</span>
                {t.due && (
                  <span className="shrink-0 text-muted-foreground">
                    due {t.due}
                  </span>
                )}
              </Item>
            ))}
            {tasks.data && open.length === 0 && (
              <p className="px-4 pb-1 text-xs text-muted-foreground">
                Nothing open.
              </p>
            )}
            {open.length > 3 && (
              <More onClick={() => onOpen("tasks")}>
                {open.length - 3} more
              </More>
            )}
          </Section>
          <Section
            title="Memory"
            count={facts.data ? `${known.length} facts` : undefined}
            onTitle={() => onOpen("memory")}
          >
            {known.slice(0, 3).map((f) => (
              <Item key={f.id} onClick={() => onOpen("memory")}>
                <SparkleIcon
                  weight="fill"
                  className="mt-0.5 size-3 shrink-0 self-start text-(--acc-memory)"
                />
                <span className="line-clamp-2 min-w-0 flex-1">{f.text}</span>
              </Item>
            ))}
            {facts.data && known.length === 0 && (
              <p className="px-4 pb-1 text-xs text-muted-foreground">
                Nothing yet: it learns as it works, or teach it from the
                composer.
              </p>
            )}
            {known.length > 3 && (
              <More onClick={() => onOpen("memory")}>
                {known.length - 3} more
              </More>
            )}
          </Section>
          <Section
            title="Computers"
            action={<Add onClick={() => setAdding("computer")}>Another</Add>}
          >
            {computers.map((sb) => (
              <Item key={sb.id} onClick={() => onComputer(sb.id)}>
                <StatusDot {...phaseDot(phase(sb))} compact />
                <span className="min-w-0 flex-1 truncate font-mono">
                  {sb.name}
                </span>
                <span className="shrink-0 text-muted-foreground">
                  {phaseDot(phase(sb)).label}
                </span>
                <CaretRightIcon className="size-3 shrink-0 text-muted-foreground" />
              </Item>
            ))}
            {computers.length === 0 && (
              <p className="px-4 pb-1 text-xs text-muted-foreground">
                None yet.
              </p>
            )}
          </Section>
          <Section title="Settings" onTitle={() => onOpen("settings")}>
            <Setting
              icon={RobotIcon}
              label="Agent"
              value={harnessLabels[teammate.harness]}
              onClick={() => onOpen("settings", "agent")}
            />
            <Setting
              icon={ClockIcon}
              label="Check-ins"
              value={
                teammate.heartbeat
                  ? heartbeatSentence(teammate.heartbeat)
                  : "Off"
              }
              onClick={() => onOpen("settings", "checkins")}
            />
            <Setting
              icon={GlobeIcon}
              label="New hosts"
              value={<LevelSummary env={env} teammate={teammate} />}
              onClick={() => onOpen("settings", "network")}
            />
            <Setting
              icon={KeyIcon}
              label="Secrets"
              onClick={() => onOpen("settings", "secrets")}
            />
            <Setting
              icon={SlidersHorizontalIcon}
              label="All settings"
              onClick={() => onOpen("settings")}
            />
          </Section>
          <div className="flex items-center gap-1 px-2 py-2">
            <Button
              variant="ghost"
              size="sm"
              className="text-muted-foreground"
              onClick={() => setAdding("copy")}
            >
              <CopyIcon />
              Copy to environment…
            </Button>
            <Button
              variant="ghost"
              size="sm"
              className="ml-auto text-destructive hover:text-destructive"
              onClick={() => onOpen("settings", "delete")}
            >
              <TrashIcon />
              Remove {teammate.name}
            </Button>
          </div>
        </ScrollArea>
      </div>
      <div onClick={(e) => e.stopPropagation()}>
        <TaskDialog
          env={env}
          teammateId={id}
          open={adding === "task"}
          onOpenChange={(on) => setAdding(on ? "task" : undefined)}
        />
        <CreateSandboxDialog
          env={env}
          teammate={id}
          open={adding === "computer"}
          onOpenChange={(on) => setAdding(on ? "computer" : undefined)}
        />
        <CopyTeammateDialog
          env={env}
          teammate={teammate}
          open={adding === "copy"}
          onOpenChange={(on) => setAdding(on ? "copy" : undefined)}
        />
      </div>
    </div>
  )
}

function Section({
  title,
  count,
  onTitle,
  action,
  children,
}: {
  title: string
  count?: string
  onTitle?: () => void
  action?: ReactNode
  children: ReactNode
}) {
  const label = (
    <>
      {title}
      {count && (
        <span className="tracking-normal normal-case">&nbsp; {count}</span>
      )}
    </>
  )
  return (
    <section className="grid gap-0.5 border-b py-2.5 last:border-b-0">
      <div className="flex h-6 items-center gap-2 px-4">
        {onTitle ? (
          <button
            type="button"
            onClick={onTitle}
            className="section-label text-muted-foreground hover:text-foreground"
          >
            {label}
          </button>
        ) : (
          <span className="section-label text-muted-foreground">{label}</span>
        )}
        <span className="ml-auto">{action}</span>
      </div>
      {children}
    </section>
  )
}

function Add({
  onClick,
  children,
}: {
  onClick: () => void
  children: ReactNode
}) {
  return (
    <button
      type="button"
      onClick={onClick}
      className="inline-flex items-center gap-1 text-xs text-muted-foreground hover:text-foreground"
    >
      <PlusIcon className="size-3" />
      {children}
    </button>
  )
}

function Item({
  onClick,
  children,
}: {
  onClick: () => void
  children: ReactNode
}) {
  return (
    <button
      type="button"
      onClick={onClick}
      className="mx-1.5 flex min-h-8 items-center gap-2.5 rounded-md px-2.5 py-1 text-left text-[0.8125rem] outline-none hover:bg-muted focus-visible:bg-muted"
    >
      {children}
    </button>
  )
}

function More({
  onClick,
  children,
}: {
  onClick: () => void
  children: ReactNode
}) {
  return (
    <button
      type="button"
      onClick={onClick}
      className="mx-4 justify-self-start text-xs text-muted-foreground hover:text-foreground"
    >
      {children}
    </button>
  )
}

function Setting({
  icon: I,
  label,
  value,
  onClick,
}: {
  icon: Icon
  label: string
  value?: ReactNode
  onClick: () => void
}) {
  return (
    <Item onClick={onClick}>
      <I className="size-3.5 shrink-0 text-muted-foreground" />
      <span className="shrink-0">{label}</span>
      <span className="ml-auto min-w-0 truncate text-xs text-muted-foreground">
        {value}
      </span>
      <CaretRightIcon className="size-3 shrink-0 text-muted-foreground" />
    </Item>
  )
}

const sheets: Record<
  Exclude<ProfileView, "card">,
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

/** The whole of one part of the profile, wide, over the page. */
export function ProfileSheet({
  env,
  teammate,
  view,
  section,
  fromSetup,
  onOpen,
}: {
  env: string
  teammate: Teammate
  view?: ProfileView
  /** The settings card that is open. */
  section?: string
  fromSetup?: boolean
  onOpen: Open
}) {
  // The sheet keeps its content while it slides out.
  const [shown, setShown] = useState<Exclude<ProfileView, "card">>()
  const open = view !== undefined && view !== "card"
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
                <TeammateAvatar
                  name={teammate.name}
                  avatar={teammate.avatar}
                  className="size-6 text-[0.625rem]"
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
                <TeammateMemory env={env} id={teammate.id} />
              )}
              {shown === "settings" && (
                <TeammateSettings
                  env={env}
                  id={teammate.id}
                  open={section}
                  onOpen={(s) => onOpen("settings", s)}
                  fromSetup={fromSetup}
                />
              )}
            </ScrollArea>
          </>
        )}
      </SheetContent>
    </Sheet>
  )
}
