import { useState } from "react"
import { useQueryClient } from "@tanstack/react-query"
import { toast } from "sonner"
import { EyeIcon, EyeSlashIcon } from "@phosphor-icons/react"
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
import {
  errorMessage,
  fetchClient,
  type Provider,
  type ProviderKind,
} from "@/lib/api/client"
import { isSubscription, kindLabels, providerKinds } from "@/lib/personas"

export function ProviderDialog({
  env,
  provider,
  open,
  onOpenChange,
}: {
  env: string
  /** The provider to edit; absent for a new one. */
  provider?: Provider
  open: boolean
  onOpenChange: (open: boolean) => void
}) {
  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent>
        {/* The form, including a typed key, exists only while the dialog is open. */}
        <ProviderForm
          key={provider?.id ?? "new"}
          env={env}
          provider={provider}
          onDone={() => onOpenChange(false)}
        />
      </DialogContent>
    </Dialog>
  )
}

function ProviderForm({
  env,
  provider,
  onDone,
}: {
  env: string
  provider?: Provider
  onDone: () => void
}) {
  const queryClient = useQueryClient()
  const [name, setName] = useState(provider?.name ?? "")
  const [kind, setKind] = useState<ProviderKind>(
    provider?.kind ?? "anthropic_api"
  )
  const [apiKey, setApiKey] = useState("")
  const [showKey, setShowKey] = useState(false)
  const [baseUrl, setBaseUrl] = useState(provider?.baseUrl ?? "")
  const [model, setModel] = useState(provider?.model ?? "")
  const [serverError, setServerError] = useState<string>()
  const [saving, setSaving] = useState(false)

  const subscription = isSubscription(kind)
  const compatible = kind === "openai_compatible"
  const canSave = provider
    ? !subscription
    : name.trim() !== "" &&
      (subscription || apiKey !== "") &&
      (!compatible || (baseUrl.trim() !== "" && model.trim() !== ""))

  const save = async () => {
    if (!canSave || saving) return
    setSaving(true)
    setServerError(undefined)
    try {
      // Sent directly rather than through a mutation: a mutation cache would keep the key after the dialog closes.
      const result = provider
        ? await fetchClient.PUT("/api/environments/{env}/providers/{id}", {
            params: { path: { env, id: provider.id } },
            body: {
              apiKey: apiKey || undefined,
              baseUrl: compatible ? baseUrl.trim() : undefined,
              model: compatible ? model.trim() : undefined,
            },
          })
        : await fetchClient.POST("/api/environments/{env}/providers", {
            params: { path: { env } },
            body: {
              name: name.trim(),
              kind,
              apiKey: subscription ? undefined : apiKey,
              baseUrl: compatible ? baseUrl.trim() : undefined,
              model: compatible ? model.trim() : undefined,
            },
          })
      if (!result.error) {
        queryClient.invalidateQueries({
          queryKey: ["get", "/api/environments/{env}/providers"],
        })
        queryClient.invalidateQueries({
          queryKey: ["get", "/api/environments/{env}/secrets"],
        })
        onDone()
        return
      }
      if (result.response.status === 409 || result.response.status === 422) {
        setServerError(errorMessage(result.error))
      } else {
        toast.error("Could not save the provider", {
          description: errorMessage(result.error),
        })
      }
    } catch (err) {
      toast.error("Could not save the provider", {
        description: errorMessage(err),
      })
    } finally {
      setSaving(false)
    }
  }

  return (
    <form
      className="contents"
      onSubmit={(e) => {
        e.preventDefault()
        save()
      }}
    >
      <DialogHeader>
        <DialogTitle>{provider ? "Edit provider" : "Add provider"}</DialogTitle>
        <DialogDescription>
          How personas reach a model. API keys are stored as secrets, so
          sandboxes only ever see a placeholder.
        </DialogDescription>
      </DialogHeader>
      <Field>
        <FieldLabel htmlFor="provider-name">Name</FieldLabel>
        <Input
          id="provider-name"
          autoFocus={!provider}
          autoComplete="off"
          maxLength={64}
          readOnly={!!provider}
          placeholder="Anthropic"
          value={name}
          onChange={(e) => setName(e.target.value)}
        />
        {provider && (
          <FieldDescription>The name and kind can't change.</FieldDescription>
        )}
      </Field>
      <Field>
        <FieldLabel htmlFor="provider-kind">Kind</FieldLabel>
        <Select
          value={kind}
          disabled={!!provider}
          onValueChange={(v) => {
            const next = providerKinds.find((k) => k === v)
            if (next) {
              setKind(next)
              setServerError(undefined)
            }
          }}
          items={kindLabels}
        >
          <SelectTrigger id="provider-kind" className="w-full">
            <SelectValue />
          </SelectTrigger>
          <SelectContent>
            {providerKinds.map((k) => (
              <SelectItem key={k} value={k}>
                {kindLabels[k]}
              </SelectItem>
            ))}
          </SelectContent>
        </Select>
      </Field>
      {subscription ? (
        <p className="rounded-md bg-muted px-3 py-2 text-xs/relaxed text-muted-foreground">
          Login flow coming soon. Until then, personas can use this provider but
          can't sign in with it.
        </p>
      ) : (
        <>
          {compatible && (
            <>
              <Field>
                <FieldLabel htmlFor="provider-url">Base URL</FieldLabel>
                <Input
                  id="provider-url"
                  className="font-mono"
                  autoComplete="off"
                  spellCheck={false}
                  maxLength={2048}
                  placeholder="https://llm.example.com/v1"
                  value={baseUrl}
                  onChange={(e) => setBaseUrl(e.target.value)}
                />
                <FieldDescription>
                  An https:// URL on a public host. The key is only sent there.
                </FieldDescription>
              </Field>
              <Field>
                <FieldLabel htmlFor="provider-model">Model</FieldLabel>
                <Input
                  id="provider-model"
                  className="font-mono"
                  autoComplete="off"
                  spellCheck={false}
                  maxLength={128}
                  placeholder="qwen3-coder"
                  value={model}
                  onChange={(e) => setModel(e.target.value)}
                />
              </Field>
            </>
          )}
          <Field>
            <FieldLabel htmlFor="provider-key">API key</FieldLabel>
            <div className="relative">
              <Input
                id="provider-key"
                type={showKey ? "text" : "password"}
                className="pr-8 font-mono"
                autoComplete="off"
                autoFocus={!!provider}
                data-1p-ignore
                data-lpignore="true"
                spellCheck={false}
                placeholder={
                  provider ? "Leave empty to keep the current key" : undefined
                }
                value={apiKey}
                onChange={(e) => setApiKey(e.target.value)}
              />
              <Button
                type="button"
                variant="ghost"
                size="icon-xs"
                className="absolute top-1/2 right-1 -translate-y-1/2"
                aria-label={showKey ? "Hide key" : "Show key"}
                onClick={() => setShowKey(!showKey)}
              >
                {showKey ? <EyeSlashIcon /> : <EyeIcon />}
              </Button>
            </div>
            <FieldDescription>
              Sealed on arrival and never shown again.
            </FieldDescription>
          </Field>
        </>
      )}
      {serverError && <FieldError>{serverError}</FieldError>}
      <DialogFooter>
        <Button type="submit" disabled={!canSave || saving}>
          {saving && <Spinner />}
          {provider ? "Save" : "Add provider"}
        </Button>
      </DialogFooter>
    </form>
  )
}
