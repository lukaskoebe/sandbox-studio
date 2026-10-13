import { useEffect, useState } from "react"
import {
  BrainIcon,
  ChatCircleDotsIcon,
  CheckIcon,
  ClockIcon,
  GitBranchIcon,
  GlobeIcon,
  ListChecksIcon,
  SparkleIcon,
  TerminalWindowIcon,
  type Icon,
} from "@phosphor-icons/react"
import { StatusDot } from "@/components/status-dot"
import { Button } from "@/components/ui/button"
import { ScrollArea } from "@/components/ui/scroll-area"
import { tileClass, type Area } from "@/lib/accents"
import { cn } from "@/lib/utils"
import {
  activity,
  board as initialBoard,
  currentWork,
  finn,
  terminalTicker,
  type Card,
  type MockPresence,
} from "./data"
import { LiveAvatar, presenceWords } from "./frame"

const actions: {
  id: string
  label: string
  hint: string
  icon: Icon
  area: Area
}[] = [
  {
    id: "ask",
    label: "Ask",
    hint: "a question, answered here",
    icon: ChatCircleDotsIcon,
    area: "chat",
  },
  {
    id: "task",
    label: "Hand over",
    hint: "a task for a sandbox",
    icon: ListChecksIcon,
    area: "git",
  },
  {
    id: "teach",
    label: "Teach",
    hint: "something to remember",
    icon: BrainIcon,
    area: "memory",
  },
  {
    id: "pair",
    label: "Pair",
    hint: "in the workspace, live",
    icon: TerminalWindowIcon,
    area: "browser",
  },
]

const lanes: { id: Card["lane"]; label: string }[] = [
  { id: "asked", label: "You asked" },
  { id: "doing", label: "On it" },
  { id: "needs", label: "Needs you" },
  { id: "done", label: "Done this week" },
]

const cardIcons: Record<Card["kind"], [Icon, Area | undefined]> = {
  chat: [ChatCircleDotsIcon, "chat"],
  task: [ListChecksIcon, undefined],
  approval: [GlobeIcon, "network"],
  push: [GitBranchIcon, "git"],
  checkin: [ClockIcon, undefined],
}

/** Concept 2: visiting the teammate's desk. Who, what now, and everything between you two. */
export function DeskConcept({ presence }: { presence: MockPresence }) {
  const [cards, setCards] = useState(initialBoard)
  const [composer, setComposer] = useState<string | null>(null)
  const move = (id: string, lane: Card["lane"]) =>
    setCards((cs) => cs.map((c) => (c.id === id ? { ...c, lane } : c)))

  return (
    <ScrollArea className="min-h-0 flex-1">
      <section className="border-b bg-[linear-gradient(var(--wb-grid)_1px,transparent_1px),linear-gradient(90deg,var(--wb-grid)_1px,transparent_1px)] bg-size-[12px_12px]">
        <div className="mx-auto grid max-w-6xl gap-6 px-6 py-8 lg:grid-cols-[minmax(0,1fr)_auto] lg:items-center">
          <div className="flex items-center gap-5">
            <LiveAvatar presence={presence} size="lg" />
            <div className="grid min-w-0 gap-1">
              <h1 className="text-2xl font-semibold tracking-tight">
                {finn.name}
              </h1>
              <p className="text-[0.8125rem] text-muted-foreground">
                {finn.role} · runs {finn.harness} on{" "}
                <span className="font-mono">{finn.model}</span>
              </p>
              <p className="flex items-center gap-1.5 text-[0.8125rem]">
                <StatusDot
                  status={
                    presence === "working"
                      ? "running"
                      : presence === "waiting"
                        ? "waiting"
                        : "stopped"
                  }
                  label={presenceWords[presence]}
                />
                {presence === "working" && (
                  <span className="text-muted-foreground">
                    · {currentWork.title}, {currentWork.started}
                  </span>
                )}
              </p>
            </div>
          </div>
          <div className="grid grid-cols-2 gap-2 sm:grid-cols-4">
            {actions.map((a) => (
              <button
                key={a.id}
                type="button"
                onClick={() => setComposer(composer === a.id ? null : a.id)}
                className={cn(
                  "grid w-32 justify-items-start gap-2 rounded-xl border bg-card p-3 text-left transition hover:-translate-y-0.5 hover:shadow-md",
                  composer === a.id && "ring-2 ring-primary/40"
                )}
              >
                <span
                  className={cn(
                    "inline-flex size-7 items-center justify-center rounded-md",
                    tileClass(a.area)
                  )}
                >
                  <a.icon weight="bold" className="size-4" />
                </span>
                <span className="grid">
                  <span className="text-[0.8125rem] font-semibold">
                    {a.label}
                  </span>
                  <span className="text-[0.6875rem] leading-tight text-muted-foreground">
                    {a.hint}
                  </span>
                </span>
              </button>
            ))}
          </div>
          {composer && (
            <div className="flex animate-[tm-in_.2s_ease-out] gap-2 lg:col-span-2">
              <input
                autoFocus
                placeholder={
                  composer === "teach"
                    ? "Finn should remember that…"
                    : composer === "task"
                      ? "Describe the task; Finn picks the sandbox…"
                      : composer === "pair"
                        ? "What do you want to work on together?"
                        : "Ask Finn…"
                }
                className="h-9 flex-1 rounded-lg border bg-background px-3 text-[0.8125rem] outline-none focus:border-primary/60"
              />
              <Button onClick={() => setComposer(null)}>Send</Button>
            </div>
          )}
        </div>
      </section>

      <div className="mx-auto grid max-w-6xl gap-8 px-6 py-6 text-[0.8125rem]">
        <Live presence={presence} />

        <section className="grid gap-3">
          <h2 className="font-mono text-[0.625rem] tracking-wider text-muted-foreground uppercase">
            Between you two
          </h2>
          <div className="grid gap-3 md:grid-cols-2 xl:grid-cols-4">
            {lanes.map((lane) => {
              const list = cards.filter((c) => c.lane === lane.id)
              return (
                <div
                  key={lane.id}
                  className={cn(
                    "grid content-start gap-2 rounded-xl bg-muted/50 p-2",
                    lane.id === "needs" &&
                      list.length > 0 &&
                      "bg-(--st-waiting)/10"
                  )}
                >
                  <div className="flex items-center gap-2 px-1 text-xs font-medium">
                    {lane.label}
                    <span className="font-mono text-[0.6875rem] text-muted-foreground">
                      {list.length}
                    </span>
                  </div>
                  {list.map((c) => (
                    <BoardCard key={c.id} card={c} onMove={move} />
                  ))}
                  {list.length === 0 && (
                    <p className="px-1 py-3 text-center text-xs text-muted-foreground">
                      Nothing here
                    </p>
                  )}
                </div>
              )
            })}
          </div>
        </section>

        <Rhythm />
      </div>
    </ScrollArea>
  )
}

