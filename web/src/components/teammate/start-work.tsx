import { useState } from "react"
import { useQueryClient } from "@tanstack/react-query"
import { useNavigate } from "@tanstack/react-router"
import {
  ChatCircleDotsIcon,
  MonitorIcon,
  PlusIcon,
  RobotIcon,
  type Icon,
} from "@phosphor-icons/react"
import { BaseMark } from "@/components/base-dialog"
import { gitChangeCount, useGitStatus } from "@/components/computer/git-changes"
import { CreateSandboxDialog } from "@/components/create-sandbox-dialog"
import { forkName } from "@/components/fork-sandbox-dialog"
import { wakeAction } from "@/components/sandbox-lifecycle"
import { phase, phaseDot } from "@/components/status-badge"
import { StatusDot } from "@/components/status-dot"
import { TeammateAvatar } from "@/components/teammate-avatar"
import { Button } from "@/components/ui/button"
import { Checkbox } from "@/components/ui/checkbox"
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog"
import { Field, FieldLabel } from "@/components/ui/field"
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select"
import { Spinner } from "@/components/ui/spinner"
import { Textarea } from "@/components/ui/textarea"
import {
  $api,
  errorMessage,
  fetchClient,
  isReady,
  type Sandbox,
  type Teammate,
} from "@/lib/api/client"
import type { components } from "@/lib/api/schema.gen"
import { lastComputer, rememberComputer, screenComputer } from "@/lib/computer"
import {
  openStartWork,
  useStartWork,
  type StartWorkMode,
  type StartWorkRequest,
} from "@/lib/start-work"
import { harnessLabels } from "@/lib/teammates"
import { terminalText } from "@/components/terminal"
import { terminalURL } from "@/lib/terminals"
import { cn } from "@/lib/utils"

type GitRepo = components["schemas"]["GitRepo"]

/** How many of a terminal's last lines Bring in sends. */
const broughtLines = 120

/**
 * Start work with a teammate (the sidebar's +, its home and its desk): a chat that starts with
 * your message, its agent in a terminal you can type in too, or only its computer, with no
 * conversation and no thread. On one of its computers or a fresh fork of it. Opened from a
 * terminal (Bring in), the terminal's last lines and the uncommitted changes can come along.
 */
export function StartWorkDialog({ env }: { env: string }) {
  const req = useStartWork()
  const teammates = $api.useQuery("get", "/api/environments/{env}/teammates", {
    params: { path: { env } },
  })
  const teammate = teammates.data?.find((t) => t.id === req?.teammate)
  return (
    <Dialog
      open={!!req && !!teammate}
      onOpenChange={(open) => !open && openStartWork(null)}
    >
      <DialogContent className="sm:max-w-lg">
        {req && teammate && (
          <StartWork
            key={`${req.teammate}-${req.computer}-${req.mode}-${req.bring?.term}`}
            env={env}
            teammate={teammate}
            req={req}
          />
        )}
      </DialogContent>
    </Dialog>
  )
}

const modes: {
  id: StartWorkMode
  icon: Icon
  label: (t: Teammate) => string
  hint: (t: Teammate) => string
}[] = [
  {
    id: "chat",
    icon: ChatCircleDotsIcon,
    label: () => "Ask in chat",
    hint: () => "A new thread that starts with your message",
  },
  {
    id: "terminal",
    icon: RobotIcon,
    label: (t) => `${harnessLabels[t.harness]} in a terminal`,
    hint: (t) => `${t.name} works where you can type along; a thread too`,
  },
  {
    id: "computer",
    icon: MonitorIcon,
    label: () => "Just the computer",
    hint: () => "Its terminal, browser and files; no conversation",
  },
]

