import { useState } from "react"
import { useQueryClient } from "@tanstack/react-query"
import { toast } from "sonner"
import {
  ModelChoice,
  firstModel,
  type ModelPick,
} from "@/components/model-choice"
import { TeammateAvatar } from "@/components/teammate-avatar"
import { Button } from "@/components/ui/button"
import { DialogFooter } from "@/components/ui/dialog"
import { Field, FieldError, FieldLabel } from "@/components/ui/field"
import { Input } from "@/components/ui/input"
import { Spinner } from "@/components/ui/spinner"
import {
  $api,
  errorMessage,
  errorStatus,
  type Provider,
  type Teammate,
} from "@/lib/api/client"

/**
 * Adds a teammate with only what it needs to start: a name, what it does, and its model. Its
 * face comes from the name; instructions, who it commits as and approvals keep their defaults
 * until its settings change them.
 */
export function NewTeammateForm({
  env,
  providers,
  onSaved,
}: {
  env: string
  providers: Provider[]
  onSaved: (teammate: Teammate) => void
}) {
  const queryClient = useQueryClient()
  const [name, setName] = useState("")
  const [role, setRole] = useState("")
  const [picked, setPicked] = useState<ModelPick>()
  const [formError, setFormError] = useState<string>()
  // Until one is picked, the first provider's usual model; a provider added in the step
  // before may arrive after the form.
  const pick = picked ?? firstModel(providers)
  const create = $api.useMutation("post", "/api/environments/{env}/teammates", {
    onSuccess: (teammate) => {
      queryClient.invalidateQueries({
        queryKey: ["get", "/api/environments/{env}/teammates"],
      })
      queryClient.invalidateQueries({ queryKey: ["get", "/api/setup"] })
      onSaved(teammate)
    },
    onError: (err) => {
      const status = errorStatus(err)
      if (status === 409 || status === 422) setFormError(errorMessage(err))
      else
        toast.error("Could not add the teammate", {
          description: errorMessage(err),
        })
    },
  })
  // Why Add is greyed out, said beside it.
  const reason = !name.trim()
    ? "Give it a name to add it."
    : !pick
      ? "Pick a model to add it."
      : undefined

  return (
    <form
      className="contents"
      onSubmit={(e) => {
        e.preventDefault()
        if (reason || !pick || create.isPending) return
        setFormError(undefined)
        create.mutate({
          params: { path: { env } },
          body: {
            name: name.trim(),
            role: role.trim() || undefined,
            providerId: pick.providerId,
            harness: pick.harness,
            model: pick.model || undefined,
            approvalScope: "environment",
          },
        })
      }}
    >
      <div className="grid gap-4">
        <div className="flex items-end gap-3">
          <TeammateAvatar
            name={name.trim() || "?"}
            className="mb-px size-8 shrink-0 text-caption"
          />
          <Field className="min-w-0 flex-1">
            <FieldLabel htmlFor="teammate-name">Name</FieldLabel>
            <Input
              id="teammate-name"
              autoFocus
              autoComplete="off"
              maxLength={64}
              placeholder="e.g. Ada"
              value={name}
              onChange={(e) => {
                setName(e.target.value)
                setFormError(undefined)
              }}
            />
          </Field>
        </div>
        <Field>
          <FieldLabel htmlFor="teammate-role">
            What it does
            <span className="font-normal text-muted-foreground">optional</span>
          </FieldLabel>
          <Input
            id="teammate-role"
            autoComplete="off"
            maxLength={200}
            placeholder="e.g. Reviews pull requests"
            value={role}
            onChange={(e) => setRole(e.target.value)}
          />
        </Field>
        <Field>
          <FieldLabel htmlFor="teammate-model">Model</FieldLabel>
          <ModelChoice
            id="teammate-model"
            providers={providers}
            value={pick}
            onChange={setPicked}
          />
        </Field>
        <p className="text-xs/relaxed text-muted-foreground">
          Its face comes from its name. Its instructions, who it commits as and
          its approvals are in its settings, with good defaults.
        </p>
        {formError && <FieldError>{formError}</FieldError>}
      </div>
      <DialogFooter className="items-center">
        {reason && (
          <p className="text-xs text-muted-foreground sm:mr-auto">{reason}</p>
        )}
        <Button type="submit" disabled={!!reason || create.isPending}>
          {create.isPending && <Spinner />}
          Add teammate
        </Button>
      </DialogFooter>
    </form>
  )
}
