import { useState } from "react"
import {
  ArrowUpIcon,
  GitBranchIcon,
  HandIcon,
  PencilSimpleIcon,
  SparkleIcon,
  TrashIcon,
} from "@phosphor-icons/react"
import { Button } from "@/components/ui/button"
import { ScrollArea } from "@/components/ui/scroll-area"
import { cn } from "@/lib/utils"
import { finn, journal, owner, topics, type MockPresence } from "./data"
import { LiveAvatar, Typing, presenceWords } from "./frame"

/** Concept 3: the teammate's journal of its days, and its memory as a map. */
export function JournalConcept({ presence }: { presence: MockPresence }) {
  return (
    <div className="flex min-h-0 flex-1">
      <div className="flex min-w-0 flex-1 flex-col">
        <header className="flex h-16 shrink-0 items-center gap-3 border-b px-6">
          <LiveAvatar presence={presence} size="sm" />
          <div className="grid">
            <h1 className="text-sm font-semibold">Finn's journal</h1>
            <span className="text-xs text-muted-foreground">
              Written by Finn from its sessions · {presenceWords[presence]}
            </span>
          </div>
        </header>
        <ScrollArea className="min-h-0 flex-1">
          <div className="mx-auto grid max-w-2xl gap-10 px-6 py-8">
            {journal.map((d, i) => (
              <Entry
                key={d.day}
                day={d}
                live={i === 0 && presence === "working"}
              />
            ))}
          </div>
        </ScrollArea>
        <div className="shrink-0 border-t px-6 py-3">
          <div className="mx-auto flex max-w-2xl items-center gap-2 rounded-xl border bg-card px-3 py-1.5">
            <input
              placeholder={`Write to ${finn.name}…`}
              className="h-8 flex-1 bg-transparent text-[0.8125rem] outline-none"
            />
            <Button size="icon-sm" aria-label="Send">
              <ArrowUpIcon />
            </Button>
          </div>
        </div>
      </div>
      <MemoryMap />
    </div>
  )
}

function Entry({
  day,
  live,
}: {
  day: (typeof journal)[number]
  live: boolean
}) {
  const [reply, setReply] = useState("")
  const [replied, setReplied] = useState<string>()
  const [forgot, setForgot] = useState<string[]>([])
  return (
    <article className="grid gap-4 text-[0.875rem] leading-relaxed">
      <div className="flex items-baseline gap-3">
        <h2 className="text-lg font-semibold tracking-tight">{day.day}</h2>
        <span className="font-mono text-[0.6875rem] text-muted-foreground">
          {day.date}
        </span>
        {live && (
          <span className="ml-auto flex items-center gap-1.5 text-xs text-muted-foreground">
            <Typing /> still writing
          </span>
        )}
      </div>
      <p className="text-pretty">{day.summary}</p>

      {day.shipped.length > 0 && (
        <div className="flex flex-wrap items-center gap-1.5">
          <span className="mr-1 font-mono text-[0.625rem] tracking-wider text-muted-foreground uppercase">
            Shipped
          </span>
          {day.shipped.map((s) => (
            <span
              key={s}
              className="inline-flex items-center gap-1 rounded-md bg-(--acc-git)/10 px-2 py-0.5 font-mono text-xs text-(--acc-git)"
            >
              <GitBranchIcon className="size-3" />
              {s}
            </span>
          ))}
        </div>
      )}

      {day.learned.length > 0 && (
        <div className="grid gap-1.5">
          <span className="font-mono text-[0.625rem] tracking-wider text-muted-foreground uppercase">
            Learned
          </span>
          <ul className="grid gap-1">
            {day.learned.map((f) => {
              const gone = forgot.includes(f)
              return (
                <li
                  key={f}
                  className="group flex items-start gap-2 rounded-md px-2 py-1 text-[0.8125rem] hover:bg-muted"
                >
                  <SparkleIcon
                    weight="fill"
                    className="mt-1 size-3 shrink-0 text-(--acc-memory)"
                  />
                  <span
                    className={cn(
                      "flex-1",
                      gone && "text-muted-foreground line-through"
                    )}
                  >
                    {f}
                  </span>
                  <span className="flex gap-0.5 opacity-0 transition group-hover:opacity-100">
                    <Button size="icon-xs" variant="ghost" aria-label="Correct">
                      <PencilSimpleIcon />
                    </Button>
                    <Button
                      size="icon-xs"
                      variant="ghost"
                      aria-label={gone ? "Remember again" : "Forget"}
                      onClick={() =>
                        setForgot((l) =>
                          gone ? l.filter((x) => x !== f) : [...l, f]
                        )
                      }
                    >
                      <TrashIcon />
                    </Button>
                  </span>
                </li>
              )
            })}
          </ul>
        </div>
      )}

      {day.stuck && (
        <div className="grid gap-2 rounded-xl border border-(--st-waiting)/50 bg-(--st-waiting)/8 p-3">
          <span className="flex items-center gap-1.5 font-mono text-[0.625rem] tracking-wider uppercase">
            <HandIcon className="size-3" />
            Stuck on
          </span>
          <p className="text-[0.8125rem]">{day.stuck}</p>
          {replied ? (
            <div className="flex justify-end">
              <span className="max-w-[80%] rounded-2xl rounded-br-md bg-primary px-3 py-1.5 text-[0.8125rem] text-primary-foreground">
                {replied}
              </span>
            </div>
          ) : (
            <div className="flex flex-wrap gap-1.5">
              <Button
                size="sm"
                onClick={() => setReplied("Yes, allow it for good.")}
              >
                Yes, for good
              </Button>
              <Button
                size="sm"
                variant="outline"
                onClick={() => setReplied("Keep asking each time.")}
              >
                Keep asking
              </Button>
              <form
                className="flex min-w-48 flex-1"
                onSubmit={(e) => {
                  e.preventDefault()
                  if (reply.trim()) setReplied(reply.trim())
                }}
              >
                <input
                  value={reply}
                  onChange={(e) => setReply(e.target.value)}
                  placeholder={`Reply as ${owner.name}…`}
                  className="h-7 flex-1 rounded-md border bg-background px-2 text-xs outline-none focus:border-primary/60"
                />
              </form>
            </div>
          )}
        </div>
      )}
    </article>
  )
}