function StartWork({
  env,
  teammate,
  req,
}: {
  env: string
  teammate: Teammate
  req: StartWorkRequest
}) {
  const navigate = useNavigate()
  const queryClient = useQueryClient()
  const sandboxes = $api.useQuery(
    "get",
    "/api/environments/{env}/sandboxes",
    { params: { path: { env } } },
    { refetchInterval: 5000 }
  )
  const chats = $api.useQuery("get", "/api/environments/{env}/chats", {
    params: { path: { env } },
  })
  const owned = (sandboxes.data ?? []).filter(
    (s) => s.teammateId === teammate.id
  )
  const suggested = screenComputer(
    owned,
    (chats.data ?? []).filter((c) => c.teammateId === teammate.id),
    req.computer,
    undefined,
    lastComputer(env, teammate.id)
  )
  const [mode, setMode] = useState<StartWorkMode>(req.mode ?? "chat")
  const [picked, setPicked] = useState<string>()
  const computer = owned.find((s) => s.id === picked) ?? suggested
  const [fork, setFork] = useState(false)
  const [message, setMessage] = useState("")
  const [step, setStep] = useState<string>()
  const [problem, setProblem] = useState<string>()
  const [giving, setGiving] = useState(false)

  // Bring in: the terminal the user worked in and what changed, on the computer it is on.
  const bringing =
    req.bring && computer?.id === req.computer ? req.bring : undefined
  const git = useGitStatus(env, computer?.id ?? "", {
    enabled: !!bringing && !!computer && isReady(computer),
    shown: false,
  })
  const changed = gitChangeCount(git.data?.repos)
  const [lines] = useState(() =>
    req.bring && req.computer
      ? terminalText(
          terminalURL(env, req.computer, req.bring.term),
          broughtLines
        )
      : undefined
  )
  const [withTerminal, setWithTerminal] = useState(true)
  const [withChanges, setWithChanges] = useState(true)

  const busy = step !== undefined
  const close = () => openStartWork(null)

  if (sandboxes.data && owned.length === 0)
    return (
      <>
        <Header teammate={teammate} />
        <p className="text-sm text-muted-foreground">
          {teammate.name} has no computer yet. Give it one to work on: its
          terminal, browser and files.
        </p>
        <DialogFooter>
          <Button variant="outline" onClick={close}>
            Cancel
          </Button>
          <Button onClick={() => setGiving(true)}>
            <PlusIcon />
            Give {teammate.name} a computer
          </Button>
        </DialogFooter>
        <CreateSandboxDialog
          env={env}
          teammate={teammate.id}
          open={giving}
          onOpenChange={(open) => {
            setGiving(open)
            if (!open) close()
          }}
        />
      </>
    )

  const refresh = () => {
    for (const path of [
      "/api/environments/{env}/sandboxes",
      "/api/environments/{env}/chats",
      "/api/environments/{env}/teammates/{id}/timeline",
      "/api/environments/{env}/sandboxes/{id}/terminals",
    ])
      void queryClient.invalidateQueries({ queryKey: ["get", path] })
  }

  /** Waits until the computer runs, starting it if it is down; gives up after five minutes. */
  const ready = async (sb: Sandbox) => {
    const wake = wakeAction(sb)
    if (wake) {
      setStep(`Starting ${sb.name}…`)
      const { error } = await fetchClient.POST(
        wake === "resume"
          ? "/api/environments/{env}/sandboxes/{id}/resume"
          : "/api/environments/{env}/sandboxes/{id}/start",
        { params: { path: { env, id: sb.id } } }
      )
      if (error) throw error
    }
    const until = Date.now() + 5 * 60_000
    for (let s = sb; !isReady(s);) {
      if (s.status === "failed")
        throw new Error(`${s.name} failed to start; its page says why.`)
      if (Date.now() > until)
        throw new Error(`${s.name} is still starting; try again in a moment.`)
      await new Promise((r) => setTimeout(r, 1000))
      const { data, error } = await fetchClient.GET(
        "/api/environments/{env}/sandboxes/{id}",
        { params: { path: { env, id: sb.id } } }
      )
      if (error) throw error
      s = data
    }
  }

  const prompt = () => {
    const parts = [
      message.trim() ||
        (bringing ? "Can you take a look at what I am doing here?" : ""),
    ]
    if (bringing && withTerminal && lines) {
      const fence = lines.includes("```") ? "~~~~" : "```"
      parts.push(
        `My terminal \`${bringing.term}\` on ${computer?.name}, its last lines:\n\n${fence}text\n${lines}\n${fence}`
      )
    }
    if (bringing && withChanges && changed > 0)
      parts.push(changesNote(git.data?.repos ?? []))
    return parts.filter(Boolean).join("\n\n")
  }

  const start = async () => {
    if (!computer) return
    setProblem(undefined)
    try {
      let target = computer
      if (fork) {
        setStep(`Making a fork of ${computer.name}…`)
        const { data, error } = await fetchClient.POST(
          "/api/environments/{env}/sandboxes/{id}/fork",
          {
            params: { path: { env, id: computer.id } },
            body: {
              name: forkName(computer.name),
              ...(computer.forkDefaults ?? {
                docker: false,
                tools: true,
                script: false,
              }),
              teammateId: teammate.id,
            },
          }
        )
        if (error) throw error
        target = data
        refresh()
      }
      const desk = (term?: string) => {
        rememberComputer(env, teammate.id, target.id)
        close()
        void navigate({
          to: "/e/$env/teammates/$id",
          params: { env, id: teammate.id },
          search: { computer: target.id, tab: "terminal", term },
        })
      }
      // The computer itself shows its own start, or the fork's progress.
      if (mode === "computer") return desk()
      if (fork) setStep(`Waiting for ${target.name} to start…`)
      await ready(target)
      if (mode === "terminal") {
        setStep(`Starting ${harnessLabels[teammate.harness]}…`)
        const { data, error } = await fetchClient.POST(
          "/api/environments/{env}/sandboxes/{id}/sessions",
          { params: { path: { env, id: target.id } } }
        )
        if (error) throw error
        refresh()
        return desk(data.name)
      }
      setStep(`Starting the thread…`)
      const { data, error } = await fetchClient.POST(
        "/api/environments/{env}/sandboxes/{id}/chats",
        {
          params: { path: { env, id: target.id } },
          body: { text: prompt() },
        }
      )
      if (error) throw error
      refresh()
      close()
      void navigate({
        to: "/e/$env/teammates/$id",
        params: { env, id: teammate.id },
        search: { thread: data.id },
      })
    } catch (err) {
      setProblem(errorMessage(err))
      setStep(undefined)
    }
  }

  const action =
    mode === "chat"
      ? "Start the thread"
      : mode === "terminal"
        ? `Open ${harnessLabels[teammate.harness]}`
        : "Open the computer"

  return (
    <form
      className="contents"
      onSubmit={(e) => {
        e.preventDefault()
        if (!busy) void start()
      }}
    >
      <Header teammate={teammate} bring={!!req.bring} />
      <div role="radiogroup" aria-label="How to start" className="grid gap-1.5">
        {modes.map((m) => (
          <button
            key={m.id}
            type="button"
            role="radio"
            aria-checked={mode === m.id}
            disabled={busy}
            onClick={() => setMode(m.id)}
            className={cn(
              "flex min-w-0 items-start gap-3 rounded-lg border bg-card px-3 py-2.5 text-left outline-none hover:bg-muted/50 focus-visible:ring-2 focus-visible:ring-ring/30",
              mode === m.id && "border-primary bg-primary/5 hover:bg-primary/5"
            )}
          >
            <m.icon
              className={cn(
                "mt-0.5 size-4 shrink-0 text-muted-foreground",
                mode === m.id && "text-primary"
              )}
            />
            <span className="grid min-w-0 gap-0.5">
              <span className="text-sm font-medium">{m.label(teammate)}</span>
              <span className="text-xs text-muted-foreground">
                {m.hint(teammate)}
              </span>
            </span>
          </button>
        ))}
      </div>
      <div className="grid gap-2">
        <div className="flex flex-wrap items-center gap-2 text-sm">
          <span className="text-muted-foreground">On</span>
          <Select
            value={computer?.id ?? ""}
            onValueChange={(v) => v && setPicked(v)}
            disabled={busy}
          >
            <SelectTrigger
              size="sm"
              aria-label="The computer"
              className="font-mono"
            >
              <SelectValue>
                {computer && (
                  <span className="flex items-center gap-2">
                    <StatusDot {...phaseDot(phase(computer))} compact />
                    {computer.name}
                  </span>
                )}
              </SelectValue>
            </SelectTrigger>
            <SelectContent>
              {owned.map((s) => (
                <SelectItem key={s.id} value={s.id} className="font-mono">
                  <StatusDot {...phaseDot(phase(s))} compact />
                  {s.name}
                  {s.forkDefaults && <BaseMark />}
                </SelectItem>
              ))}
            </SelectContent>
          </Select>
          {computer && !isReady(computer) && !fork && mode !== "computer" && (
            <span className="text-xs text-muted-foreground">
              {phaseDot(phase(computer)).label}; it starts first
            </span>
          )}
        </div>
        <label className="flex items-start gap-2 text-sm">
          <Checkbox
            checked={fork}
            onCheckedChange={(v) => setFork(v === true)}
            disabled={busy || !computer}
            className="mt-0.5"
          />
          <span className="grid gap-0.5">
            In a fresh fork
            <span className="text-xs text-muted-foreground">
              A copy of {computer?.name ?? "the computer"}
              {computer?.forkDefaults ? ", with the base's settings" : ""}, so{" "}
              {computer?.name ?? "it"} stays as it is
            </span>
          </span>
        </label>
      </div>
      {mode === "chat" && (
        <Field>
          <FieldLabel htmlFor="start-work-message">
            What should {teammate.name} do?
          </FieldLabel>
          <Textarea
            id="start-work-message"
            autoFocus
            rows={3}
            value={message}
            disabled={busy}
            placeholder={
              req.bring
                ? "The test fails after my change. Can you take over?"
                : `Ask ${teammate.name} something…`
            }
            onChange={(e) => setMessage(e.target.value)}
            onKeyDown={(e) => {
              if (e.key === "Enter" && (e.metaKey || e.ctrlKey)) {
                e.preventDefault()
                if (!busy && message.trim()) void start()
              }
            }}
          />
        </Field>
      )}
      {mode === "chat" && bringing && (lines || changed > 0) && (
        <fieldset className="grid gap-1.5">
          <legend className="mb-1 section-label text-muted-foreground">
            Comes along
          </legend>
          {lines && (
            <label className="flex items-center gap-2 text-sm">
              <Checkbox
                checked={withTerminal}
                onCheckedChange={(v) => setWithTerminal(v === true)}
                disabled={busy}
              />
              <span>
                Terminal <span className="font-mono">{bringing.term}</span>,{" "}
                {lines.includes("\n")
                  ? `its last ${lines.split("\n").length} lines`
                  : "its last line"}
              </span>
            </label>
          )}
          {changed > 0 && (
            <label className="flex items-center gap-2 text-sm">
              <Checkbox
                checked={withChanges}
                onCheckedChange={(v) => setWithChanges(v === true)}
                disabled={busy}
              />
              <span>
                {changed} uncommitted {changed === 1 ? "change" : "changes"}
                {branchOf(git.data?.repos) && (
                  <>
                    {" "}
                    on{" "}
                    <span className="font-mono">
                      {branchOf(git.data?.repos)}
                    </span>
                  </>
                )}
              </span>
            </label>
          )}
        </fieldset>
      )}
      {problem && (
        <p role="alert" className="text-xs text-destructive">
          {problem}
        </p>
      )}
      <DialogFooter className="items-center">
        {step && (
          <span
            role="status"
            className="mr-auto flex items-center gap-2 text-xs text-muted-foreground"
          >
            <Spinner className="size-3.5" />
            {step}
          </span>
        )}
        <Button type="button" variant="outline" onClick={close}>
          Cancel
        </Button>
        <Button
          type="submit"
          disabled={
            busy ||
            !computer ||
            (mode === "chat" &&
              !message.trim() &&
              !(bringing && withTerminal && lines) &&
              !(bringing && withChanges && changed > 0))
          }
        >
          {action}
        </Button>
      </DialogFooter>
    </form>
  )
}

