import { useEffect, useRef, useState, type ReactNode } from "react"
import {
  ArrowUpIcon,
  BrainIcon,
  BrowserIcon,
  CaretDownIcon,
  ChatCircleDotsIcon,
  CheckIcon,
  ClockIcon,
  CubeIcon,
  GitBranchIcon,
  GlobeIcon,
  ListChecksIcon,
  PaperclipIcon,
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
  currentWork,
  finn,
  thread as initialThread,
  topics,
  type MockPresence,
  type ThreadItem,
} from "./data"
import { LiveAvatar, Typing, presenceWords } from "./frame"

const areaIcons: Record<Area, Icon> = {
  browser: BrowserIcon,
  git: GitBranchIcon,
  network: GlobeIcon,
  chat: ChatCircleDotsIcon,
  memory: BrainIcon,
}

type Mode = "message" | "task" | "teach"

const modes: { id: Mode; label: string; icon: Icon; hint: string }[] = [
  {
    id: "message",
    label: "Message",
    icon: ChatCircleDotsIcon,
    hint: "Ask Finn anything…",
  },
  {
    id: "task",
    label: "Task",
    icon: ListChecksIcon,
    hint: "Hand Finn a task; it works on it in a sandbox and reports back…",
  },
  {
    id: "teach",
    label: "Teach",
    icon: BrainIcon,
    hint: "Tell Finn something to remember for good…",
  },
]

/** Concept 1: the teammate page as one conversation, with a profile rail. */
export function ConversationConcept({ presence }: { presence: MockPresence }) {
  const [items, setItems] = useState<ThreadItem[]>(initialThread)
  const [typing, setTyping] = useState(false)
  const end = useRef<HTMLDivElement>(null)
  useEffect(() => {
    end.current?.scrollIntoView({ behavior: "smooth", block: "end" })
  }, [items.length, typing])

  const update = (i: number, patch: Partial<ThreadItem>) =>
    setItems((list) =>
      list.map((it, j) => (j === i ? ({ ...it, ...patch } as ThreadItem) : it))
    )

  const send = (mode: Mode, text: string) => {
    const now = new Date().toTimeString().slice(0, 5)
    setItems((l) => [...l, { kind: "you", text, time: now }])
    setTyping(true)
    setTimeout(() => {
      setTyping(false)
      setItems((l) => [
        ...l,
        ...(mode === "teach"
          ? [
              {
                kind: "learned",
                fact: text,
                time: now,
                state: "kept",
              } as const,
              {
                kind: "them",
                text: "Noted, that's in my memory now. I'll bring it up when it matters.",
                time: now,
              } as const,
            ]
          : mode === "task"
            ? [
                {
                  kind: "activity",
                  area: "chat",
                  text: "Took the task and started in finn",
                  detail: text.slice(0, 60),
                  time: now,
                } as const,
                {
                  kind: "them",
                  text: "On it. I'll post here when I'm done or stuck.",
                  time: now,
                } as const,
              ]
            : [
                {
                  kind: "them",
                  text: "Good question. Give me a sec, I'll look into it in finn.",
                  time: now,
                } as const,
              ]),
      ])
    }, 1400)
  }

  return (
    <>
      <Header presence={presence} />
      <div className="flex min-h-0 flex-1">
        <div className="flex min-w-0 flex-1 flex-col">
          <ScrollArea className="min-h-0 flex-1">
            <div className="mx-auto grid max-w-3xl gap-3 px-4 py-6 text-[0.8125rem]">
              {items.map((it, i) => (
                <Item
                  key={i}
                  item={it}
                  onChange={(patch) => update(i, patch)}
                />
              ))}
              {typing && (
                <div className="flex items-center gap-2 pl-10 text-xs text-muted-foreground">
                  <Typing /> Finn is typing
                </div>
              )}
              {presence === "working" && !typing && <WorkingLine />}
              <div ref={end} />
            </div>
          </ScrollArea>
          <Composer onSend={send} />
        </div>
        <Rail presence={presence} />
      </div>
    </>
  )
}

