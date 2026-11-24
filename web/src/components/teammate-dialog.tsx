import { useState } from "react"
import { NewTeammateForm } from "@/components/new-teammate-form"
import { ProviderForm } from "@/components/provider-dialog"
import { StepCount } from "@/components/step-count"
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog"
import type { Provider } from "@/lib/api/client"

/**
 * Adds a teammate. Editing one happens on its settings page. Without a provider in the
 * environment there is nothing to pick a model from, so it adds one first: step 1 of 2.
 */
export function TeammateDialog({
  env,
  providers,
  open,
  onOpenChange,
}: {
  env: string
  providers: Provider[]
  open: boolean
  onOpenChange: (open: boolean) => void
}) {
  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent className="sm:max-w-md">
        <TeammateSteps
          env={env}
          providers={providers}
          onDone={() => onOpenChange(false)}
        />
      </DialogContent>
    </Dialog>
  )
}

/** Mounted with the dialog, so whether adding takes two steps is settled as it opens. */
function TeammateSteps({
  env,
  providers,
  onDone,
}: {
  env: string
  providers: Provider[]
  onDone: () => void
}) {
  const [twoSteps] = useState(providers.length === 0)
  const [added, setAdded] = useState<Provider>()
  if (twoSteps && !added)
    return (
      <ProviderForm
        env={env}
        step={{ count: "1 of 2", onAdded: setAdded, onCancel: onDone }}
        onDone={onDone}
      />
    )
  // The provider just added, until the list has it.
  const list =
    added && !providers.some((p) => p.id === added.id)
      ? [...providers, added]
      : providers
  return (
    <>
      <DialogHeader>
        <DialogTitle>
          Add a teammate
          {twoSteps && <StepCount>2 of 2</StepCount>}
        </DialogTitle>
        <DialogDescription>
          Who it is, and the model it thinks with.
        </DialogDescription>
      </DialogHeader>
      <NewTeammateForm env={env} providers={list} onSaved={onDone} />
    </>
  )
}
