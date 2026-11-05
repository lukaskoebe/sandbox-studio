import { useState } from "react"
import { useQueryClient } from "@tanstack/react-query"
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
import {
  ForkChoiceFields,
  type ForkChoices,
} from "@/components/fork-sandbox-dialog"
import { $api, errorMessage, type Sandbox } from "@/lib/api/client"

/** The small mark on a computer that is a base. */
export function BaseMark() {
  return (
    <span
      title="A base: new computers can start from it"
      className="shrink-0 rounded-sm border px-1 font-mono text-meta leading-3.5 font-normal text-muted-foreground"
    >
      base
    </span>
  )
}

/**
 * Makes a computer a base, a prepared one that new computers start from, and sets what their
 * copies take. Opened from the computer's menu, and mounted only while open.
 */
export function BaseDialog({
  env,
  sandbox,
  open,
  onOpenChange,
}: {
  env: string
  sandbox: Sandbox
  open: boolean
  onOpenChange: (open: boolean) => void
}) {
  const [choices, setChoices] = useState<ForkChoices>(
    () =>
      sandbox.forkDefaults ?? {
        docker: true,
        tools: true,
        script: false,
      }
  )
  const queryClient = useQueryClient()
  const done = {
    onSuccess: () => {
      queryClient.invalidateQueries({
        queryKey: ["get", "/api/environments/{env}/sandboxes"],
      })
      onOpenChange(false)
    },
  }
  const set = $api.useMutation(
    "put",
    "/api/environments/{env}/sandboxes/{id}/base",
    done
  )
  const clear = $api.useMutation(
    "delete",
    "/api/environments/{env}/sandboxes/{id}/base",
    done
  )
  const pending = set.isPending || clear.isPending
  const failure = set.error ?? clear.error
  const path = { env, id: sandbox.id }

  return (
    <Dialog
      open={open}
      onOpenChange={(next) => (next || !pending) && onOpenChange(next)}
    >
      <DialogContent showCloseButton={!pending}>
        <form
          className="contents"
          onSubmit={(event) => {
            event.preventDefault()
            if (!pending) set.mutate({ params: { path }, body: choices })
          }}
        >
          <DialogHeader>
            <DialogTitle>
              {sandbox.forkDefaults ? "Base settings for" : "Use"}{" "}
              {sandbox.name}
              {!sandbox.forkDefaults && " as a base"}
            </DialogTitle>
            <DialogDescription>
              New computer can start from a base: a computer you keep stopped,
              with your repositories and tools ready. Each copy has its
              template, resources and teammate, so a rule or secret scoped to
              the teammate reaches them all. Rules scoped to this computer stay
              here.
            </DialogDescription>
          </DialogHeader>
          <div className="grid gap-2">
            <p className="text-muted-foreground">
              A copy always gets <code className="font-mono">/workspace</code>{" "}
              and the home. By default it also does this:
            </p>
            <ForkChoiceFields
              choices={choices}
              disabled={pending}
              onChange={setChoices}
            />
          </div>
          {failure && (
            <p role="alert" className="text-destructive">
              {errorMessage(failure)}
            </p>
          )}
          <DialogFooter>
            {sandbox.forkDefaults && (
              <Button
                type="button"
                variant="outline"
                disabled={pending}
                onClick={() => clear.mutate({ params: { path } })}
              >
                {clear.isPending && <Spinner />}
                Stop using as a base
              </Button>
            )}
            <Button type="submit" disabled={pending}>
              {set.isPending && <Spinner />}
              {sandbox.forkDefaults ? "Save" : "Use as a base"}
            </Button>
          </DialogFooter>
        </form>
      </DialogContent>
    </Dialog>
  )
}
