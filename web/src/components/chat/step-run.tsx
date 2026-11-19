import { useEffect, useMemo, useState } from "react"
import {
  CaretRightIcon,
  ChatCircleDotsIcon,
  ShieldCheckIcon,
} from "@phosphor-icons/react"
import { ChatItemView, type ItemContext } from "@/components/chat/chat-items"
import { PermissionCard } from "@/components/chat/permission-card"
import { QuestionCard } from "@/components/chat/question-card"
import {
  clockLabel,
  editedFiles,
  spanLabel,
  stepSummary,
  type Run,
} from "@/components/chat/step-runs"
import { LineCounts, ToolCallCard } from "@/components/chat/tool-call-card"
import {
  Collapsible,
  CollapsibleContent,
  CollapsibleTrigger,
} from "@/components/ui/collapsible"
import type { ChatItem, ChatTurn } from "@/lib/chats"
import { cn } from "@/lib/utils"

/** The most file chips a row shows; the rest are counted. */
const maxChips = 4

/** The most lines an open fold shows while it works; the oldest merge into one. */
const maxLines = 5

/**
 * A run of steps between two of the agent's messages, as one row: how long it worked, how many
 * steps, a chip per file it edited with the lines added and removed, and how many failed. While
 * the run goes on it is open, its newest step streams and the rest are lines; it closes once the
 * agent writes again, unless it was opened or closed by hand. A file chip opens the file's diff
 * in Changes once the request is done.
 */
export function StepRun({
  run,
  turn,
  open,
  onOpenChange,
  ctx,
  onShowFile,
}: {
  run: Run
  turn?: ChatTurn
  open: boolean
  onOpenChange: (open: boolean) => void
  ctx: Omit<ItemContext, "live">
  /** Opens a file's diff among the request's changes. */
  onShowFile?: (path: string) => void
}) {
  const files = useMemo(() => editedFiles(run.items), [run.items])
  const failed = run.items.filter(
    (i) => i.kind === "tool" && i.tool?.status === "failed"
  ).length
  const now = useNow(run.live)
  const start = Date.parse(
    run.from ?? turn?.startedAt ?? run.items[0].updatedAt
  )
  const end = run.live
    ? now
    : Math.max(...run.items.map((i) => Date.parse(i.updatedAt)))
  const took = Math.max(0, end - start)
  const steps = run.items.length
  const summary = stepSummary(run.items)
  const done = !!turn?.endedAt
  const chips = files.slice(0, maxChips)

  return (
    <Collapsible
      open={open}
      onOpenChange={onOpenChange}
      className="group/run grid min-w-0 gap-1.5 text-xs"
    >
      <div className="flex min-w-0 flex-wrap items-center gap-x-2 gap-y-1">
        <CollapsibleTrigger
          className="flex min-w-0 items-center gap-1.5 rounded-sm text-left text-muted-foreground outline-none hover:text-foreground focus-visible:ring-2 focus-visible:ring-ring/30"
          title={summary}
        >
          <CaretRightIcon className="size-3 shrink-0 transition-transform group-data-[open]/run:rotate-90" />
          <span>
            {run.live ? (
              <>
                <span className={cn(!run.waiting && "shimmer")}>
                  {run.waiting ? "Waiting for you" : "Working"}
                </span>{" "}
                · <span className="tabular-nums">{clockLabel(took)}</span>
              </>
            ) : (
              <>Worked for {spanLabel(took)}</>
            )}{" "}
            · {steps} {steps === 1 ? "step" : "steps"}
          </span>
        </CollapsibleTrigger>
        {chips.map((f) => {
          const name = f.path.split("/").at(-1) || f.path
          const body = (
            <>
              <span className="truncate font-mono">{name}</span>
              <LineCounts added={f.added} removed={f.removed} />
            </>
          )
          const cls =
            "inline-flex h-5 max-w-48 min-w-0 items-center gap-1 rounded-sm border px-1.5 text-caption"
          return done && onShowFile ? (
            <button
              key={f.path}
              type="button"
              className={cn(
                cls,
                "outline-none hover:bg-accent focus-visible:ring-2 focus-visible:ring-ring/30"
              )}
              title={`Show the changes to ${f.path}`}
              onClick={() => onShowFile(f.path)}
            >
              {body}
            </button>
          ) : (
            <span key={f.path} className={cls} title={f.path}>
              {body}
            </span>
          )
        })}
        {files.length > maxChips && (
          <span className="text-caption text-muted-foreground">
            +{files.length - maxChips}{" "}
            {files.length - maxChips === 1 ? "file" : "files"}
          </span>
        )}
        {failed > 0 && (
          <span className="font-medium text-destructive">{failed} failed</span>
        )}
      </div>
      <CollapsibleContent className="ml-1.5 grid min-w-0 gap-1 border-l-2 pl-3">
        <Steps items={run.items} live={run.live} ctx={ctx} />
      </CollapsibleContent>
    </Collapsible>
  )
}

