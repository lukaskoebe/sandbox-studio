import { useEffect, useRef, useState } from "react"
import type { KeyboardEvent } from "react"
import { useQueryClient } from "@tanstack/react-query"
import { PlusIcon, RobotIcon, TerminalIcon, XIcon } from "@phosphor-icons/react"
import { Terminal } from "@/components/terminal"
import {
  AlertDialog,
  AlertDialogAction,
  AlertDialogCancel,
  AlertDialogContent,
  AlertDialogDescription,
  AlertDialogFooter,
  AlertDialogHeader,
  AlertDialogTitle,
} from "@/components/ui/alert-dialog"
import { Button } from "@/components/ui/button"
import { Spinner } from "@/components/ui/spinner"
import {
  Tooltip,
  TooltipContent,
  TooltipTrigger,
} from "@/components/ui/tooltip"
import { $api, type Sandbox, type Terminal as Session } from "@/lib/api/client"
import {
  runsProgram,
  sessionKeys,
  sortTerminals,
  terminalCommand,
} from "@/lib/terminals"
import { useTerminalTheme } from "@/lib/theme"
import { cn } from "@/lib/utils"
import { pasteFile } from "@/lib/workspace-upload"

const terminalsPath = "/api/environments/{env}/sandboxes/{id}/terminals"

/**
 * The Terminal tab: the guest's tmux sessions as a strip of tabs over the one in view. Sessions
 * outlive the browser, and those an agent starts show up too. Each one opened here stays
 * mounted while hidden, so switching keeps its scrollback and connection.
 */