function Header({ presence }: { presence: MockPresence }) {
  return (
    <header className="flex h-16 shrink-0 items-center gap-3 border-b px-4">
      <LiveAvatar presence={presence} size="sm" />
      <div className="grid min-w-0 flex-1">
        <div className="flex items-baseline gap-2">
          <h1 className="text-sm font-semibold">{finn.name}</h1>
          <span className="truncate text-xs text-muted-foreground">
            {finn.role}
          </span>
        </div>
        <p className="truncate text-xs text-muted-foreground">
          {presence === "working" ? (
            <>
              <span className="text-foreground">Working on</span>{" "}
              {currentWork.title} · in{" "}
              <span className="font-mono">{currentWork.sandbox}</span>
            </>
          ) : presence === "waiting" ? (
            <span className="text-foreground">
              Waiting for you: one host to allow
            </span>
          ) : (
            "Free · last active 12 min ago"
          )}
        </p>
      </div>
      <Button variant="outline" size="sm">
        <TerminalWindowIcon />
        Pair in workspace
      </Button>
      <Button variant="ghost" size="icon-sm" aria-label="More">
        <CaretDownIcon />
      </Button>
    </header>
  )
}

function Item({
  item,
  onChange,
}: {
  item: ThreadItem
  onChange: (patch: Partial<ThreadItem>) => void
}) {
  const enter = "animate-[tm-in_.25s_ease-out]"
  switch (item.kind) {
    case "day":
      return (
        <div className="flex items-center gap-3 py-2 font-mono text-[0.625rem] tracking-wider text-muted-foreground uppercase">
          <span className="h-px flex-1 bg-border" />
          {item.label}
          <span className="h-px flex-1 bg-border" />
        </div>
      )
    case "you":
      return (
        <div className={cn("flex justify-end", enter)}>
          <div className="max-w-[75%] rounded-2xl rounded-br-md bg-primary px-3.5 py-2 text-primary-foreground">
            {item.text}
            <span className="ml-2 align-bottom font-mono text-[0.625rem] opacity-60">
              {item.time}
            </span>
          </div>
        </div>
      )
    case "them":
      return (
        <div className={cn("flex items-end gap-2", enter)}>
          <span className="inline-flex size-8 shrink-0 items-center justify-center rounded-full bg-muted text-base">
            {finn.avatar}
          </span>
          <div className="max-w-[75%] rounded-2xl rounded-bl-md bg-muted px-3.5 py-2">
            {item.text}
            <span className="ml-2 align-bottom font-mono text-[0.625rem] text-muted-foreground">
              {item.time}
            </span>
          </div>
        </div>
      )
    case "where":
      return (
        <div
          className={cn(
            "flex items-center justify-center gap-2 py-1 text-xs",
            enter
          )}
        >
          <span className="inline-flex items-center gap-1.5 rounded-full border bg-card px-2.5 py-0.5">
            <CubeIcon className="size-3.5 text-muted-foreground" />
            Finn works in{" "}
            <span className="font-mono font-medium">{item.sandbox}</span>
          </span>
          <span className="text-muted-foreground">{item.note}</span>
        </div>
      )
    case "activity": {
      const I = areaIcons[item.area]
      return (
        <div className={cn("flex items-center gap-2 pl-10 text-xs", enter)}>
          <span
            className={cn(
              "inline-flex size-5 items-center justify-center rounded-[4px]",
              tileClass(item.area)
            )}
          >
            <I weight="bold" className="size-3" />
          </span>
          <span>{item.text}</span>
          {item.detail && (
            <span className="truncate text-muted-foreground">
              · {item.detail}
            </span>
          )}
          {item.sandbox && (
            <span className="shrink-0 rounded border px-1 font-mono text-[0.625rem] text-muted-foreground">
              {item.sandbox}
            </span>
          )}
          <span className="ml-auto font-mono text-[0.625rem] text-muted-foreground">
            {item.time}
          </span>
        </div>
      )
    }
    case "learned":
      return (
        <div
          className={cn(
            "ml-10 flex items-start gap-2.5 rounded-lg border border-dashed border-(--acc-memory)/50 bg-(--acc-memory)/5 px-3 py-2",
            item.state === "forgot" && "opacity-50",
            enter
          )}
        >
          <SparkleIcon
            weight="fill"
            className="mt-0.5 size-3.5 shrink-0 text-(--acc-memory)"
          />
          <div className="grid min-w-0 flex-1 gap-0.5">
            <span className="font-mono text-[0.625rem] tracking-wider text-(--acc-memory) uppercase">
              {item.state === "forgot" ? "Forgotten" : "Learned"}
            </span>
            <span className={cn(item.state === "forgot" && "line-through")}>
              {item.fact}
            </span>
          </div>
          {item.state === undefined ? (
            <div className="flex shrink-0 gap-1">
              <Button
                size="xs"
                variant="outline"
                onClick={() => onChange({ state: "kept" })}
              >
                <CheckIcon />
                Keep
              </Button>
              <Button
                size="xs"
                variant="ghost"
                onClick={() => onChange({ state: "forgot" })}
              >
                Forget
              </Button>
            </div>
          ) : item.state === "kept" ? (
            <Button
              size="xs"
              variant="ghost"
              className="shrink-0 text-muted-foreground"
              onClick={() => onChange({ state: "forgot" })}
            >
              Forget
            </Button>
          ) : (
            <Button
              size="xs"
              variant="ghost"
              className="shrink-0"
              onClick={() => onChange({ state: "kept" })}
            >
              Undo
            </Button>
          )}
        </div>
      )
    case "ask":
      return (
        <div
          className={cn(
            "ml-10 flex flex-wrap items-center gap-2 rounded-lg border bg-card px-3 py-2",
            item.state === undefined &&
              "border-(--st-waiting)/60 shadow-(--st-waiting)/10 shadow-[0_0_0_3px]",
            enter
          )}
        >
          <StatusDot
            status={item.state === undefined ? "waiting" : "running"}
            compact
          />
          <div className="grid min-w-0 flex-1">
            <span className="font-mono text-xs">{item.text}</span>
            <span className="text-xs text-muted-foreground">{item.detail}</span>
          </div>
          {item.state === undefined ? (
            <>
              <Button size="sm" onClick={() => onChange({ state: "allowed" })}>
                Allow once
              </Button>
              <Button
                size="sm"
                variant="outline"
                onClick={() => onChange({ state: "allowed" })}
              >
                Always
              </Button>
              <Button
                size="sm"
                variant="ghost"
                onClick={() => onChange({ state: "denied" })}
              >
                Deny
              </Button>
            </>
          ) : (
            <span className="text-xs text-muted-foreground">
              {item.state === "allowed" ? "You allowed it" : "You denied it"}
            </span>
          )}
        </div>
      )
    case "checkin":
      return (
        <div
          className={cn(
            "ml-10 grid gap-1 rounded-lg border bg-card px-3 py-2",
            enter
          )}
        >
          <span className="flex items-center gap-1.5 font-mono text-[0.625rem] tracking-wider text-muted-foreground uppercase">
            <ClockIcon className="size-3" />
            {item.title} · {item.time}
          </span>
          <span>{item.text}</span>
          <div className="flex gap-1">
            <Button size="xs" variant="outline">
              Open a branch
            </Button>
            <Button size="xs" variant="ghost">
              Later
            </Button>
          </div>
        </div>
      )
  }
}

