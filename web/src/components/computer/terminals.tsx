import { useEffect, useState } from "react"
import { useQueryClient } from "@tanstack/react-query"
import {
  CaretDownIcon,
  PlusIcon,
  RobotIcon,
  TerminalWindowIcon,
  XIcon,
} from "@phosphor-icons/react"
import { useStartAgentSession } from "@/components/agent-sessions"
import { Terminal } from "@/components/terminal"
import { Button } from "@/components/ui/button"
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuGroup,
  DropdownMenuItem,
  DropdownMenuLabel,
  DropdownMenuSeparator,
  DropdownMenuTrigger,
} from "@/components/ui/dropdown-menu"
import { Spinner } from "@/components/ui/spinner"
import { $api, type Sandbox, type Teammate } from "@/lib/api/client"
import { harnessLabels } from "@/lib/teammates"
import { useTerminalTheme } from "@/lib/theme"
import { cn } from "@/lib/utils"
import { pasteFile } from "@/lib/workspace-upload"

/**
 * The Terminal tab: the guest's tmux sessions, one in view and the others behind a chip in the
 * corner. Sessions outlive the browser, and those an agent starts show up too. Each one opened
 * here stays mounted while hidden, so switching keeps its scrollback and connection.
 */
export function Terminals({
  env,
  sandbox: sb,
  owner,
  term,
  visible,
  suspended,
  onTerm,
}: {
  env: string
  sandbox: Sandbox
  owner?: Teammate
  /** The session the URL names; the first visit opens "main". */
  term?: string
  /** Whether the Terminal tab is the one in view. */
  visible: boolean
  suspended: boolean
  onTerm: (name: string | undefined) => void
}) {
  const queryClient = useQueryClient()
  const sessions = $api.useQuery(
    "get",
    "/api/environments/{env}/sandboxes/{id}/terminals",
    { params: { path: { env, id: sb.id } } },
    { refetchInterval: 5000, enabled: !suspended }
  )
  const close = $api.useMutation(
    "delete",
    "/api/environments/{env}/sandboxes/{id}/terminals/{name}",
    {
      onSettled: () =>
        queryClient.invalidateQueries({
          queryKey: ["get", "/api/environments/{env}/sandboxes/{id}/terminals"],
        }),
    }
  )
  // Sessions being closed leave the list until the guest no longer reports them.
  const [closing, setClosing] = useState<ReadonlySet<string>>(new Set())
  const listed = [...(sessions.data ?? [])]
    .sort(
      // tmux counts in seconds, so sessions made in the same one go main first, then by name
      (a, b) =>
        a.created - b.created ||
        Number(b.name === "main") - Number(a.name === "main") ||
        a.name.localeCompare(b.name, undefined, { numeric: true })
    )
    .filter((s) => !closing.has(s.name))
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

  const drop = (name: string) => {
    setClosing((prev) => new Set(prev).add(name))
    setMounted((prev) => prev.filter((n) => n !== name))
    if (name !== current) return
    const next = names.find((n) => n !== name)
    if (!next) setNone(true)
    onTerm(next)
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
  const agent = useStartAgentSession(env, sb.id, open)
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
        <Button size="sm" variant="secondary" onClick={() => open(fresh())}>
          <PlusIcon />
          New shell
        </Button>
      </div>
    )
  }

  const all = [...new Set([...names, ...mounted])]
  const harnessOf = (name: string) => {
    const h = listed.find((s) => s.name === name)?.harness
    return h && h in harnessLabels
      ? harnessLabels[h as keyof typeof harnessLabels]
      : h
  }

  return (
    <div className={cn("relative flex min-h-0 flex-1", !visible && "hidden")}>
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
      <DropdownMenu>
        <DropdownMenuTrigger
          aria-label={`Terminal session ${current}`}
          title="Terminal sessions"
          className={cn(
            "absolute top-2.5 right-3 z-10 inline-flex h-6 items-center gap-1.5 rounded-md border px-2 font-mono text-[0.6875rem] backdrop-blur-sm",
            dark
              ? "border-white/10 bg-(--wb-term-bg)/90 text-(--wb-term-fg)/70 hover:text-(--wb-term-fg) data-popup-open:text-(--wb-term-fg)"
              : "bg-background/90 text-muted-foreground hover:text-foreground data-popup-open:text-foreground"
          )}
        >
          {harnessOf(current) ? (
            <RobotIcon className="size-3" />
          ) : (
            <TerminalWindowIcon className="size-3" />
          )}
          {current}
          {all.length > 1 && <span className="opacity-60">{all.length}</span>}
          <CaretDownIcon className="size-2.5" />
        </DropdownMenuTrigger>
        <DropdownMenuContent align="end" className="w-60">
          <DropdownMenuGroup>
            <DropdownMenuLabel>Sessions on {sb.name}</DropdownMenuLabel>
            {all.map((name) => (
              <DropdownMenuItem
                key={name}
                aria-current={name === current || undefined}
                onClick={() => open(name)}
              >
                {harnessOf(name) ? <RobotIcon /> : <TerminalWindowIcon />}
                <span className="font-mono">{name}</span>
                <span className="ml-auto text-[0.6875rem] text-muted-foreground">
                  {harnessOf(name) ?? (name === current ? "open" : "")}
                </span>
              </DropdownMenuItem>
            ))}
          </DropdownMenuGroup>
          <DropdownMenuSeparator />
          <DropdownMenuItem onClick={() => open(fresh())}>
            <PlusIcon />
            New shell
          </DropdownMenuItem>
          {owner && (
            <DropdownMenuItem disabled={agent.starting} onClick={agent.start}>
              {agent.starting ? <Spinner /> : <RobotIcon />}
              <span className="grid">
                {owner.name} in a terminal
                <span className="text-[0.6875rem] text-muted-foreground">
                  {harnessLabels[owner.harness]}&apos;s own interface
                </span>
              </span>
            </DropdownMenuItem>
          )}
          <DropdownMenuSeparator />
          <DropdownMenuItem
            onClick={() => {
              drop(current)
              close.mutate({
                params: { path: { env, id: sb.id, name: current } },
              })
            }}
          >
            <XIcon />
            <span className="grid">
              Close <span className="font-mono">{current}</span>
              <span className="text-[0.6875rem] text-muted-foreground">
                Ends what runs in it
              </span>
            </span>
          </DropdownMenuItem>
        </DropdownMenuContent>
      </DropdownMenu>
    </div>
  )
}
