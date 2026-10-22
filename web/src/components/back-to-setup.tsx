import { Link } from "@tanstack/react-router"
import { ArrowLeftIcon } from "@phosphor-icons/react"
import type { SetupKey } from "@/lib/setup"

/** Where a page opened from the guided setup goes back to: the step it came from. */
export function BackToSetup({ step }: { step: SetupKey }) {
  return (
    <Link
      to="/setup"
      search={{ step }}
      className="inline-flex w-fit items-center gap-1.5 text-xs text-muted-foreground underline-offset-4 hover:text-foreground hover:underline"
    >
      <ArrowLeftIcon className="size-3" />
      Back to setup
    </Link>
  )
}

/** The ?from= of a page the guided setup links to. */
export const fromSetup = (s: Record<string, unknown>) =>
  s.from === "setup" ? { from: "setup" as const } : {}
