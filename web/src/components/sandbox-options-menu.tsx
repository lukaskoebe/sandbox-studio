import { useState } from "react"
import { toast } from "sonner"
import { useNavigate } from "@tanstack/react-router"
import {
  DotsThreeIcon,
  DownloadSimpleIcon,
  GitForkIcon,
  ArrowClockwiseIcon,
  ClockCounterClockwiseIcon,
  PauseIcon,
  PlayIcon,
  PlusIcon,
  StopIcon,
  TrashIcon,
} from "@phosphor-icons/react"
import { CreateSandboxDialog } from "@/components/create-sandbox-dialog"
import { ForkSandboxDialog } from "@/components/fork-sandbox-dialog"
import { RebaseSandboxDialog } from "@/components/rebase-sandbox-dialog"
import { HistorySheet } from "@/components/sandbox-details"
import { Button } from "@/components/ui/button"
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
import {
  useInvalidateSandboxes,
  useSandboxLifecycle,
} from "@/components/sandbox-lifecycle"
import { $api, errorMessage, type Sandbox } from "@/lib/api/client"

/**
 * The actions that are not on a sandbox's everyday path, on both of its pages: fork,
 * rebase, export, suspend or resume, stop and delete.
 */
export function SandboxOptionsMenu({
  env,
  sandbox: sb,
}: {
  env: string
  sandbox: Sandbox
}) {
  const [dialog, setDialog] = useState<
    "history" | "fork" | "another" | "rebase" | "delete"
  >()
  const { path, stop, suspend, resume, phase: p } = useSandboxLifecycle(env, sb)
  // A create or fork that runs or failed has no guest yet; only deleting it makes sense.
  const building = sb.status === "creating" || sb.status === "failed"
  const available = sb.status === "running" || sb.status === "stopped"
  const exportable = available
  const exportHref = `/api/environments/${encodeURIComponent(env)}/sandboxes/${encodeURIComponent(sb.id)}/export`

  return (
    <>
      <DropdownMenu modal={false}>
        <DropdownMenuTrigger
          render={
            <Button
              variant="ghost"
              size="icon-sm"
              aria-label="Sandbox options"
              title="Sandbox options"
            />
          }
        >
          <DotsThreeIcon weight="bold" />
        </DropdownMenuTrigger>
        <DropdownMenuContent align="end" className="w-60">
          <DropdownMenuGroup>
            <DropdownMenuLabel className="truncate">
              {sb.name}
            </DropdownMenuLabel>
            {!building && (
              <>
                <DropdownMenuItem onClick={() => setDialog("history")}>
                  <ClockCounterClockwiseIcon />
                  History and checkpoints
                </DropdownMenuItem>
                <DropdownMenuItem
                  disabled={!available}
                  title={
                    available
                      ? "Copy this sandbox into a new one"
                      : "Only a running or stopped sandbox can be forked"
                  }
                  onClick={() => setDialog("fork")}
                >
                  <GitForkIcon />
                  Fork
                </DropdownMenuItem>
                {sb.teammateId && (
                  <DropdownMenuItem onClick={() => setDialog("another")}>
                    <PlusIcon />
                    Another sandbox for this teammate
                  </DropdownMenuItem>
                )}
                <DropdownMenuItem
                  disabled={!available}
                  title={
                    available
                      ? "Move this sandbox onto another template"
                      : "Only a running or stopped sandbox can be rebased"
                  }
                  onClick={() => setDialog("rebase")}
                >
                  <ArrowClockwiseIcon />
                  Rebase
                </DropdownMenuItem>
                {exportable ? (
                  <DropdownMenuItem
                    render={
                      <a
                        href={exportHref}
                        download={`${sb.name}.studio-sandbox`}
                        title="Downloads the template, resources, /workspace and /home/agent; secrets, checkpoints and Docker state are left out."
                      />
                    }
                  >
                    <DownloadSimpleIcon />
                    Export
                  </DropdownMenuItem>
                ) : (
                  <DropdownMenuItem disabled>
                    <DownloadSimpleIcon />
                    Export
                  </DropdownMenuItem>
                )}
              </>
            )}
          </DropdownMenuGroup>
          {!building && (p === "ready" || p === "suspended") && (
            <>
              <DropdownMenuSeparator />
              <DropdownMenuGroup>
                {p === "suspended" ? (
                  <DropdownMenuItem
                    disabled={resume.isPending}
                    onClick={() => resume.mutate(path)}
                  >
                    {resume.isPending ? <Spinner /> : <PlayIcon />}
                    Resume
                  </DropdownMenuItem>
                ) : (
                  <DropdownMenuItem
                    disabled={suspend.isPending}
                    title="Pause the VM in place; everything continues on resume"
                    onClick={() => suspend.mutate(path)}
                  >
                    {suspend.isPending ? <Spinner /> : <PauseIcon />}
                    Suspend
                  </DropdownMenuItem>
                )}
              </DropdownMenuGroup>
            </>
          )}
          {!building &&
            (p === "ready" || p === "booting" || p === "suspended") && (
              <DropdownMenuItem
                disabled={stop.isPending}
                onClick={() => stop.mutate(path)}
              >
                {stop.isPending ? <Spinner /> : <StopIcon />}
                Stop
              </DropdownMenuItem>
            )}
          <DropdownMenuSeparator />
          <DropdownMenuItem
            variant="destructive"
            onClick={() => setDialog("delete")}
          >
            <TrashIcon />
            Delete
          </DropdownMenuItem>
        </DropdownMenuContent>
      </DropdownMenu>
      {/* Rendered beside the menu: the menu closes when an item is chosen. */}
      <HistorySheet
        env={env}
        sandbox={sb}
        open={dialog === "history"}
        onOpenChange={(open) => setDialog(open ? "history" : undefined)}
      />
      <CreateSandboxDialog
        env={env}
        teammate={sb.teammateId}
        open={dialog === "another"}
        onOpenChange={(open) => setDialog(open ? "another" : undefined)}
      />
      <ForkSandboxDialog
        env={env}
        sandbox={sb}
        open={dialog === "fork"}
        onOpenChange={(open) => setDialog(open ? "fork" : undefined)}
      />
      <RebaseSandboxDialog
        env={env}
        sandbox={sb}
        open={dialog === "rebase"}
        onOpenChange={(open) => setDialog(open ? "rebase" : undefined)}
      />
      <DeleteSandboxDialog
        env={env}
        sandbox={sb}
        open={dialog === "delete"}
        onOpenChange={(open) => setDialog(open ? "delete" : undefined)}
      />
    </>
  )
}

