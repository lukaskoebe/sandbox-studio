import { useState } from "react"
import { useQueryClient } from "@tanstack/react-query"
import { DesktopIcon } from "@phosphor-icons/react"
import { Button } from "@/components/ui/button"
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog"
import { FieldError } from "@/components/ui/field"
import { Spinner } from "@/components/ui/spinner"
import {
  $api,
  errorMessage,
  fetchClient,
  type Provider,
  type ProviderKind,
} from "@/lib/api/client"

/** What a provider found on this computer becomes: its kind and the name it starts with. */
export type HostOffer = { kind: ProviderKind; name: string }

// The names imported opencode keys start with, by opencode's provider name.
const opencodeNames: Record<string, string> = {
  anthropic: "Anthropic",
  openai: "OpenAI",
}

/**
 * The CLIs signed in on the computer Studio runs on, as offers of the providers that match
 * them (docs/host-logins.md). Only the files' existence is known here: a subscription offer
 * opens the provider dialog, where Studio signs in on its own, and opencode's keys are read
 * only when the user opens their import.
 */
export function HostLoginOffers({
  env,
  providers,
  onUse,
}: {
  env: string
  providers: Provider[]
  onUse: (offer: HostOffer) => void
}) {
  const found = $api.useQuery("get", "/api/system/host-logins").data
  const [importing, setImporting] = useState(false)
  const has = (kind: ProviderKind) => providers.some((p) => p.kind === kind)
  const rows = [
    found?.claude &&
      !has("claude_subscription") && {
        key: "claude",
        title: "Claude Code",
        text: "is set up on this computer. Use your Claude plan: Studio makes a token of its own, and this computer stays signed in.",
        action: "Use Claude subscription",
        onClick: () => onUse({ kind: "claude_subscription", name: "Claude" }),
      },
    found?.codex &&
      !has("chatgpt_subscription") && {
        key: "codex",
        title: "Codex",
        text: "is signed in on this computer. Use your ChatGPT plan: Studio signs in on its own, and this computer stays signed in.",
        action: "Use ChatGPT subscription",
        onClick: () => onUse({ kind: "chatgpt_subscription", name: "ChatGPT" }),
      },
    found?.opencode && {
      key: "opencode",
      title: "opencode",
      text: "has API keys on this computer. Studio can import them one by one; opencode keeps its own.",
      action: "Import keys…",
      onClick: () => setImporting(true),
    },
  ].filter((r) => !!r)
  if (!rows.length) return null
  return (
    <section aria-label="On this computer" className="grid gap-2">
      <h3 className="flex items-center gap-1.5 text-xs font-medium">
        <DesktopIcon className="size-3.5" />
        On this computer
      </h3>
      <ul className="divide-y rounded-md ring-1 ring-foreground/10">
        {rows.map((r) => (
          <li
            key={r.key}
            className="flex flex-wrap items-center gap-x-3 gap-y-2 p-2.5 text-xs"
          >
            <p className="min-w-48 flex-1 text-muted-foreground">
              <span className="font-medium text-foreground">{r.title}</span>{" "}
              {r.text}
            </p>
            <Button size="xs" variant="outline" onClick={r.onClick}>
              {r.action}
            </Button>
          </li>
        ))}
      </ul>
      <OpencodeImportDialog
        env={env}
        providers={providers}
        open={importing}
        onOpenChange={setImporting}
        onUse={(offer) => {
          setImporting(false)
          onUse(offer)
        }}
      />
    </section>
  )
}

/** The account the computer's Codex login belongs to, once the user picked ChatGPT. */
export function HostCodexAccount() {
  const found = $api.useQuery("get", "/api/system/host-logins").data
  const account = $api.useQuery(
    "get",
    "/api/system/host-logins/codex",
    undefined,
    { enabled: !!found?.codex, retry: false }
  ).data
  if (!account?.email) return null
  return (
    <p className="rounded-md bg-muted px-3 py-2 text-xs/relaxed text-muted-foreground">
      This computer's Codex is signed in as{" "}
      <span className="font-medium text-foreground">{account.email}</span>
      {account.plan && <> ({account.plan})</>}. Studio doesn't copy that login:
      it would sign this computer out. Add the provider and sign in once more
      with the same account.
    </p>
  )
}

