import { Link } from "@tanstack/react-router"
import { ListChecksIcon } from "@phosphor-icons/react"
import { Button } from "@/components/ui/button"
import { SectionHeader } from "@/components/section-header"
import {
  doneSteps,
  firstOpenStep,
  setupOver,
  setupSteps,
  useSetup,
} from "@/lib/setup"
import { cn } from "@/lib/utils"

/**
 * Home's reminder of the guided setup: how many steps are done, and the next one. It stays
 * until the setup is complete or dismissed.
 */
export function SetupCard({ env }: { env: string }) {
  const { setup, record, saving } = useSetup(env)
  if (!setup || setupOver(setup)) return null
  const next = firstOpenStep(setup)
  return (
    <section
      aria-label="Finish setup"
      className="rounded-lg border border-dashed border-primary/40 bg-primary/[0.035] p-3"
    >
      <SectionHeader
        title="Finish setup"
        icon={ListChecksIcon}
        count={`${doneSteps(setup)}/${setupSteps.length}`}
        actions={
          <Button
            variant="ghost"
            size="xs"
            disabled={saving}
            onClick={() => record(setup.steps, true)}
          >
            Dismiss
          </Button>
        }
      />
      <div className="mt-2.5 flex gap-0.5">
        {setupSteps.map((s) => (
          <span
            key={s.key}
            title={s.title}
            className={cn(
              "h-1 flex-1 rounded-[1px]",
              setup.steps[s.key] === "done" ? "bg-primary" : "bg-foreground/10"
            )}
          />
        ))}
      </div>
      <div className="mt-2.5 flex items-center gap-2 text-xs">
        {next >= 0 && (
          <span className="min-w-0 truncate text-muted-foreground">
            Next: {setupSteps[next].title}
          </span>
        )}
        <Button size="xs" className="ml-auto" render={<Link to="/setup" />}>
          Continue
        </Button>
      </div>
    </section>
  )
}
