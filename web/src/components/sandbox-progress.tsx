import type { ReactNode } from "react"
import {
  CheckCircleIcon,
  CircleIcon,
  CubeIcon,
  WarningCircleIcon,
  WarningIcon,
} from "@phosphor-icons/react"
import { Alert, AlertDescription, AlertTitle } from "@/components/ui/alert"
import {
  Empty,
  EmptyContent,
  EmptyDescription,
  EmptyHeader,
  EmptyMedia,
  EmptyTitle,
} from "@/components/ui/empty"
import { Progress } from "@/components/ui/progress"
import { Spinner } from "@/components/ui/spinner"
import type { Sandbox } from "@/lib/api/client"
import { cn, formatBytes } from "@/lib/utils"

type Run = NonNullable<Sandbox["progress"]>
type Step = Run["step"]
type Pull = NonNullable<Run["pull"]>
type StepState = "done" | "running" | "failed" | "pending"

/** What each step of a create or fork is called on screen. */
const stepLabels: Record<Step, string> = {
  pull: "Pulling the image",
  vm: "Starting the VM",
  copy: "Copying the workspace",
  boot: "Booting the guest",
  services: "Starting services",
  agent: "Connecting the agent",
  configure: "Configuring secrets and CA",
}

const labelTones: Record<StepState, string> = {
  done: "",
  running: "font-medium",
  failed: "font-medium text-destructive",
  pending: "text-muted-foreground",
}

/**
 * A sandbox whose create or fork runs, or failed: its steps, the image pull while it runs, and
 * the error with the action that clears a failure.
 */
export function SandboxProgress({
  sandbox: sb,
  action,
}: {
  sandbox: Sandbox
  /** Shown after a failure, such as the delete button. */
  action?: ReactNode
}) {
  const run = sb.progress
  const failed = sb.status === "failed"
  const verb = run?.operation === "fork" ? "Forking" : "Creating"
  const steps: Step[] = run?.steps?.length ? run.steps : run ? [run.step] : []
  const current = run ? steps.indexOf(run.step) : -1

  return (
    <Empty>
      <EmptyHeader>
        <EmptyMedia variant="icon">
          {failed ? <WarningIcon /> : <CubeIcon />}
        </EmptyMedia>
        <EmptyTitle>
          {failed ? `${verb} the sandbox failed` : `${verb} the sandbox`}
        </EmptyTitle>
        <EmptyDescription>
          {failed
            ? "Delete the sandbox to try again."
            : "This can take a few minutes. The work keeps going in the background."}
        </EmptyDescription>
      </EmptyHeader>
      <EmptyContent>
        {steps.length > 0 && (
          <ol className="w-full space-y-2.5 text-left">
            {steps.map((step, i) => {
              const state = stepState(i, current, failed)
              return (
                <li
                  key={step}
                  aria-current={state === "running" ? "step" : undefined}
                  className="space-y-1.5"
                >
                  <div
                    className={cn("flex items-center gap-2", labelTones[state])}
                  >
                    <StepIcon state={state} />
                    {stepLabels[step]}
                  </div>
                  {state === "running" && step === "pull" && (
                    <PullBar pull={run?.pull} />
                  )}
                </li>
              )
            })}
          </ol>
        )}
        {failed && (
          <Alert variant="destructive" role="alert">
            <WarningIcon />
            <div className="min-w-0">
              <AlertTitle>
                {run ? `${stepLabels[run.step]} failed` : "Creation failed"}
              </AlertTitle>
              <AlertDescription>
                {run?.error || "No details were recorded."}
              </AlertDescription>
            </div>
          </Alert>
        )}
        {failed && action}
      </EmptyContent>
    </Empty>
  )
}

/** Steps before the current one are done, those after it pending. */
function stepState(index: number, current: number, failed: boolean): StepState {
  if (index < current) return "done"
  if (index > current) return "pending"
  return failed ? "failed" : "running"
}

function StepIcon({ state }: { state: StepState }) {
  if (state === "running") return <Spinner className="shrink-0" />
  if (state === "failed")
    return <WarningCircleIcon className="size-4 shrink-0 text-destructive" />
  if (state === "done")
    return (
      <CheckCircleIcon className="size-4 shrink-0 text-emerald-600 dark:text-emerald-400" />
    )
  return <CircleIcon className="size-4 shrink-0 text-muted-foreground/50" />
}

/**
 * The image pull's bar: by bytes once the total is known, else by layers, else indeterminate.
 */
function PullBar({ pull }: { pull?: Pull }) {
  let value: number | null = null
  let text = ""
  if (pull && pull.totalBytes > 0) {
    value = (pull.bytes / pull.totalBytes) * 100
    text = `${formatBytes(pull.bytes)} of ${formatBytes(pull.totalBytes)}`
  } else if (pull && pull.layers > 0) {
    value = (pull.layersDone / pull.layers) * 100
    text = `${pull.layersDone} of ${pull.layers} layers`
  }
  return (
    <div className="space-y-1 pl-6">
      <Progress value={value} />
      {text && <p className="text-muted-foreground tabular-nums">{text}</p>}
    </div>
  )
}
