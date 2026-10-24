import { useState } from "react"
import { toast } from "sonner"
import { useNavigate } from "@tanstack/react-router"
import { TrashIcon } from "@phosphor-icons/react"
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
import { Spinner } from "@/components/ui/spinner"
import { useInvalidateSandboxes } from "@/components/sandbox-lifecycle"
import { $api, errorMessage, type Sandbox } from "@/lib/api/client"

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