export function Terminals({
  env,
  sandbox: sb,
  term,
  visible,
  suspended,
  onTerm,
}: {
  env: string
  sandbox: Sandbox
  /** The session the URL names; the first visit opens "main". */
  term?: string
  /** Whether the Terminal tab is the one in view. */
  visible: boolean
  suspended: boolean
  onTerm: (name: string | undefined) => void
}) {
  const queryClient = useQueryClient()
  const path = { env, id: sb.id }
  const sessions = $api.useQuery(
    "get",
    terminalsPath,
    { params: { path } },
    { refetchInterval: 5000, enabled: !suspended }
  )
  const refetch = () =>
    queryClient.invalidateQueries({ queryKey: ["get", terminalsPath] })
  const close = $api.useMutation(
    "delete",
    "/api/environments/{env}/sandboxes/{id}/terminals/{name}",
    { onSettled: refetch }
  )
  const rename = $api.useMutation(
    "put",
    "/api/environments/{env}/sandboxes/{id}/terminals/{name}/title",
    { onSettled: refetch }
  )
  // Sessions being closed leave the list until the guest no longer reports them.
  const [closing, setClosing] = useState<ReadonlySet<string>>(new Set())
  const listed = sortTerminals(sessions.data ?? []).filter(
    (s) => !closing.has(s.name)
  )
  const names = listed.map((s) => s.name)
  // Closing the last session leaves none open until a new one is asked for.
  const [none, setNone] = useState(false)
  const current =
    term ??
    (none
      ? undefined
      : ((names.includes("main") ? "main" : names.at(0)) ??
        (sessions.data ? "main" : undefined)))
  const [mounted, setMounted] = useState<string[]>([])
  useEffect(() => {
    if (!current) return
    setMounted((prev) => (prev.includes(current) ? prev : [...prev, current]))
  }, [current])
  useEffect(() => {
    if (!sessions.data) return
    const live = new Set(sessions.data.map((s) => s.name))
    setClosing((prev) => {
      const still = new Set([...prev].filter((n) => live.has(n)))
      return still.size === prev.size ? prev : still
    })
  }, [sessions.data])
  // A session that closes while asking first, after the user said yes.
  const [confirm, setConfirm] = useState<Session>()

  const drop = (name: string) => {
    setClosing((prev) => new Set(prev).add(name))
    setMounted((prev) => prev.filter((n) => n !== name))
    if (name !== current) return
    const next = names.find((n) => n !== name)
    if (!next) setNone(true)
    onTerm(next)
  }
  const end = (name: string) => {
    drop(name)
    close.mutate({ params: { path: { ...path, name } } })
  }
  const ask = (name: string) => {
    const s = listed.find((x) => x.name === name)
    if (s && runsProgram(s)) setConfirm(s)
    else end(name)
  }
  const open = (name: string) => {
    setNone(false)
    onTerm(name)
  }
  const fresh = () => {
    if (!names.includes("main") && !mounted.includes("main")) return "main"
    let n = 2
    while (names.includes(String(n)) || mounted.includes(String(n))) n++
    return String(n)
  }
  const openFresh = () => {
    open(fresh())
    // The guest names the new shell once tmux has started it.
    setTimeout(refetch, 800)
  }
  const dark = useTerminalTheme() === "dark"

  if (!current) {
    if (!visible) return null
    if (!none) return <Spinner className="m-auto" />
    return (
      <div
        className={cn(
          "m-auto grid justify-items-center gap-3 text-center text-xs",
          dark ? "text-(--wb-term-fg)/70" : "text-muted-foreground"
        )}
      >
        No terminal open
        <Button size="sm" variant="secondary" onClick={openFresh}>
          <PlusIcon />
          New shell
        </Button>
      </div>
    )
  }

  const all = [...new Set([...names, ...mounted])]
  const byName = new Map(listed.map((s) => [s.name, s]))
  const step = (by: number) => {
    const i = all.indexOf(current)
    open(all[(i + by + all.length) % all.length])
  }
  // Captured before xterm sees the keys, which would send them to the shell.
  const onKeyDownCapture = (e: KeyboardEvent) => {
    if (!e.altKey || !e.shiftKey || e.ctrlKey || e.metaKey) return
    const act = {
      KeyT: openFresh,
      KeyW: () => ask(current),
      BracketLeft: () => step(-1),
      BracketRight: () => step(1),
    }[e.code]
    if (!act) return
    e.preventDefault()
    e.stopPropagation()
    act()
  }

  return (
    <div
      className={cn("flex min-h-0 flex-1 flex-col", !visible && "hidden")}
      onKeyDownCapture={onKeyDownCapture}
    >
      <div
        role="tablist"
        aria-label="Terminal sessions"
        className={cn(
          "flex h-6 shrink-0 [scrollbar-width:none] items-stretch overflow-x-auto border-b",
          dark
            ? "border-white/8 bg-black/25 text-(--wb-term-fg)/65"
            : "bg-foreground/[0.035] text-muted-foreground"
        )}
      >
        {all.map((name, i) => (
          <SessionTab
            key={name}
            name={name}
            session={byName.get(name)}
            active={name === current}
            dark={dark}
            last={i === all.length - 1}
            onOpen={() => open(name)}
            onClose={() => ask(name)}
            onRename={(title) =>
              rename.mutate({
                params: { path: { ...path, name } },
                body: { title },
              })
            }
          />
        ))}
        <Tooltip>
          <TooltipTrigger
            delay={400}
            render={
              <button
                type="button"
                aria-label="New shell"
                onClick={openFresh}
                className={cn(
                  "flex shrink-0 items-center px-2 outline-none focus-visible:ring-1 focus-visible:ring-ring focus-visible:ring-inset",
                  dark ? "hover:text-(--wb-term-fg)" : "hover:text-foreground"
                )}
              />
            }
          >
            <PlusIcon className="size-3" />
          </TooltipTrigger>
          <TooltipContent side="bottom">
            New shell · {sessionKeys.open}
          </TooltipContent>
        </Tooltip>
      </div>
      <div className="relative flex min-h-0 flex-1">
        {mounted.map((name) => (
          <Terminal
            key={name}
            url={`/api/environments/${env}/sandboxes/${sb.id}/terminals/${name}/attach`}
            active={visible && name === current}
            suspended={suspended}
            onExit={() => drop(name)}
            onPasteFile={(file) => pasteFile(env, sb.id, file)}
          />
        ))}
      </div>
      <AlertDialog
        open={!!confirm}
        onOpenChange={(o) => !o && setConfirm(undefined)}
      >
        <AlertDialogContent>
          <AlertDialogHeader>
            <AlertDialogTitle>Close {confirm?.label}?</AlertDialogTitle>
            <AlertDialogDescription>
              {confirm?.program} runs in it, and ends with it.
            </AlertDialogDescription>
          </AlertDialogHeader>
          <AlertDialogFooter>
            <AlertDialogCancel>Cancel</AlertDialogCancel>
            <AlertDialogAction
              variant="destructive"
              onClick={() => {
                if (confirm) end(confirm.name)
                setConfirm(undefined)
              }}
            >
              Close
            </AlertDialogAction>
          </AlertDialogFooter>
        </AlertDialogContent>
      </AlertDialog>
    </div>
  )
}

