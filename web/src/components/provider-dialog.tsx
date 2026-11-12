import { useState } from "react"
import { useQueryClient } from "@tanstack/react-query"
import { toast } from "sonner"
import { EyeIcon, EyeSlashIcon } from "@phosphor-icons/react"
import { Button } from "@/components/ui/button"
import { Checkbox } from "@/components/ui/checkbox"
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
  FieldContent,
  FieldDescription,
  FieldError,
  FieldLabel,
} from "@/components/ui/field"
import { Input } from "@/components/ui/input"
import { ModelInput } from "@/components/model-input"
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select"
import { Spinner } from "@/components/ui/spinner"
import { HostCodexAccount, type HostOffer } from "@/components/host-logins"
import { ProviderLogin } from "@/components/provider-login"
import {
  errorMessage,
  fetchClient,
  type Provider,
  type ProviderKind,
} from "@/lib/api/client"
import {
  endpointPlace,
  useProviderModels,
  type EndpointPlace,
} from "@/lib/provider-models"
import { isSubscription, kindLabels, providerKinds } from "@/lib/teammates"

// What each kind asks for, so an API key and a subscription sign-in don't look alike.
const kindHints: Record<ProviderKind, string> = {
  anthropic_api:
    "Pay per use with a key from console.anthropic.com. Paste the key below; there is no sign-in.",
  openai_api:
    "Pay per use with a key from platform.openai.com. Paste the key below; there is no sign-in.",
  openai_compatible:
    "Any server that speaks the OpenAI API, with its URL and model, and a key if it takes one: a hosted service, or one on this computer or the local network.",
  claude_subscription:
    "Use your Claude Pro or Max plan. You sign in with your Claude account in the browser; there is no key to paste.",
  chatgpt_subscription:
    "Use your ChatGPT Plus or Pro plan. You sign in with your ChatGPT account in the browser; there is no key to paste.",
}

// The base URL's hint, by where its server is.
const endpointHints: Record<EndpointPlace | "unknown", string> = {
  unknown:
    "https:// for a hosted service; http:// for a server on this computer, such as http://localhost:11434/v1, or on the local network.",
  public: "A hosted service. The key is only sent there.",
  computer:
    "A server on this computer. Saving opens its port to this environment's sandboxes; each connection still asks for approval until a rule allows it.",
  network:
    "A server on the local network. Saving adds it to the environment's local network list; each connection still asks for approval until a rule allows it.",
}

export function ProviderDialog({
  env,
  provider,
  offer,
  open,
  onOpenChange,
}: {
  env: string
  /** The provider to edit; absent for a new one. */
  provider?: Provider
  /** The kind and name a new provider starts with, from a login on this computer. */
  offer?: HostOffer
  open: boolean
  onOpenChange: (open: boolean) => void
}) {
  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent className="sm:max-w-xl lg:max-w-2xl">
        {/* The form, including a typed key, exists only while the dialog is open. */}
        <ProviderForm
          key={provider?.id ?? offer?.kind ?? "new"}
          env={env}
          provider={provider}
          offer={offer}
          onDone={() => onOpenChange(false)}
        />
      </DialogContent>
    </Dialog>
  )
}