function OpencodeImportDialog({
  env,
  providers,
  open,
  onOpenChange,
  onUse,
}: {
  env: string
  providers: Provider[]
  open: boolean
  onOpenChange: (open: boolean) => void
  onUse: (offer: HostOffer) => void
}) {
  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent className="sm:max-w-lg">
        <DialogHeader>
          <DialogTitle>Import keys from opencode</DialogTitle>
          <DialogDescription>
            Studio reads opencode's stored credentials on this computer. An API
            key you import is sealed in Studio's vault like a typed one;
            opencode keeps its own.
          </DialogDescription>
        </DialogHeader>
        {/* The keys are read only while the dialog is open. */}
        {open && <OpencodeKeys env={env} providers={providers} onUse={onUse} />}
        <DialogFooter>
          <Button variant="outline" onClick={() => onOpenChange(false)}>
            Done
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  )
}

function OpencodeKeys({
  env,
  providers,
  onUse,
}: {
  env: string
  providers: Provider[]
  onUse: (offer: HostOffer) => void
}) {
  const keys = $api.useQuery(
    "get",
    "/api/system/host-logins/opencode",
    undefined,
    { retry: false, gcTime: 0 }
  )
  const queryClient = useQueryClient()
  const [busy, setBusy] = useState<string>()
  const [imported, setImported] = useState<string[]>([])
  const [error, setError] = useState<string>()

  if (keys.isPending) return <Spinner className="mx-auto block" />
  if (keys.error) return <FieldError>{errorMessage(keys.error)}</FieldError>
  const list = keys.data ?? []
  if (!list.length)
    return (
      <p className="text-xs text-muted-foreground">
        opencode stores no credentials.
      </p>
    )

  const nameFor = (provider: string) => {
    const base = opencodeNames[provider] ?? provider
    return providers.some((p) => p.name === base) ? `${base} (opencode)` : base
  }
  const importKey = async (provider: string) => {
    setBusy(provider)
    setError(undefined)
    try {
      const result = await fetchClient.POST(
        "/api/environments/{env}/providers/import-opencode",
        {
          params: { path: { env } },
          body: { provider, name: nameFor(provider) },
        }
      )
      if (result.error) {
        setError(errorMessage(result.error))
        return
      }
      setImported((done) => [...done, provider])
      queryClient.invalidateQueries({
        queryKey: ["get", "/api/environments/{env}/providers"],
      })
      queryClient.invalidateQueries({
        queryKey: ["get", "/api/environments/{env}/secrets"],
      })
      queryClient.invalidateQueries({
        queryKey: ["get", "/api/environments/{env}/teammates"],
      })
      queryClient.invalidateQueries({ queryKey: ["get", "/api/setup"] })
    } catch (err) {
      setError(errorMessage(err))
    } finally {
      setBusy(undefined)
    }
  }

  return (
    <div className="grid gap-2">
      <ul className="divide-y rounded-md ring-1 ring-foreground/10">
        {list.map((k) => {
          const oauthKind: ProviderKind | undefined =
            k.type === "oauth"
              ? k.provider === "anthropic"
                ? "claude_subscription"
                : k.provider === "openai"
                  ? "chatgpt_subscription"
                  : undefined
              : undefined
          return (
            <li
              key={k.provider}
              className="flex flex-wrap items-center gap-x-3 gap-y-2 p-2.5 text-xs"
            >
              <div className="min-w-40 flex-1">
                <div className="font-medium">
                  {opencodeNames[k.provider] ?? k.provider}
                </div>
                <div className="text-muted-foreground">
                  {k.kind
                    ? "API key"
                    : oauthKind
                      ? "A subscription sign-in. Studio doesn't copy it; it signs in on its own."
                      : k.type === "api"
                        ? "API key of a provider Studio has no kind for. Add it as an OpenAI-compatible endpoint."
                        : "Studio can't import this kind of credential."}
                </div>
              </div>
              {k.kind ? (
                imported.includes(k.provider) ? (
                  <span className="text-muted-foreground">Imported</span>
                ) : (
                  <Button
                    size="xs"
                    variant="outline"
                    disabled={!!busy}
                    onClick={() => importKey(k.provider)}
                  >
                    {busy === k.provider && <Spinner />}
                    Import
                  </Button>
                )
              ) : oauthKind && !providers.some((p) => p.kind === oauthKind) ? (
                <Button
                  size="xs"
                  variant="outline"
                  onClick={() =>
                    onUse({
                      kind: oauthKind,
                      name:
                        oauthKind === "claude_subscription"
                          ? "Claude"
                          : "ChatGPT",
                    })
                  }
                >
                  Sign in
                </Button>
              ) : null}
            </li>
          )
        })}
      </ul>
      {error && <FieldError>{error}</FieldError>}
    </div>
  )
}