/** While it works, the thread ends in a live line: the step it is on, with a scanning bar. */
function WorkingLine() {
  const step = currentWork.steps.find((s) => !s.done)
  return (
    <div className="ml-10 grid gap-1.5 text-xs">
      <div className="flex items-center gap-2 text-muted-foreground">
        <Typing />
        <span>{step?.label}…</span>
      </div>
      <div className="relative h-0.5 w-48 overflow-hidden rounded-full bg-muted">
        <span className="absolute inset-y-0 w-1/3 animate-[tm-scan_1.6s_linear_infinite] rounded-full bg-(--st-running)" />
      </div>
    </div>
  )
}

function Composer({ onSend }: { onSend: (mode: Mode, text: string) => void }) {
  const [mode, setMode] = useState<Mode>("message")
  const [text, setText] = useState("")
  const current = modes.find((m) => m.id === mode)!
  const submit = () => {
    if (!text.trim()) return
    onSend(mode, text.trim())
    setText("")
  }
  return (
    <div className="shrink-0 px-4 pb-4">
      <div
        className={cn(
          "mx-auto max-w-3xl rounded-xl border bg-card shadow-xs transition-colors focus-within:border-primary/60",
          mode === "teach" && "border-(--acc-memory)/60",
          mode === "task" && "border-(--acc-chat)/60"
        )}
      >
        <div className="flex items-center gap-1 border-b px-2 py-1.5">
          {modes.map((m) => (
            <button
              key={m.id}
              type="button"
              onClick={() => setMode(m.id)}
              className={cn(
                "inline-flex h-6 items-center gap-1.5 rounded-md px-2 text-xs",
                mode === m.id
                  ? "bg-muted font-medium"
                  : "text-muted-foreground hover:text-foreground"
              )}
            >
              <m.icon className="size-3.5" />
              {m.label}
            </button>
          ))}
          <span className="ml-auto inline-flex h-6 items-center gap-1 rounded-md px-2 text-xs text-muted-foreground">
            <CubeIcon className="size-3.5" />
            in <span className="font-mono text-foreground">finn</span>
            <CaretDownIcon className="size-3" />
          </span>
        </div>
        <textarea
          value={text}
          onChange={(e) => setText(e.target.value)}
          onKeyDown={(e) => {
            if (e.key === "Enter" && !e.shiftKey) {
              e.preventDefault()
              submit()
            }
          }}
          rows={2}
          placeholder={current.hint}
          className="block w-full resize-none bg-transparent px-3 py-2 text-[0.8125rem] outline-none placeholder:text-muted-foreground"
        />
        <div className="flex items-center gap-2 px-2 pb-2">
          <Button variant="ghost" size="icon-sm" aria-label="Attach">
            <PaperclipIcon />
          </Button>
          {mode === "teach" && (
            <span className="text-[0.6875rem] text-muted-foreground">
              Saved to Finn's memory, not run as a task.
            </span>
          )}
          <Button
            size="icon-sm"
            className="ml-auto"
            aria-label="Send"
            disabled={!text.trim()}
            onClick={submit}
          >
            <ArrowUpIcon />
          </Button>
        </div>
      </div>
    </div>
  )
}