/** Deletes the sandbox after a confirmation, then returns to the environment's sandboxes. */
export function DeleteSandboxDialog({
  env,
  sandbox: sb,
  open,
  onOpenChange,
}: {
  env: string
  sandbox: Sandbox
  open: boolean
  onOpenChange: (open: boolean) => void
}) {
  const navigate = useNavigate()
  const invalidate = useInvalidateSandboxes()
  const path = { params: { path: { env, id: sb.id } } }
  const remove = $api.useMutation(
    "delete",
    "/api/environments/{env}/sandboxes/{id}",
    {
      onSuccess: () => {
        invalidate()
        navigate({ to: "/e/$env", params: { env } })
      },
      onError: (err) =>
        toast.error("Could not delete the sandbox", {
          description: errorMessage(err),
        }),
    }
  )

  return (
    <AlertDialog open={open} onOpenChange={onOpenChange}>
      <AlertDialogContent>
        <AlertDialogHeader>
          <AlertDialogTitle>Delete {sb.name}?</AlertDialogTitle>
          <AlertDialogDescription>
            The VM, its workspace, Docker images, and checkpoints are deleted.
            This can&apos;t be undone.
          </AlertDialogDescription>
        </AlertDialogHeader>
        <AlertDialogFooter>
          <AlertDialogCancel>Cancel</AlertDialogCancel>
          <AlertDialogAction
            variant="destructive"
            disabled={remove.isPending}
            onClick={() => remove.mutate(path)}
          >
            {remove.isPending && <Spinner />}
            Delete
          </AlertDialogAction>
        </AlertDialogFooter>
      </AlertDialogContent>
    </AlertDialog>
  )
}

/** The Delete button of a failed create, which has no options menu of its own. */
export function DeleteSandboxButton({
  env,
  sandbox,
}: {
  env: string
  sandbox: Sandbox
}) {
  const [open, setOpen] = useState(false)
  return (
    <>
      <Button variant="destructive" onClick={() => setOpen(true)}>
        <TrashIcon />
        Delete
      </Button>
      <DeleteSandboxDialog
        env={env}
        sandbox={sandbox}
        open={open}
        onOpenChange={setOpen}
      />
    </>
  )
}