/** The steps of an open fold: a line each, the newest open while it works. */
function Steps({
  items,
  live,
  ctx,
}: {
  items: ChatItem[]
  live: boolean
  ctx: Omit<ItemContext, "live">
}) {
  const [all, setAll] = useState(false)
  const merged = live && !all && items.length > maxLines
  const hidden = merged ? items.slice(0, items.length - (maxLines - 1)) : []
  const shown = merged ? items.slice(hidden.length) : items
  return (
    <>
      {merged && (
        <button
          type="button"
          className="flex min-w-0 items-center gap-1.5 rounded-sm py-0.5 text-left text-muted-foreground outline-none hover:text-foreground focus-visible:ring-2 focus-visible:ring-ring/30"
          onClick={() => setAll(true)}
        >
          <CaretRightIcon className="size-3 shrink-0" />
          <span className="truncate">{stepSummary(hidden)}</span>
        </button>
      )}
      {shown.map((item, i) => (
        <Step
          key={item.id}
          item={item}
          ctx={{ ...ctx, live: live && i === shown.length - 1 }}
        />
      ))}
    </>
  )
}

const allowed: Record<string, string> = {
  approved: "Allowed",
  denied: "Denied",
  dismissed: "Ignored",
  cancelled: "No longer needed",
}

const answered: Record<string, string> = {
  answered: "Answered",
  declined: "Skipped",
  cancelled: "No longer needed",
}

/**
 * One step: a tool call as a line that opens to its details, a thought, or an ask you settled
 * as a line that opens to its card. The step still being written shows in full.
 */
function Step({ item, ctx }: { item: ChatItem; ctx: ItemContext }) {
  if (item.kind === "tool" && item.tool)
    return ctx.live ? (
      <ToolCallCard tool={item.tool} />
    ) : (
      <ToolCallCard tool={item.tool} line />
    )
  if (item.kind === "permission" && item.permission) {
    const tc = item.permission.toolCall
    return (
      <SettledAsk
        icon={ShieldCheckIcon}
        text={`${allowed[item.status ?? ""] ?? item.status} · ${tc.title || "a step"}`}
      >
        <PermissionCard
          env={ctx.env}
          item={item}
          teammateName={ctx.teammateName}
        />
      </SettledAsk>
    )
  }
  if (item.kind === "question" && item.question)
    return (
      <SettledAsk
        icon={ChatCircleDotsIcon}
        text={`${answered[item.status ?? ""] ?? item.status} · ${item.question.message}`}
      >
        <QuestionCard
          env={ctx.env}
          chatId={ctx.chatId}
          item={item}
          teammateName={ctx.teammateName}
        />
      </SettledAsk>
    )
  return <ChatItemView item={item} ctx={ctx} />
}

function SettledAsk({
  icon: Icon,
  text,
  children,
}: {
  icon: typeof ShieldCheckIcon
  text: string
  children: React.ReactNode
}) {
  return (
    <Collapsible className="group/ask grid min-w-0 gap-1 text-xs">
      <CollapsibleTrigger className="flex w-full min-w-0 items-center gap-2 rounded-sm py-0.5 text-left text-muted-foreground outline-none hover:text-foreground focus-visible:ring-2 focus-visible:ring-ring/30">
        <Icon className="size-3.5 shrink-0" />
        <span className="min-w-0 flex-1 truncate">{text}</span>
        <CaretRightIcon className="size-3 shrink-0 transition-transform group-data-[open]/ask:rotate-90" />
      </CollapsibleTrigger>
      <CollapsibleContent>{children}</CollapsibleContent>
    </Collapsible>
  )
}

/** The time now, every second while `ticking`. */
function useNow(ticking: boolean) {
  const [now, setNow] = useState(() => Date.now())
  useEffect(() => {
    if (!ticking) return
    const t = setInterval(() => setNow(Date.now()), 1000)
    return () => clearInterval(t)
  }, [ticking])
  return now
}
