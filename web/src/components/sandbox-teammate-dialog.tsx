import { useState } from "react"
import { useQueryClient } from "@tanstack/react-query"
import { useNavigate } from "@tanstack/react-router"
import { toast } from "sonner"
import { Button } from "@/components/ui/button"
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog"
import { Spinner } from "@/components/ui/spinner"
import { TeammateField } from "@/components/teammate-field"
import { $api, errorMessage, type Sandbox } from "@/lib/api/client"

/**
 * Gives a stopped computer another teammate, or none. The new owner's rules, secrets and
 * harness files apply from its next start; the home stays as it is. A teammate's computer is on
 * its page, so the change goes there, or to the plain computer's page for none.
 */
export function SandboxTeammateDialog({
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
  const [teammateId, setTeammateId] = useState(sb.teammateId ?? "")
  const queryClient = useQueryClient()
  const navigate = useNavigate()
  const set = $api.useMutation(
    "put",
    "/api/environments/{env}/sandboxes/{id}/teammate",
    {
      onSuccess: async (changed) => {
        // The computer's page redirects by the cached list, so it has to be current first.
        await queryClient.invalidateQueries({
          queryKey: ["get", "/api/environments/{env}/sandboxes"],
          refetchType: "all",
        })
        onOpenChange(false)
        if (changed.teammateId)
          navigate({
            to: "/e/$env/teammates/$id",
            params: { env, id: changed.teammateId },
            search: { computer: sb.id },
          })
        else
          navigate({
            to: "/e/$env/sandboxes/$id",
            params: { env, id: sb.id },
          })
      },
      onError: (err) =>
        toast.error("Could not change the teammate", {
          description: errorMessage(err),
        }),
    }
  )
  const stopped = sb.status === "stopped"
  const same = teammateId === (sb.teammateId ?? "")

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent>
        <form
          className="contents"
          onSubmit={(e) => {
            e.preventDefault()
            if (!stopped || same) return
            set.mutate({
              params: { path: { env, id: sb.id } },
              body: { teammateId },
            })
          }}
        >
          <DialogHeader>
            <DialogTitle>Teammate of {sb.name}</DialogTitle>
            <DialogDescription>
              The teammate&apos;s rules, secrets and harness settings apply from
              the computer&apos;s next start. Its home stays as it is, with the
              harness history and logins in it, and chats stay with the teammate
              that had them.
            </DialogDescription>
          </DialogHeader>
          <TeammateField
            env={env}
            id="sandbox-owner"
            value={teammateId}
            onChange={setTeammateId}
            disabled={!stopped || set.isPending}
            description={
              stopped
                ? "The agent identity working in the computer."
                : "Stop the computer to change its teammate."
            }
          />
          <DialogFooter>
            <Button
              type="button"
              variant="outline"
              onClick={() => onOpenChange(false)}
            >
              Close
            </Button>
            <Button type="submit" disabled={!stopped || same || set.isPending}>
              {set.isPending && <Spinner />}
              Change
            </Button>
          </DialogFooter>
        </form>
      </DialogContent>
    </Dialog>
  )
}