function ProviderForm({
  env,
  provider,
  offer,
  onDone,
}: {
  env: string
  provider?: Provider
  offer?: HostOffer
  onDone: () => void
}) {
  const queryClient = useQueryClient()
  const [name, setName] = useState(provider?.name ?? offer?.name ?? "")
  const [kind, setKind] = useState<ProviderKind>(
    provider?.kind ?? offer?.kind ?? "anthropic_api"
  )
  const [apiKey, setApiKey] = useState("")
  const [removeKey, setRemoveKey] = useState(false)
  const [showKey, setShowKey] = useState(false)
  const [baseUrl, setBaseUrl] = useState(provider?.baseUrl ?? "")
  const [model, setModel] = useState(provider?.model ?? "")
  const [images, setImages] = useState(provider?.images ?? false)
  const [serverError, setServerError] = useState<string>()
  const [saving, setSaving] = useState(false)
  // A subscription added in this dialog: its sign-in follows the creation, in the same dialog.
  const [added, setAdded] = useState<Provider>()
  const current = provider ?? added

  const subscription = isSubscription(kind)
  const compatible = kind === "openai_compatible"
  // A provider that was imported without its key needs one before it can be used.
  const keyMissing = provider?.state === "key_required"
  const place = compatible ? endpointPlace(baseUrl) : undefined
  // A key would cross the local network in plain http://, so a server there takes none.
  const keyless = place === "network"
  const hasKey = !!provider?.secretId
  const dropKey = compatible && hasKey && (removeKey || keyless)
  // The endpoint's models, as suggestions. A saved provider's stored key only lists at its own URL.
  const suggestions = useProviderModels({
    env,
    baseUrl: compatible ? baseUrl : "",
    apiKey: keyless ? undefined : apiKey,
    providerId:
      provider && baseUrl.trim() === provider.baseUrl ? provider.id : undefined,
  })
  const canSave = provider
    ? !subscription && (!keyMissing || apiKey !== "")
    : name.trim() !== "" &&
      (subscription || compatible || apiKey !== "") &&
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
              apiKey: (!keyless && !dropKey && apiKey) || undefined,
              removeKey: dropKey || undefined,
              baseUrl: compatible ? baseUrl.trim() : undefined,
              model: compatible ? model.trim() : undefined,
              images: compatible ? images : undefined,
            },
          })
        : await fetchClient.POST("/api/environments/{env}/providers", {
            params: { path: { env } },
            body: {
              name: name.trim(),
              kind,
              apiKey: subscription || keyless ? undefined : apiKey || undefined,
              baseUrl: compatible ? baseUrl.trim() : undefined,
              model: compatible ? model.trim() : undefined,
              images: compatible ? images : undefined,
            },
          })
      if (!result.error) {
        queryClient.invalidateQueries({
          queryKey: ["get", "/api/environments/{env}/providers"],
        })
        queryClient.invalidateQueries({
          queryKey: ["get", "/api/environments/{env}/secrets"],
        })
        // The first provider brings the default teammate, and both are steps of the setup.
        queryClient.invalidateQueries({
          queryKey: ["get", "/api/environments/{env}/teammates"],
        })
        queryClient.invalidateQueries({ queryKey: ["get", "/api/setup"] })
        if (!provider && subscription) {
          // A subscription has no key to type: its sign-in is the next step.
          setAdded(result.data)
          return
        }
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
        <DialogTitle>
          {provider && subscription
            ? `Sign in to ${provider.name}`
            : provider
              ? "Edit provider"
              : "Add provider"}
        </DialogTitle>
        <DialogDescription>
          How teammates reach a model. API keys and subscription logins are
          stored as secrets, so sandboxes only ever see a placeholder.
        </DialogDescription>
      </DialogHeader>
      <div className="grid items-start gap-4 sm:grid-cols-2">
        <Field>
          <FieldLabel htmlFor="provider-name">Name</FieldLabel>
          <Input
            id="provider-name"
            autoFocus={!provider}
            autoComplete="off"
            maxLength={64}
            readOnly={!!current}
            placeholder="Anthropic"
            value={name}
            onChange={(e) => setName(e.target.value)}
          />
          {current && (
            <FieldDescription>The name and kind can't change.</FieldDescription>
          )}
        </Field>
        <Field>
          <FieldLabel htmlFor="provider-kind">Kind</FieldLabel>
          <Select
            value={kind}
            disabled={!!current}
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
          {!current && <FieldDescription>{kindHints[kind]}</FieldDescription>}
        </Field>
      </div>
      {subscription ? (
        current ? (
          <ProviderLogin env={env} provider={current} />
        ) : (
          <>
            {kind === "chatgpt_subscription" && <HostCodexAccount />}
            <p className="rounded-md bg-muted px-3 py-2 text-xs/relaxed text-muted-foreground">
              Add the provider, then sign in with your subscription in your
              browser. Nothing to paste yet.
            </p>
          </>
        )
      ) : (
        <>
          {compatible && (
            <div className="grid items-start gap-4 sm:grid-cols-2">
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
                  {endpointHints[place ?? "unknown"]}
                </FieldDescription>
              </Field>
              <Field>
                <FieldLabel htmlFor="provider-model">Model</FieldLabel>
                <ModelInput
                  id="provider-model"
                  placeholder="qwen3-coder"
                  suggestions={suggestions.models}
                  value={model}
                  onChange={setModel}
                />
                <ModelSuggestions {...suggestions} />
              </Field>
            </div>
          )}
          {compatible && (
            <Field orientation="horizontal">
              <Checkbox
                id="provider-images"
                checked={images}
                onCheckedChange={setImages}
              />
              <FieldContent>
                <FieldLabel htmlFor="provider-images">
                  The model can see images
                </FieldLabel>
                <FieldDescription>
                  Screenshots, such as those of browser notes, then go to the
                  model. Many local models read text only: leave it off for
                  them, and notes go as text alone.
                </FieldDescription>
              </FieldContent>
            </Field>
          )}
          {keyless ? (
            <p className="rounded-md bg-muted px-3 py-2 text-xs/relaxed text-muted-foreground">
              No key: a server on the local network is reached over plain
              http://, which would carry it across the network
              {hasKey && ", so saving removes the stored key"}. Serve it over
              https:// on a public name to use a key.
            </p>
          ) : (
            <Field>
              <FieldLabel htmlFor="provider-key">
                {compatible ? "API key (optional)" : "API key"}
              </FieldLabel>
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
                  disabled={dropKey}
                  placeholder={
                    keyMissing
                      ? "Paste the API key"
                      : provider && hasKey
                        ? "Leave empty to keep the current key"
                        : compatible
                          ? "Leave empty if the server takes none"
                          : undefined
                  }
                  value={dropKey ? "" : apiKey}
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
              {compatible && hasKey && (
                <Field orientation="horizontal">
                  <Checkbox
                    id="provider-remove-key"
                    checked={removeKey}
                    onCheckedChange={setRemoveKey}
                  />
                  <FieldLabel htmlFor="provider-remove-key">
                    Remove the stored key; the server takes none
                  </FieldLabel>
                </Field>
              )}
            </Field>
          )}
        </>
      )}
      {serverError && <FieldError>{serverError}</FieldError>}
      <DialogFooter>
        {subscription && current ? (
          <Button type="button" onClick={onDone}>
            Done
          </Button>
        ) : (
          <Button type="submit" disabled={!canSave || saving}>
            {saving && <Spinner />}
            {provider ? "Save" : "Add provider"}
          </Button>
        )}
      </DialogFooter>
    </form>
  )
}

/**
 * A line saying where the models an endpoint lists stand, under a model input that suggests
 * them. Any other model can still be typed.
 */
export function ModelSuggestions({
  models,
  error,
  loading,
}: {
  models: string[]
  error?: string
  loading: boolean
}) {
  return (
    <>
      {loading ? (
        <FieldDescription>Asking the endpoint for its models…</FieldDescription>
      ) : error ? (
        <FieldDescription>Could not list the models: {error}</FieldDescription>
      ) : models.length > 0 ? (
        <FieldDescription>
          {models.length === 1
            ? "The endpoint lists 1 model"
            : `The endpoint lists ${models.length} models`}
          ; pick one or type another.
        </FieldDescription>
      ) : null}
    </>
  )
}
