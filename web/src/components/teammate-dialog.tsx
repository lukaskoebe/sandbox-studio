import { SettingsLink } from "@/components/settings/link"
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog"
import { Button } from "@/components/ui/button"
import { TeammateForm } from "@/components/teammate-form"
import type { Provider } from "@/lib/api/client"

/**
 * Adds a teammate. Editing one happens on its settings page. Without a provider in the
 * environment there is nothing to choose a model from, so it says that and leads to one.
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
      <DialogContent className="sm:max-w-xl lg:max-w-2xl">
        {providers.length ? (
          <>
            <DialogHeader>
              <DialogTitle>Add a teammate</DialogTitle>
              <DialogDescription>
                An agent identity: who it is, the harness it runs in, the model
                it uses and the name it commits under.
              </DialogDescription>
            </DialogHeader>
            <TeammateForm
              env={env}
              providers={providers}
              onSaved={() => onOpenChange(false)}
            />
          </>
        ) : (
          <>
            <DialogHeader>
              <DialogTitle>Add a provider first</DialogTitle>
              <DialogDescription>
                A teammate thinks with a model from an AI provider, and this
                environment has none yet. Sign in with a Claude or ChatGPT
                subscription, or add an API key. While the environment has no
                teammates, its first provider also brings one, Iris.
              </DialogDescription>
            </DialogHeader>
            <DialogFooter>
              <Button variant="outline" onClick={() => onOpenChange(false)}>
                Cancel
              </Button>
              <Button
                render={<SettingsLink section="providers" />}
                onClick={() => onOpenChange(false)}
              >
                Add a provider
              </Button>
            </DialogFooter>
          </>
        )}
      </DialogContent>
    </Dialog>
  )
}