function Header({ teammate, bring }: { teammate: Teammate; bring?: boolean }) {
  return (
    <DialogHeader>
      <DialogTitle className="flex items-center gap-2">
        <TeammateAvatar
          name={teammate.name}
          avatar={teammate.avatar}
          className="size-6 text-caption"
        />
        {bring
          ? `Bring ${teammate.name} in`
          : `Start work with ${teammate.name}`}
      </DialogTitle>
      <DialogDescription>
        {bring
          ? `${teammate.name} joins what you are doing: a thread on this computer, with what you pick below.`
          : "A chat or the agent in a terminal is a thread in the sidebar; the computer alone is not."}
      </DialogDescription>
    </DialogHeader>
  )
}

/** The branch of the first repository with changes, when it has one. */
function branchOf(repos?: GitRepo[] | null) {
  return repos?.find((r) => r.changes?.length)?.branch
}

/** The uncommitted changes as a note for the first message: each repository and its files. */
function changesNote(repos: GitRepo[]) {
  const out: string[] = []
  for (const r of repos) {
    const files = [
      ...new Map((r.changes ?? []).map((c) => [c.path, c])).values(),
    ]
    if (!files.length) continue
    const where = r.path ? `/workspace/${r.path}` : "/workspace"
    out.push(
      `Uncommitted changes in ${where}${r.branch ? ` on branch ${r.branch}` : ""}:\n` +
        files
          .slice(0, 50)
          .map(
            (c) =>
              `- ${c.path} (${c.status}${c.binary ? "" : `, +${c.added} −${c.removed}`})`
          )
          .join("\n") +
        (files.length > 50 ? `\n- and ${files.length - 50} more` : "")
    )
  }
  return out.join("\n\n") + (out.length ? "\n\nSee them with git diff." : "")
}
