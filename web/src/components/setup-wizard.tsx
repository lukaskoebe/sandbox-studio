import { useState } from "react"
import { useNavigate } from "@tanstack/react-router"
import { CheckIcon, MinusIcon } from "@phosphor-icons/react"
import { Button } from "@/components/ui/button"
import { Spinner } from "@/components/ui/spinner"
import { SetupStepBody } from "@/components/setup-steps"
import { errorMessage } from "@/lib/api/client"
import { doneSteps, firstOpenStep, setupSteps, useSetup } from "@/lib/setup"
import { cn } from "@/lib/utils"

type Outcome = "done" | "skipped"

const outcomeWords: Record<Outcome | "todo", string> = {
  done: "Done",
  skipped: "Skipped",
  todo: "To do",
}

/**
 * The guided setup as a vertical stepper. Each step is a row; the open one shows its body and
 * the buttons to move on. Steps are skippable and resumable: the first step without an outcome
 * opens first, and any row can be picked.
 */
export function SetupWizard({ env }: { env: string }) {
  const navigate = useNavigate()
  const { setup, record, saving, isPending, error } = useSetup(env)
  const [picked, setPicked] = useState<number>()
  if (!setup)
    return isPending ? (
      <Spinner className="mx-auto block" />
    ) : (
      <p className="text-xs text-destructive">
        {error ? errorMessage(error) : "The setup could not be loaded."}
      </p>
    )

  const current = picked ?? firstOpenStep(setup)
  const last = setupSteps.length - 1
  // Records the step's outcome with the others as they stand. The last step ends the setup on
  // Home; any other moves to the next row.
  const finish = (i: number, outcome: Outcome) => {
    record({ ...setup.steps, [setupSteps[i].key]: outcome })
    if (i === last) navigate({ to: "/e/$env", params: { env } })
    else setPicked(i + 1)
  }

  return (
    <div className="grid gap-4">
      <p className="font-mono text-xs text-muted-foreground">
        {doneSteps(setup)}/{setupSteps.length} done
        {setup.completedAt &&
          " · Setup is complete; any step can still be changed on its page"}
      </p>
      <ol className="grid gap-2">
        {setupSteps.map((step, i) => {
          const outcome = setup.steps[step.key]
          const open = i === current
          const label =
            i === last
              ? "Finish"
              : step.key === "teammate" && outcome === "done"
                ? "Accept"
                : "Next"
          return (
            <li key={step.key} className="rounded-lg ring-1 ring-foreground/10">
              <button
                type="button"
                aria-expanded={open}
                onClick={() => setPicked(i)}
                className="flex w-full items-center gap-3 rounded-lg p-3 text-left outline-none hover:bg-muted/50 focus-visible:ring-2 focus-visible:ring-ring/30"
              >
                <Mark n={i + 1} outcome={outcome} />
                <span className="text-sm font-medium">{step.title}</span>
                <span className="ml-auto text-xs text-muted-foreground">
                  {outcomeWords[outcome ?? "todo"]}
                </span>
              </button>
              {open && (
                <div className="grid gap-4 border-t p-4">
                  <SetupStepBody step={step.key} env={env} />
                  <div className="flex flex-wrap items-center justify-end gap-2">
                    {i > 0 && (
                      <Button variant="ghost" onClick={() => setPicked(i - 1)}>
                        Back
                      </Button>
                    )}
                    {outcome === undefined && (
                      <Button
                        variant="outline"
                        disabled={saving}
                        onClick={() => finish(i, "skipped")}
                      >
                        Skip
                      </Button>
                    )}
                    {/* Network has no condition to check, so going through it is the step. */}
                    {(outcome !== undefined || step.key === "network") && (
                      <Button
                        disabled={saving}
                        onClick={() => finish(i, outcome ?? "done")}
                      >
                        {label}
                      </Button>
                    )}
                  </div>
                </div>
              )}
            </li>
          )
        })}
      </ol>
    </div>
  )
}

/** The step's number, or its outcome as a mark. */
function Mark({ n, outcome }: { n: number; outcome?: Outcome }) {
  return (
    <span
      className={cn(
        "flex size-6 shrink-0 items-center justify-center rounded-full border font-mono text-[0.6875rem] text-muted-foreground",
        outcome === "done" &&
          "border-primary bg-primary text-primary-foreground",
        outcome === "skipped" && "border-dashed"
      )}
    >
      {outcome === "done" ? (
        <CheckIcon weight="bold" className="size-3.5" />
      ) : outcome === "skipped" ? (
        <MinusIcon className="size-3.5" />
      ) : (
        n
      )}
    </span>
  )
}
