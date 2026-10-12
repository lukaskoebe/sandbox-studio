import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog"
import { TeammateForm } from "@/components/teammate-form"
import type { Provider } from "@/lib/api/client"

/** Adds a teammate. Editing one happens on its settings page. */
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
      <DialogContent className="sm:max-w-lg">
        <DialogHeader>
          <DialogTitle>Add a teammate</DialogTitle>
          <DialogDescription>
            An agent identity: who it is, the harness it runs in, the model it
            uses and the name it commits under.
          </DialogDescription>
        </DialogHeader>
        <TeammateForm
          env={env}
          providers={providers}
          onSaved={() => onOpenChange(false)}
        />
      </DialogContent>
    </Dialog>
  )
}
