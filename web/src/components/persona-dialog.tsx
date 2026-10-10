import { useState } from "react"
import { useQueryClient } from "@tanstack/react-query"
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
import {
  Field,
  FieldDescription,
  FieldError,
  FieldLabel,
} from "@/components/ui/field"
import { Input } from "@/components/ui/input"
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select"
import { Spinner } from "@/components/ui/spinner"
import { ToggleGroup, ToggleGroupItem } from "@/components/ui/toggle-group"
import {
  $api,
  errorMessage,
  errorStatus,
  type Harness,
  type Persona,
  type Provider,
} from "@/lib/api/client"
import { harnessLabels, isSubscription, kindLabels } from "@/lib/personas"

const maxSoulBytes = 16 * 1024

/** The slug the server derives the default git email from. */
function slug(name: string) {
  return name
    .toLowerCase()
    .replace(/[^a-z0-9]+/g, "-")
    .replace(/^-+|-+$/g, "")
}

export function PersonaDialog({
  env,
  persona,
  providers,
  open,
  onOpenChange,
}: {
  env: string
  /** The persona to edit; absent for a new one. */
  persona?: Persona
  providers: Provider[]
  open: boolean
  onOpenChange: (open: boolean) => void
}) {
  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent className="max-h-[calc(100dvh-2rem)] overflow-y-auto sm:max-w-lg">
        <PersonaForm
          key={persona?.id ?? "new"}
          env={env}
          persona={persona}
          providers={providers}
          onDone={() => onOpenChange(false)}
        />
      </DialogContent>
    </Dialog>
  )
}