/** The memory as topics around the teammate: bigger bubbles know more. Select one to see it. */
function MemoryMap() {
  const [selected, setSelected] = useState("repo")
  const W = 300
  const H = 260
  const cx = W / 2
  const cy = H / 2
  const placed = topics.map((t, i) => {
    const angle = (i / topics.length) * Math.PI * 2 - Math.PI / 2
    const r = 14 + Math.sqrt(t.facts.length) * 11
    return {
      ...t,
      r,
      x: cx + Math.cos(angle) * 88,
      y: cy + Math.sin(angle) * 82,
    }
  })
  const topic = topics.find((t) => t.id === selected)!
  const total = topics.reduce((n, t) => n + t.facts.length, 0)
  return (
    <aside className="flex w-[22rem] shrink-0 flex-col border-l max-xl:hidden">
      <div className="flex h-16 shrink-0 items-center gap-2 border-b px-4">
        <h2 className="text-sm font-semibold">What Finn knows</h2>
        <span className="font-mono text-xs text-muted-foreground">{total}</span>
        <Button size="xs" variant="outline" className="ml-auto">
          <SparkleIcon />
          Teach
        </Button>
      </div>
      <ScrollArea className="min-h-0 flex-1">
        <div className="grid gap-4 p-4 text-[0.8125rem]">
          <svg
            viewBox={`0 0 ${W} ${H}`}
            className="w-full"
            role="img"
            aria-label="Memory topics"
          >
            {placed.map((t) => (
              <line
                key={t.id}
                x1={cx}
                y1={cy}
                x2={t.x}
                y2={t.y}
                className="stroke-border"
                strokeWidth={1.5}
                strokeDasharray={t.id === selected ? undefined : "3 3"}
              />
            ))}
            <circle
              cx={cx}
              cy={cy}
              r={20}
              className="fill-muted stroke-border"
            />
            <text x={cx} y={cy + 6} textAnchor="middle" fontSize={18}>
              {finn.avatar}
            </text>
            {placed.map((t) => {
              const on = t.id === selected
              return (
                <g
                  key={t.id}
                  className="cursor-pointer"
                  onClick={() => setSelected(t.id)}
                >
                  <circle
                    cx={t.x}
                    cy={t.y}
                    r={t.r}
                    className={cn(
                      "transition-all",
                      on
                        ? "fill-(--acc-memory)/25 stroke-(--acc-memory)"
                        : "fill-(--acc-memory)/8 stroke-(--acc-memory)/40 hover:fill-(--acc-memory)/15"
                    )}
                    strokeWidth={on ? 2 : 1}
                  />
                  <text
                    x={t.x}
                    y={t.y - 2}
                    textAnchor="middle"
                    className="fill-foreground"
                    fontSize={10}
                    fontWeight={on ? 600 : 400}
                  >
                    {t.label}
                  </text>
                  <text
                    x={t.x}
                    y={t.y + 10}
                    textAnchor="middle"
                    className="fill-muted-foreground font-mono"
                    fontSize={9}
                  >
                    {t.facts.length}
                  </text>
                </g>
              )
            })}
          </svg>
          <div className="grid gap-2">
            <div className="flex items-baseline gap-2">
              <span className="font-semibold">{topic.label}</span>
              <span className="font-mono text-[0.6875rem] text-muted-foreground">
                {topic.facts.length} facts
              </span>
            </div>
            <ul className="divide-y rounded-lg border bg-card">
              {topic.facts.map((f) => (
                <li
                  key={f}
                  className="flex animate-[tm-in_.2s_ease-out] items-start gap-2 px-3 py-2 text-xs"
                >
                  <SparkleIcon
                    weight="fill"
                    className="mt-0.5 size-3 shrink-0 text-(--acc-memory)"
                  />
                  {f}
                </li>
              ))}
            </ul>
          </div>
        </div>
      </ScrollArea>
    </aside>
  )
}
