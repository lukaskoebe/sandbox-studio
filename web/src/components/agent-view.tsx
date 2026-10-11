import { ChatCircleDotsIcon, TerminalWindowIcon } from "@phosphor-icons/react"
import { toast } from "sonner"
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog"
import { ToggleGroup, ToggleGroupItem } from "@/components/ui/toggle-group"
import { errorMessage } from "@/lib/api/client"
import { useAgentView } from "@/lib/agent-view"
import { cn } from "@/lib/utils"

const choices = [
  {
    value: "chat",
    icon: ChatCircleDotsIcon,
    title: "Chat",
    text: "Write to your teammates like in a messenger. You see what they do and approve risky steps with a click.",
  },
  {
    value: "terminal",
    icon: TerminalWindowIcon,
    title: "Terminal",
    text: "Run each agent's own command-line app in the sandbox, as a developer would.",
  },
] as const

/**
 * Asks once how to work with agents: chat or terminal. It only orders the sandbox page and
 * picks what opens first; the other view is always one click away.
 */
export function AgentViewOnboarding() {
  const { view, set } = useAgentView()
  const choose = (v: "chat" | "terminal") =>
    set.mutate(v, {
      onError: (err) =>
        toast.error("Could not save your choice", {
          description: errorMessage(err),
        }),
    })
  return (
    <Dialog open={view === ""} onOpenChange={(open) => !open && choose("chat")}>
      <DialogContent showCloseButton={false} className="sm:max-w-md">
        <DialogHeader>
          <DialogTitle>How do you want to work with agents?</DialogTitle>
          <DialogDescription>
            You can switch any time in Settings, and the other way is always one
            click away.
          </DialogDescription>
        </DialogHeader>
        <div className="grid gap-2">
          {choices.map((c) => (
            <button
              key={c.value}
              type="button"
              disabled={set.isPending}
              onClick={() => choose(c.value)}
              className={cn(
                "flex items-start gap-3 rounded-lg border p-3 text-left transition-colors outline-none hover:bg-muted focus-visible:ring-2 focus-visible:ring-ring/30 disabled:opacity-50",
                c.value === "chat" && "border-primary/40"
              )}
            >
              <c.icon className="mt-0.5 size-5 shrink-0 text-primary" />
              <span className="grid gap-0.5">
                <span className="text-sm font-medium">
                  {c.title}
                  {c.value === "chat" && (
                    <span className="ml-2 text-xs font-normal text-muted-foreground">
                      Recommended
                    </span>
                  )}
                </span>
                <span className="text-xs text-muted-foreground">{c.text}</span>
              </span>
            </button>
          ))}
        </div>
      </DialogContent>
    </Dialog>
  )
}

/** The same choice as a toggle, for Settings. */
export function AgentViewToggle() {
  const { view, set } = useAgentView()
  return (
    <ToggleGroup
      aria-label="How agents open"
      variant="outline"
      value={view ? [view] : []}
      disabled={view === undefined || set.isPending}
      onValueChange={(v) => {
        const next = v[0] as "chat" | "terminal" | undefined
        if (next)
          set.mutate(next, {
            onError: (err) =>
              toast.error("Could not save the setting", {
                description: errorMessage(err),
              }),
          })
      }}
    >
      {choices.map((c) => (
        <ToggleGroupItem key={c.value} value={c.value} size="sm">
          <c.icon />
          {c.title}
        </ToggleGroupItem>
      ))}
    </ToggleGroup>
  )
}