/**
 * One session in the strip: its icon, label and what it does, with × on hover. Double-click
 * renames it; the tooltip says what runs, where, and the keys.
 */
function SessionTab({
  name,
  session: s,
  active,
  dark,
  last,
  onOpen,
  onClose,
  onRename,
}: {
  name: string
  /** What the guest last said of it; a session just opened has no report yet. */
  session?: Session
  active: boolean
  dark: boolean
  last: boolean
  onOpen: () => void
  onClose: () => void
  onRename: (title: string) => void
}) {
  const [editing, setEditing] = useState(false)
  // A new name shows until the guest reports it.
  const [renamed, setRenamed] = useState<string>()
  useEffect(() => setRenamed(undefined), [s?.label])
  const label = renamed ?? s?.label ?? name
  const input = useRef<HTMLInputElement>(null)
  useEffect(() => {
    if (editing) input.current?.select()
  }, [editing])
  const save = () => {
    const title = input.current?.value.trim() ?? ""
    setEditing(false)
    if (title === (s?.title ?? "")) return
    setRenamed(title || undefined)
    onRename(title)
  }
  const Icon = s?.harness || s?.state === "working" ? RobotIcon : TerminalIcon
  return (
    <div
      className={cn(
        "group/tab flex shrink-0 items-stretch border-t-2 border-r border-t-transparent",
        dark ? "border-r-white/8" : "border-r-border",
        last && "border-r-0",
        active &&
          (dark
            ? "border-t-primary bg-(--wb-term-bg) text-(--wb-term-fg)"
            : "border-t-primary bg-[#fbfcfc] text-foreground")
      )}
    >
      <Tooltip>
        <TooltipTrigger
          delay={600}
          render={
            <button
              type="button"
              role="tab"
              aria-selected={active}
              onClick={onOpen}
              onDoubleClick={() => setEditing(true)}
              className={cn(
                "flex max-w-56 items-center gap-1.5 pr-1 pl-2 font-mono text-caption whitespace-nowrap outline-none focus-visible:ring-1 focus-visible:ring-ring focus-visible:ring-inset",
                !active &&
                  (dark ? "hover:text-(--wb-term-fg)" : "hover:text-foreground")
              )}
            />
          }
        >
          <Icon className="size-3 shrink-0" />
          {editing ? (
            <input
              ref={input}
              aria-label={`Name of ${label}`}
              defaultValue={s?.title ?? label}
              maxLength={40}
              onClick={(e) => e.stopPropagation()}
              onKeyDown={(e) => {
                e.stopPropagation()
                if (e.key === "Enter") save()
                if (e.key === "Escape") setEditing(false)
              }}
              onBlur={save}
              className="w-28 bg-transparent font-mono outline-none"
            />
          ) : (
            <span className="min-w-0 truncate">{label}</span>
          )}
          {s?.state === "working" && (
            <span
              aria-label="working"
              className="size-1.5 shrink-0 animate-pulse rounded-full bg-(--st-running)"
            />
          )}
          {s?.state === "busy" && (
            <span
              aria-label="a program runs"
              className="size-1.5 shrink-0 rounded-full ring-1 ring-current ring-inset"
            />
          )}
        </TooltipTrigger>
        <TooltipContent
          side="bottom"
          align="start"
          className="grid max-w-md justify-items-start gap-0.5 font-mono text-caption"
        >
          <span>{s ? terminalCommand(s) : name}</span>
          {s?.path && <span className="opacity-75">in {s.path}</span>}
          <span className="opacity-60">
            Double-click to rename · {sessionKeys.next} next
          </span>
        </TooltipContent>
      </Tooltip>
      <button
        type="button"
        aria-label={`Close ${label}`}
        title={`Close · ${sessionKeys.close}`}
        onClick={onClose}
        className={cn(
          "flex items-center pr-1.5 pl-0.5 opacity-0 outline-none group-hover/tab:opacity-70 hover:opacity-100! focus-visible:opacity-100",
          active && "opacity-50"
        )}
      >
        <XIcon className="size-2.5" />
      </button>
    </div>
  )
}