function BoardCard({
  card,
  onMove,
}: {
  card: Card
  onMove: (id: string, lane: Card["lane"]) => void
}) {
  const [I, area] = cardIcons[card.kind]
  return (
    <div
      className={cn(
        "grid animate-[tm-in_.2s_ease-out] gap-2 rounded-lg border bg-card p-2.5 shadow-xs",
        card.lane === "done" && "opacity-70"
      )}
    >
      <div className="flex items-start gap-2">
        <span
          className={cn(
            "mt-px inline-flex size-5 shrink-0 items-center justify-center rounded-[4px]",
            tileClass(area)
          )}
        >
          <I weight="bold" className="size-3" />
        </span>
        <div className="grid min-w-0">
          <span
            className={cn(
              "truncate font-medium",
              card.kind === "approval" && "font-mono text-xs"
            )}
          >
            {card.title}
          </span>
          <span className="truncate text-xs text-muted-foreground">
            {card.meta}
          </span>
        </div>
      </div>
      {card.lane === "needs" && (
        <div className="flex gap-1">
          <Button size="xs" onClick={() => onMove(card.id, "doing")}>
            {card.kind === "push" ? "Review" : "Allow once"}
          </Button>
          <Button
            size="xs"
            variant="ghost"
            onClick={() => onMove(card.id, "done")}
          >
            {card.kind === "push" ? "Reject" : "Deny"}
          </Button>
        </div>
      )}
      {card.lane === "asked" && (
        <Button
          size="xs"
          variant="outline"
          className="justify-self-start"
          onClick={() => onMove(card.id, "doing")}
        >
          Start now
        </Button>
      )}
    </div>
  )
}

