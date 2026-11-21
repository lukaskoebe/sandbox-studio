import { useState } from "react"
import { ArrowsInLineVerticalIcon } from "@phosphor-icons/react"
import { Button } from "@/components/ui/button"
import {
  Popover,
  PopoverContent,
  PopoverTitle,
  PopoverTrigger,
} from "@/components/ui/popover"
import type { ChatTurn } from "@/lib/chats"
import { cn } from "@/lib/utils"

/** How full the agent's context is, from the latest turn that said. */
export type ContextUse = { used: number; size: number }

/** From this share on, the meter warns that the agent will soon summarize on its own. */
const nearlyFull = 0.8

/** The context the agent last reported, leaving out undone turns; undefined if none did. */
export function latestContext(turns: ChatTurn[]): ContextUse | undefined {
  for (let i = turns.length - 1; i >= 0; i--) {
    const t = turns[i]
    const u = t.usage
    if (t.undoneAt || !u?.contextSize || u.contextUsed === undefined) continue
    return { used: u.contextUsed, size: u.contextSize }
  }
  return undefined
}

/** 124k, 1.2M: a token count at a glance. */
function tokens(n: number): string {
  return Intl.NumberFormat(undefined, {
    notation: "compact",
    maximumFractionDigits: 1,
  }).format(n)
}

/**
 * How full the agent's context is, as a ring and a share beside the composer. Its popover says
 * the counts and compacts the conversation: the agent's /compact, which summarizes it to make
 * room, as the agent does on its own once it runs full.
 */
export function ContextMeter({
  context,
  teammateName,
  running,
  onCompact,
}: {
  context: ContextUse
  teammateName: string
  /** The agent works: compacting waits until it is done. */
  running: boolean
  /** Sends /compact; the button waits for it, so a double click sends one. */
  onCompact: () => Promise<unknown>
}) {
  const [sending, setSending] = useState(false)
  const share = Math.min(1, context.used / context.size)
  const pct = Math.round(share * 100)
  const full = share >= nearlyFull
  return (
    <Popover>
      <PopoverTrigger
        render={
          <button
            type="button"
            aria-label={`Context: ${pct}% full`}
            title={`Context: ${pct}% full`}
            className={cn(
              "inline-flex h-6 items-center gap-1 rounded-md px-1.5 text-xs text-muted-foreground tabular-nums outline-none hover:bg-muted hover:text-foreground focus-visible:ring-2 focus-visible:ring-ring/40",
              full && "text-(--st-waiting-fg)"
            )}
          />
        }
      >
        <Ring share={share} />
        {pct}%
      </PopoverTrigger>
      <PopoverContent align="start" className="w-72 gap-2.5">
        <div className="flex items-baseline justify-between gap-2">
          <PopoverTitle className="text-sm">Context</PopoverTitle>
          <span className="text-xs text-muted-foreground tabular-nums">
            {tokens(context.used)} of {tokens(context.size)} tokens
          </span>
        </div>
        <div
          role="meter"
          aria-label="Context used"
          aria-valuemin={0}
          aria-valuemax={100}
          aria-valuenow={pct}
          className="h-1.5 overflow-hidden rounded-full bg-muted"
        >
          <div
            className={cn(
              "h-full rounded-full bg-primary transition-[width]",
              full && "bg-(--st-waiting-fg)"
            )}
            style={{ width: `${pct}%` }}
          />
        </div>
        <p className="text-xs text-muted-foreground">
          What {teammateName} keeps in mind of this thread. Once it runs full,{" "}
          {teammateName} summarizes the conversation to make room. Compact now
          to choose when; the summary takes a moment.
        </p>
        <Button
          size="sm"
          variant="outline"
          disabled={running || sending}
          onClick={() => {
            setSending(true)
            void onCompact().finally(() => setSending(false))
          }}
        >
          <ArrowsInLineVerticalIcon />
          {running ? `Once ${teammateName} is done` : "Compact now"}
        </Button>
      </PopoverContent>
    </Popover>
  )
}

/** A ring that fills clockwise from the top. */
function Ring({ share }: { share: number }) {
  const r = 5
  const length = 2 * Math.PI * r
  return (
    <svg viewBox="0 0 14 14" className="size-3.5 -rotate-90" aria-hidden>
      <circle
        cx="7"
        cy="7"
        r={r}
        fill="none"
        strokeWidth="2"
        className="stroke-current opacity-20"
      />
      <circle
        cx="7"
        cy="7"
        r={r}
        fill="none"
        strokeWidth="2"
        strokeLinecap="round"
        strokeDasharray={`${share * length} ${length}`}
        className="stroke-current"
      />
    </svg>
  )
}
