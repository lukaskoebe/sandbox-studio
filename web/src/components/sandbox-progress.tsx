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
import { useQueryClient } from "@tanstack/react-query"
import { toast } from "sonner"
import { Button } from "@/components/ui/button"
import { Progress } from "@/components/ui/progress"
import { Spinner } from "@/components/ui/spinner"
import { $api, errorMessage, type Sandbox } from "@/lib/api/client"
import { cn, formatBytes } from "@/lib/utils"

type Run = NonNullable<Sandbox["progress"]>
type Step = Run["step"]
type Pull = NonNullable<Run["pull"]>
type Copy = NonNullable<Run["copy"]>
type StepState = "done" | "running" | "failed" | "pending"

/** What each step of a create, fork, rebase or disk resize is called on screen. */
const stepLabels: Record<Step, string> = {
  prepare: "Preparing the sandbox for the copy",
  pull: "Pulling the image",
  vm: "Starting the VM",
  copy: "Copying the files",
  boot: "Booting the guest",
  services: "Starting services",
  agent: "Connecting the agent",
  configure: "Configuring secrets and CA",
}

/** What each copied tree is called on screen. */
const treeLabels: Record<Copy["tree"], string> = {
  workspace: "Workspace",
  home: "Home directory",
  tools: "Tools",
  docker: "Docker's data",
}

const verbs: Record<Run["operation"], string> = {
  create: "Creating",
  fork: "Forking",
  rebase: "Rebasing",
  disks: "Resizing the disks of",
}

/**
 * Whether the sandbox's last rebase or disk resize failed. The sandbox is back as it was;
 * only its progress tells.
 */
export function rebaseFailed(sb: Sandbox) {
  return (
    (sb.progress?.operation === "rebase" ||
      sb.progress?.operation === "disks") &&
    sb.progress.state === "failed"
  )
}

const labelTones: Record<StepState, string> = {
  done: "",
  running: "font-medium",
  failed: "font-medium text-destructive",
  pending: "text-muted-foreground",
}

/**
 * A sandbox whose create, fork or rebase runs, or failed: its steps, the image pull while it
 * runs, and the error with the action that clears a failure.
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
  const rebase = run?.operation === "rebase" || run?.operation === "disks"
  const failed = sb.status === "failed" || run?.state === "failed"
  const verb = run ? verbs[run.operation] : "Creating"
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
            ? rebase
              ? run.operation === "disks"
                ? "The sandbox keeps its disks, in the state it was in."
                : "The sandbox stays on its old template, in the state it was in."
              : "Delete the sandbox to try again."
            : run?.queued
              ? "Sandboxes copy one at a time, so this one waits for the copy before it. It keeps running meanwhile."
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
                    {state === "running" && run?.queued
                      ? "Waiting for another sandbox's copy to finish"
                      : stepLabels[step]}
                  </div>
                  {state === "running" && step === "pull" && (
                    <PullBar pull={run?.pull} />
                  )}
                  {state === "running" && step === "copy" && (
                    <CopyBar copy={run?.copy} />
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

/**
 * The copy's bar, for the tree being copied: by bytes once its total is known, else
 * indeterminate. A tree that changes during the copy may pass the total it was counted at.
 */
function CopyBar({ copy }: { copy?: Copy }) {
  if (!copy)
    return (
      <div className="pl-6">
        <Progress value={null} />
      </div>
    )
  const known = copy.totalBytes > 0
  const size = known
    ? `${formatBytes(copy.bytes)} of ${formatBytes(copy.totalBytes)}`
    : copy.bytes > 0
      ? formatBytes(copy.bytes)
      : ""
  return (
    <div className="space-y-1 pl-6">
      <Progress
        value={
          known ? Math.min((copy.bytes / copy.totalBytes) * 100, 100) : null
        }
      />
      <p className="text-muted-foreground tabular-nums">
        {treeLabels[copy.tree]}
        {size && `: ${size}`}
      </p>
    </div>
  )
}

/** Dismisses the report of a failed rebase or disk resize. */
export function DismissRebaseButton({
  env,
  sandbox,
}: {
  env: string
  sandbox: Sandbox
}) {
  const queryClient = useQueryClient()
  const dismiss = $api.useMutation(
    "delete",
    "/api/environments/{env}/sandboxes/{id}/progress",
    {
      onSuccess: () =>
        Promise.all([
          queryClient.invalidateQueries({
            queryKey: ["get", "/api/environments/{env}/sandboxes"],
          }),
          queryClient.invalidateQueries({
            queryKey: ["get", "/api/environments/{env}/sandboxes/{id}"],
          }),
        ]),
      onError: (error) =>
        toast.error(`Could not dismiss: ${errorMessage(error)}`),
    }
  )
  return (
    <Button
      variant="outline"
      size="sm"
      disabled={dismiss.isPending}
      onClick={() =>
        dismiss.mutate({ params: { path: { env, id: sandbox.id } } })
      }
    >
      {dismiss.isPending && <Spinner />}
      Dismiss
    </Button>
  )
}

/**
 * A note above the workspace that the sandbox's last rebase or disk resize failed, and at
 * which step.
 */
export function RebaseFailedAlert({
  env,
  sandbox: sb,
}: {
  env: string
  sandbox: Sandbox
}) {
  const run = sb.progress
  if (!run || !rebaseFailed(sb)) return null
  return (
    <Alert variant="destructive" role="alert" className="m-2 w-auto shrink-0">
      <WarningIcon />
      <div className="min-w-0 space-y-2">
        <AlertTitle>
          {run.operation === "disks" ? "Disk resize" : "Rebase"} failed:{" "}
          {stepLabels[run.step]} failed
        </AlertTitle>
        <AlertDescription>
          {run.operation === "disks"
            ? "The sandbox keeps its disks."
            : "The sandbox stays on its old template."}{" "}
          {run.error || "No details were recorded."}
        </AlertDescription>
        <DismissRebaseButton env={env} sandbox={sb} />
      </div>
    </Alert>
  )
}