/** What the teammate is doing right now: its terminal and its steps, live. */
function Live({ presence }: { presence: MockPresence }) {
  const [lines, setLines] = useState(3)
  useEffect(() => {
    if (presence !== "working") return
    const t = setInterval(
      () => setLines((n) => (n >= terminalTicker.length ? 3 : n + 1)),
      900
    )
    return () => clearInterval(t)
  }, [presence])

  if (presence === "waiting")
    return (
      <section className="flex flex-wrap items-center gap-4 rounded-xl border border-(--st-waiting)/50 bg-(--st-waiting)/10 p-4">
        <span className="text-2xl">✋</span>
        <div className="grid min-w-0 flex-1">
          <span className="font-semibold">Finn is waiting for you</span>
          <span className="text-muted-foreground">
            It wants to reach{" "}
            <span className="font-mono">registry.npmjs.org:443</span> to install
            @playwright/test, and stopped until you decide.
          </span>
        </div>
        <Button>Allow once</Button>
        <Button variant="outline">Always for Finn</Button>
      </section>
    )
  if (presence === "idle")
    return (
      <section className="flex flex-wrap items-center gap-4 rounded-xl border border-dashed p-4">
        <span className="text-2xl">☕</span>
        <div className="grid min-w-0 flex-1">
          <span className="font-semibold">Finn's desk is quiet</span>
          <span className="text-muted-foreground">
            Last active 12 minutes ago. Next check-in tomorrow at 09:00.
          </span>
        </div>
        <Button variant="outline">Suggest something</Button>
      </section>
    )
  const done = currentWork.steps.filter((s) => s.done).length
  return (
    <section className="grid gap-3 lg:grid-cols-[minmax(0,1fr)_20rem]">
      <div className="overflow-hidden rounded-xl border bg-stone-950 text-stone-200">
        <div className="flex items-center gap-2 border-b border-stone-800 px-3 py-1.5 font-mono text-[0.6875rem] text-stone-400">
          <span className="size-2 animate-pulse rounded-full bg-(--st-running)" />
          live · finn · main
          <button
            type="button"
            className="ml-auto text-stone-400 hover:text-white"
          >
            Open workspace →
          </button>
        </div>
        <pre className="h-48 overflow-hidden px-3 py-2 font-mono text-[0.6875rem] leading-relaxed">
          {terminalTicker.slice(0, lines).map((l, i) => (
            <div
              key={i}
              className={cn(
                "animate-[tm-in_.2s_ease-out]",
                l.startsWith("$") && "text-stone-50",
                l.includes("✘") && "text-red-400",
                l.includes("✓") && "text-emerald-400"
              )}
            >
              {l}
            </div>
          ))}
          <span className="inline-block h-3 w-1.5 animate-pulse bg-stone-300 align-middle" />
        </pre>
      </div>
      <div className="grid content-start gap-3 rounded-xl border bg-card p-4">
        <div className="flex items-baseline justify-between gap-2">
          <span className="font-semibold">{currentWork.title}</span>
          <span className="font-mono text-[0.6875rem] text-muted-foreground">
            {done}/{currentWork.steps.length}
          </span>
        </div>
        <ol className="relative grid gap-2.5 border-l pl-4 text-xs">
          {currentWork.steps.map((s, i) => {
            const now = !s.done && currentWork.steps[i - 1]?.done !== false
            return (
              <li key={s.label} className="relative">
                <span
                  className={cn(
                    "absolute top-0.5 -left-[1.3125rem] inline-flex size-3 items-center justify-center rounded-full border bg-background",
                    s.done && "border-(--st-running) bg-(--st-running)",
                    now && "border-(--st-running)"
                  )}
                >
                  {s.done && (
                    <CheckIcon weight="bold" className="size-2 text-white" />
                  )}
                  {now && (
                    <span className="size-1.5 animate-pulse rounded-full bg-(--st-running)" />
                  )}
                </span>
                <span
                  className={cn(
                    s.done && "text-muted-foreground",
                    now && "font-medium"
                  )}
                >
                  {s.label}
                </span>
              </li>
            )
          })}
        </ol>
      </div>
    </section>
  )
}

/** Two weeks of working together: turns per day as bars, things learned as sparks above. */
function Rhythm() {
  const max = Math.max(...activity.map((d) => d.turns))
  const learned = activity.reduce((n, d) => n + d.learned, 0)
  const turns = activity.reduce((n, d) => n + d.turns, 0)
  return (
    <section className="grid gap-3">
      <div className="flex items-baseline gap-4">
        <h2 className="font-mono text-[0.625rem] tracking-wider text-muted-foreground uppercase">
          Two weeks together
        </h2>
        <span className="text-xs text-muted-foreground">
          <span className="font-mono text-foreground">{turns}</span> turns ·{" "}
          <span className="font-mono text-foreground">{learned}</span> things
          learned
        </span>
      </div>
      <div className="flex h-24 items-end gap-1.5 rounded-xl border bg-card p-3">
        {activity.map((d, i) => (
          <div
            key={i}
            className="group relative flex flex-1 flex-col items-center justify-end gap-1"
            title={`${d.turns} turns, ${d.learned} learned`}
          >
            {d.learned > 0 && (
              <span className="flex gap-0.5">
                {Array.from({ length: d.learned }, (_, j) => (
                  <SparkleIcon
                    key={j}
                    weight="fill"
                    className="size-2 text-(--acc-memory)"
                  />
                ))}
              </span>
            )}
            <span
              className="w-full rounded-sm bg-primary/70 transition-colors group-hover:bg-primary"
              style={{ height: `${Math.max(2, (d.turns / max) * 52)}px` }}
            />
          </div>
        ))}
      </div>
    </section>
  )
}