function Rail({ presence }: { presence: MockPresence }) {
  const [soul, setSoul] = useState(false)
  const facts = topics.flatMap((t) => t.facts)
  const done = currentWork.steps.filter((s) => s.done).length
  return (
    <aside className="w-80 shrink-0 border-l max-xl:hidden">
      <ScrollArea className="h-full">
        <div className="grid gap-5 p-4 text-[0.8125rem]">
          <div className="grid justify-items-center gap-2 pt-2 text-center">
            <LiveAvatar presence={presence} size="lg" />
            <div>
              <div className="text-base font-semibold">{finn.name}</div>
              <div className="text-xs text-muted-foreground">{finn.role}</div>
            </div>
            <span className="inline-flex items-center gap-1.5 rounded-full border px-2 py-0.5 text-xs">
              <StatusDot
                status={
                  presence === "working"
                    ? "running"
                    : presence === "waiting"
                      ? "waiting"
                      : "stopped"
                }
                compact
              />
              {presenceWords[presence]}
            </span>
          </div>

          <RailSection title="Now">
            {presence === "working" ? (
              <div className="grid gap-2">
                <div className="font-medium">{currentWork.title}</div>
                <div className="h-1 overflow-hidden rounded-full bg-muted">
                  <div
                    className="h-full rounded-full bg-(--st-running) transition-all"
                    style={{
                      width: `${(done / currentWork.steps.length) * 100}%`,
                    }}
                  />
                </div>
                <ol className="grid gap-1 text-xs">
                  {currentWork.steps.map((s) => (
                    <li
                      key={s.label}
                      className={cn(
                        "flex items-center gap-1.5",
                        s.done ? "text-muted-foreground" : "font-medium"
                      )}
                    >
                      {s.done ? (
                        <CheckIcon className="size-3 text-(--st-running)" />
                      ) : (
                        <span className="size-3 rounded-full border" />
                      )}
                      {s.label}
                    </li>
                  ))}
                </ol>
              </div>
            ) : presence === "waiting" ? (
              <p className="text-xs">
                Stopped at <span className="font-mono">registry.npmjs.org</span>
                . Allow it in the thread and Finn carries on.
              </p>
            ) : (
              <div className="grid gap-1.5 text-xs text-muted-foreground">
                <p>Nothing on its plate. Ideas:</p>
                {["Bump vite and xterm", "Review last week's TODOs"].map(
                  (s) => (
                    <button
                      key={s}
                      type="button"
                      className="rounded-md border border-dashed px-2 py-1 text-left text-foreground hover:bg-muted"
                    >
                      {s}
                    </button>
                  )
                )}
              </div>
            )}
          </RailSection>

          <RailSection title="Knows" count={facts.length} action="Notebook">
            <ul className="grid gap-1.5 text-xs">
              {facts.slice(0, 4).map((f) => (
                <li key={f} className="flex gap-1.5">
                  <SparkleIcon
                    weight="fill"
                    className="mt-0.5 size-3 shrink-0 text-(--acc-memory)"
                  />
                  {f}
                </li>
              ))}
            </ul>
          </RailSection>

          <RailSection title="Routines">
            <div className="flex items-center gap-2 text-xs">
              <ClockIcon className="size-3.5 text-muted-foreground" />
              <span className="flex-1">Dependency check</span>
              <span className="font-mono text-[0.6875rem] text-muted-foreground">
                daily 09:00
              </span>
            </div>
          </RailSection>

          <RailSection title="Sandboxes">
            <div className="flex flex-wrap gap-1.5">
              {finn.sandboxes.map((s) => (
                <span
                  key={s.name}
                  className="inline-flex items-center gap-1.5 rounded-md border px-2 py-0.5 font-mono text-xs"
                >
                  <StatusDot status={s.status} compact />
                  {s.name}
                </span>
              ))}
            </div>
          </RailSection>

          <RailSection title="Profile" action="Edit">
            <dl className="grid grid-cols-[5rem_1fr] gap-y-1 text-xs">
              <dt className="text-muted-foreground">Runs in</dt>
              <dd>{finn.harness}</dd>
              <dt className="text-muted-foreground">Model</dt>
              <dd className="font-mono">{finn.model}</dd>
            </dl>
            <button
              type="button"
              onClick={() => setSoul(!soul)}
              className="mt-1 flex items-center gap-1 text-xs text-muted-foreground hover:text-foreground"
            >
              <CaretDownIcon
                className={cn(
                  "size-3 transition-transform",
                  soul && "rotate-180"
                )}
              />
              Personality
            </button>
            {soul && (
              <p className="text-xs/relaxed text-muted-foreground italic">
                “{finn.soul}”
              </p>
            )}
          </RailSection>
        </div>
      </ScrollArea>
    </aside>
  )
}

function RailSection({
  title,
  count,
  action,
  children,
}: {
  title: string
  count?: number
  action?: string
  children: ReactNode
}) {
  return (
    <section className="grid gap-2">
      <div className="flex items-center gap-2">
        <h2 className="font-mono text-[0.625rem] tracking-wider text-muted-foreground uppercase">
          {title}
        </h2>
        {count !== undefined && (
          <span className="font-mono text-[0.625rem] text-muted-foreground">
            {count}
          </span>
        )}
        {action && (
          <button
            type="button"
            className="ml-auto text-[0.6875rem] text-muted-foreground hover:text-foreground"
          >
            {action}
          </button>
        )}
      </div>
      {children}
    </section>
  )
}