function PersonaForm({
  env,
  persona,
  providers,
  onDone,
}: {
  env: string
  persona?: Persona
  providers: Provider[]
  onDone: () => void
}) {
  const queryClient = useQueryClient()
  const [name, setName] = useState(persona?.name ?? "")
  const [role, setRole] = useState(persona?.role ?? "")
  const [providerId, setProviderId] = useState(
    persona?.providerId ?? providers.at(0)?.id ?? ""
  )
  const provider = providers.find((p) => p.id === providerId)
  const supported = provider?.harnesses ?? []
  const [harness, setHarness] = useState<Harness | undefined>(
    persona?.harness ?? supported[0]
  )
  const [model, setModel] = useState(persona?.model ?? "")
  const [gitName, setGitName] = useState(persona?.gitName ?? "")
  const [gitEmail, setGitEmail] = useState(persona?.gitEmail ?? "")
  const [soul, setSoul] = useState(persona?.soul ?? "")
  const [formError, setFormError] = useState<string>()

  const soulBytes = new TextEncoder().encode(soul).length
  const needsModel =
    provider !== undefined &&
    !isSubscription(provider.kind) &&
    provider.kind !== "openai_compatible"
  const valid =
    name.trim() !== "" &&
    provider !== undefined &&
    harness !== undefined &&
    supported.includes(harness) &&
    (!needsModel || model.trim() !== "") &&
    soulBytes <= maxSoulBytes

  const onSaved = () => {
    queryClient.invalidateQueries({
      queryKey: ["get", "/api/environments/{env}/personas"],
    })
    onDone()
  }
  const onFailed = (err: unknown) => {
    const status = errorStatus(err)
    if (status === 409 || status === 422) setFormError(errorMessage(err))
    else
      toast.error("Could not save the persona", {
        description: errorMessage(err),
      })
  }
  const create = $api.useMutation("post", "/api/environments/{env}/personas", {
    onSuccess: onSaved,
    onError: onFailed,
  })
  const update = $api.useMutation(
    "put",
    "/api/environments/{env}/personas/{id}",
    { onSuccess: onSaved, onError: onFailed }
  )
  const saving = create.isPending || update.isPending

  const submit = () => {
    if (!valid || saving) return
    setFormError(undefined)
    const body = {
      name: name.trim(),
      role: role.trim() || undefined,
      soul: soul || undefined,
      harness,
      providerId,
      model: model.trim() || undefined,
      gitName: gitName.trim() || undefined,
      gitEmail: gitEmail.trim() || undefined,
    }
    if (persona)
      update.mutate({ params: { path: { env, id: persona.id } }, body })
    else create.mutate({ params: { path: { env } }, body })
  }

  return (
    <form
      className="contents"
      onSubmit={(e) => {
        e.preventDefault()
        submit()
      }}
    >
      <DialogHeader>
        <DialogTitle>{persona ? "Edit persona" : "Add persona"}</DialogTitle>
        <DialogDescription>
          An agent identity: who it is, the harness it runs in, the model it
          uses and the name it commits under.
        </DialogDescription>
      </DialogHeader>
      <div className="grid gap-4 sm:grid-cols-2">
        <Field>
          <FieldLabel htmlFor="persona-name">Name</FieldLabel>
          <Input
            id="persona-name"
            autoFocus
            autoComplete="off"
            maxLength={64}
            placeholder="Ada"
            value={name}
            onChange={(e) => setName(e.target.value)}
          />
        </Field>
        <Field>
          <FieldLabel htmlFor="persona-role">Role</FieldLabel>
          <Input
            id="persona-role"
            autoComplete="off"
            maxLength={200}
            placeholder="Code reviewer"
            value={role}
            onChange={(e) => setRole(e.target.value)}
          />
        </Field>
      </div>
      <Field>
        <FieldLabel htmlFor="persona-provider">Provider</FieldLabel>
        <Select
          value={providerId}
          onValueChange={(v) => {
            const next = providers.find((p) => p.id === v)
            if (!next) return
            setProviderId(next.id)
            const harnesses = next.harnesses ?? []
            if (!harness || !harnesses.includes(harness))
              setHarness(harnesses[0])
            setFormError(undefined)
          }}
          items={Object.fromEntries(providers.map((p) => [p.id, p.name]))}
        >
          <SelectTrigger id="persona-provider" className="w-full">
            <SelectValue placeholder="Add a provider first" />
          </SelectTrigger>
          <SelectContent>
            {providers.map((p) => (
              <SelectItem key={p.id} value={p.id}>
                {p.name}
                <span className="text-muted-foreground">
                  {kindLabels[p.kind]}
                </span>
              </SelectItem>
            ))}
          </SelectContent>
        </Select>
      </Field>
      <Field>
        <FieldLabel>Harness</FieldLabel>
        <ToggleGroup
          aria-label="Harness"
          variant="outline"
          value={harness ? [harness] : []}
          onValueChange={(v) => {
            const next = supported.find((h) => h === v[0])
            if (next) setHarness(next)
          }}
          className="w-full"
        >
          {supported.map((h) => (
            <ToggleGroupItem key={h} value={h} className="flex-1">
              {harnessLabels[h]}
            </ToggleGroupItem>
          ))}
        </ToggleGroup>
        <FieldDescription>
          The harnesses this provider supports.
        </FieldDescription>
      </Field>
      <Field>
        <FieldLabel htmlFor="persona-model">Model</FieldLabel>
        <Input
          id="persona-model"
          className="font-mono"
          autoComplete="off"
          spellCheck={false}
          maxLength={128}
          placeholder={
            provider?.kind === "openai_compatible"
              ? provider.model
              : needsModel
                ? "claude-sonnet-4-5"
                : "The subscription's default"
          }
          value={model}
          onChange={(e) => setModel(e.target.value)}
        />
      </Field>
      <div className="grid gap-4 sm:grid-cols-2">
        <Field>
          <FieldLabel htmlFor="persona-git-name">Git name</FieldLabel>
          <Input
            id="persona-git-name"
            autoComplete="off"
            maxLength={100}
            placeholder={name.trim() || "The persona's name"}
            value={gitName}
            onChange={(e) => setGitName(e.target.value)}
          />
        </Field>
        <Field>
          <FieldLabel htmlFor="persona-git-email">Git email</FieldLabel>
          <Input
            id="persona-git-email"
            autoComplete="off"
            spellCheck={false}
            maxLength={254}
            placeholder={`${slug(name) || "name"}@agents.invalid`}
            value={gitEmail}
            onChange={(e) => setGitEmail(e.target.value)}
          />
        </Field>
      </div>
      <Field data-invalid={soulBytes > maxSoulBytes || undefined}>
        <FieldLabel htmlFor="persona-soul">Soul</FieldLabel>
        <textarea
          id="persona-soul"
          rows={8}
          spellCheck
          placeholder={"# Ada\n\nYou review changes carefully and explain why."}
          className="min-h-32 w-full min-w-0 resize-y rounded-md border border-input bg-input/20 px-2 py-1 font-mono text-sm outline-none placeholder:text-muted-foreground focus-visible:border-ring focus-visible:ring-2 focus-visible:ring-ring/30 aria-invalid:border-destructive aria-invalid:ring-2 aria-invalid:ring-destructive/20 md:text-xs/relaxed dark:bg-input/30"
          aria-invalid={soulBytes > maxSoulBytes}
          value={soul}
          onChange={(e) => setSoul(e.target.value)}
        />
        {soulBytes > maxSoulBytes ? (
          <FieldError>The soul is limited to 16 KiB.</FieldError>
        ) : (
          <FieldDescription>
            Personality and working rules, in markdown.
          </FieldDescription>
        )}
      </Field>
      {formError && <FieldError>{formError}</FieldError>}
      <DialogFooter>
        <Button type="submit" disabled={!valid || saving}>
          {saving && <Spinner />}
          {persona ? "Save" : "Add persona"}
        </Button>
      </DialogFooter>
    </form>
  )
}
