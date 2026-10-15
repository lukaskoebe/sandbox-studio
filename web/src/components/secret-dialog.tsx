import {
  useRef,
  useState,
  type ClipboardEvent,
  type KeyboardEvent,
} from "react"
import { useQueryClient } from "@tanstack/react-query"
import { toast } from "sonner"
import { EyeIcon, EyeSlashIcon, XIcon } from "@phosphor-icons/react"
import { Badge } from "@/components/ui/badge"
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
  $api,
  errorMessage,
  fetchClient,
  type Sandbox,
  type Secret,
} from "@/lib/api/client"

/** Host patterns are separated by commas, spaces or newlines. */
const separators = /[\s,]+/

function splitHosts(text: string): string[] {
  return text.split(separators).filter(Boolean)
}

function unique(list: string[]): string[] {
  return [...new Set(list)]
}

/** What a scope means for the sandboxes that see the secret. */
function scopeHelp(scope: string) {
  if (scope === "environment")
    return "Every sandbox sees it, unless a narrower secret has the same name."
  if (scope.startsWith("teammate:"))
    return "Every sandbox the teammate owns sees it, unless a narrower secret has the same name."
  return "Only this sandbox sees it."
}

export function SecretDialog({
  env,
  secret,
  scope,
  sandboxes,
  open,
  onOpenChange,
}: {
  env: string
  /** The secret to edit; absent for a new one. */
  secret?: Secret
  /**
   * The scope a new secret starts with: "environment", a sandbox ID, or "teammate:" and a
   * teammate ID. Absent means the environment.
   */
  scope?: string
  sandboxes: Sandbox[]
  open: boolean
  onOpenChange: (open: boolean) => void
}) {
  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent>
        {/* The form, including a typed value, exists only while the dialog is open. */}
        <SecretForm
          key={secret?.id ?? `new:${scope ?? ""}`}
          env={env}
          secret={secret}
          initialScope={scope}
          sandboxes={sandboxes}
          onDone={() => onOpenChange(false)}
        />
      </DialogContent>
    </Dialog>
  )
}

function SecretForm({
  env,
  secret,
  initialScope,
  sandboxes,
  onDone,
}: {
  env: string
  secret?: Secret
  initialScope?: string
  sandboxes: Sandbox[]
  onDone: () => void
}) {
  const queryClient = useQueryClient()
  const hostInput = useRef<HTMLInputElement>(null)
  const [name, setName] = useState(secret?.name ?? "")
  // "environment", a sandbox ID, or "teammate:" and a teammate ID, as in the rule dialog.
  const [scope, setScope] = useState(
    secret?.sandboxId ??
      (secret?.teammateId
        ? `teammate:${secret.teammateId}`
        : secret
          ? "environment"
          : (initialScope ?? "environment"))
  )
  const teammates = $api.useQuery("get", "/api/environments/{env}/teammates", {
    params: { path: { env } },
  })
  const teammateList = teammates.data ?? []
  const scopeLabels: Record<string, string> = {
    environment: "Environment",
    ...Object.fromEntries(sandboxes.map((sb) => [sb.id, sb.name])),
    ...Object.fromEntries(
      teammateList.map((p) => [`teammate:${p.id}`, `Teammate ${p.name}`])
    ),
  }
  const [value, setValue] = useState("")
  const [showValue, setShowValue] = useState(false)
  const [hosts, setHosts] = useState<string[]>(secret?.hosts ?? [])
  const [draft, setDraft] = useState("")
  const [note, setNote] = useState(secret?.note ?? "")
  const [serverError, setServerError] = useState<string>()
  const [saving, setSaving] = useState(false)

  // A host still being typed counts as entered when saving.
  const allHosts = unique([...hosts, ...splitHosts(draft)])
  const canSave =
    name.trim() !== "" &&
    allHosts.length > 0 &&
    (secret !== undefined || value !== "")

  const addHosts = (words: string[]) =>
    setHosts((prev) => unique([...prev, ...words]))

  const onHostChange = (text: string) => {
    // The text after the last separator is still being typed.
    const parts = text.split(separators)
    const typing = parts.pop() ?? ""
    if (parts.length) addHosts(parts.filter(Boolean))
    setDraft(typing)
  }
  const confirmDraft = () => {
    if (draft.trim()) addHosts(splitHosts(draft))
    setDraft("")
  }
  const onHostKey = (e: KeyboardEvent<HTMLInputElement>) => {
    if (e.key === "Enter") {
      // Enter confirms the host; it must not submit the form.
      e.preventDefault()
      confirmDraft()
    } else if (e.key === "Backspace" && draft === "" && hosts.length > 0) {
      setHosts((prev) => prev.slice(0, -1))
    }
  }
  const onHostPaste = (e: ClipboardEvent<HTMLInputElement>) => {
    const text = e.clipboardData.getData("text")
    // A single host pastes like any text; a list is split into hosts.
    if (!separators.test(text.trim())) return
    e.preventDefault()
    addHosts(splitHosts(`${draft} ${text}`))
    setDraft("")
  }

  const save = async () => {
    if (!canSave || saving) return
    setSaving(true)
    setServerError(undefined)
    try {
      // Sent directly rather than through a mutation: a mutation cache would keep the value after the dialog closes.
      const result = secret
        ? await fetchClient.PUT("/api/environments/{env}/secrets/{id}", {
            params: { path: { env, id: secret.id } },
            body: {
              hosts: allHosts,
              note: note.trim(),
              value: value || undefined,
            },
          })
        : await fetchClient.POST("/api/environments/{env}/secrets", {
            params: { path: { env } },
            body: {
              name,
              value,
              hosts: allHosts,
              sandboxId:
                scope === "environment" || scope.startsWith("teammate:")
                  ? undefined
                  : scope,
              teammateId: scope.startsWith("teammate:")
                ? scope.slice("teammate:".length)
                : undefined,
              note: note.trim() || undefined,
            },
          })
      if (!result.error) {
        queryClient.invalidateQueries({
          queryKey: ["get", "/api/environments/{env}/secrets"],
        })
        onDone()
        return
      }
      if (result.response.status === 409 || result.response.status === 422) {
        setServerError(errorMessage(result.error))
      } else {
        toast.error("Could not save the secret", {
          description: errorMessage(result.error),
        })
      }
    } catch (err) {
      toast.error("Could not save the secret", {
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
        <DialogTitle>{secret ? "Edit secret" : "Add secret"}</DialogTitle>
        <DialogDescription>
          Sandboxes only see the placeholder. Studio sends the real value over
          HTTPS to these hosts.
        </DialogDescription>
      </DialogHeader>
      <Field>
        <FieldLabel htmlFor="secret-name">Name</FieldLabel>
        {secret ? (
          <Input id="secret-name" className="font-mono" value={name} readOnly />
        ) : (
          <Input
            id="secret-name"
            className="font-mono"
            autoFocus
            autoComplete="off"
            autoCapitalize="characters"
            spellCheck={false}
            value={name}
            onChange={(e) => setName(e.target.value.toUpperCase())}
          />
        )}
        <FieldDescription>
          {secret
            ? "The name can't change."
            : "Letters, digits and _; e.g. OPENAI_API_KEY"}
        </FieldDescription>
      </Field>
      <Field>
        <FieldLabel htmlFor="secret-value">Value</FieldLabel>
        <div className="relative">
          <Input
            id="secret-value"
            type={showValue ? "text" : "password"}
            className="pr-8 font-mono"
            autoComplete="off"
            autoFocus={secret !== undefined}
            data-1p-ignore
            data-lpignore="true"
            spellCheck={false}
            placeholder={
              secret ? "Leave empty to keep the current value" : undefined
            }
            value={value}
            onChange={(e) => setValue(e.target.value)}
          />
          <Button
            type="button"
            variant="ghost"
            size="icon-xs"
            className="absolute top-1/2 right-1 -translate-y-1/2"
            aria-label={showValue ? "Hide value" : "Show value"}
            onClick={() => setShowValue(!showValue)}
          >
            {showValue ? <EyeSlashIcon /> : <EyeIcon />}
          </Button>
        </div>
        <FieldDescription>
          Sealed on arrival and never shown again.
        </FieldDescription>
      </Field>
      <Field>
        <FieldLabel htmlFor="secret-hosts">Hosts</FieldLabel>
        <div
          className="flex min-h-7 flex-wrap items-center gap-1 rounded-md border border-input bg-input/20 px-1 py-0.5 focus-within:border-ring focus-within:ring-2 focus-within:ring-ring/30 dark:bg-input/30"
          onClick={() => hostInput.current?.focus()}
        >
          {hosts.map((host) => (
            <Badge
              key={host}
              variant="secondary"
              className="gap-0.5 pr-0.5 font-mono"
            >
              {host}
              <button
                type="button"
                aria-label={`Remove ${host}`}
                className="rounded-sm p-0.5 hover:bg-foreground/10"
                onClick={() =>
                  setHosts((prev) => prev.filter((h) => h !== host))
                }
              >
                <XIcon className="size-2.5" />
              </button>
            </Badge>
          ))}
          <input
            id="secret-hosts"
            ref={hostInput}
            value={draft}
            autoComplete="off"
            autoCapitalize="none"
            spellCheck={false}
            placeholder={hosts.length ? undefined : "Add a host"}
            className="h-6 min-w-32 flex-1 bg-transparent px-1 font-mono text-xs outline-none placeholder:text-muted-foreground"
            onChange={(e) => onHostChange(e.target.value)}
            onKeyDown={onHostKey}
            onPaste={onHostPaste}
            onBlur={confirmDraft}
          />
        </div>
        <FieldDescription>
          e.g. api.openai.com or *.example.com
        </FieldDescription>
      </Field>
      <Field>
        <FieldLabel htmlFor="secret-scope">Scope</FieldLabel>
        {secret ? (
          <Input id="secret-scope" value={scopeLabels[scope] ?? ""} readOnly />
        ) : (
          <Select
            value={scope}
            onValueChange={(v) => setScope(v ?? "environment")}
            items={scopeLabels}
          >
            <SelectTrigger id="secret-scope" className="w-full">
              <SelectValue />
            </SelectTrigger>
            <SelectContent>
              <SelectItem value="environment">Environment</SelectItem>
              {sandboxes.map((sb) => (
                <SelectItem key={sb.id} value={sb.id}>
                  {sb.name}
                </SelectItem>
              ))}
              {teammateList.map((p) => (
                <SelectItem key={p.id} value={`teammate:${p.id}`}>
                  Teammate {p.name}
                </SelectItem>
              ))}
            </SelectContent>
          </Select>
        )}
        <FieldDescription>
          {secret ? "The scope can't change." : scopeHelp(scope)}
        </FieldDescription>
      </Field>
      <Field>
        <FieldLabel htmlFor="secret-note">Note</FieldLabel>
        <Input
          id="secret-note"
          autoComplete="off"
          maxLength={500}
          placeholder="Optional"
          value={note}
          onChange={(e) => setNote(e.target.value)}
        />
      </Field>
      {serverError && <FieldError>{serverError}</FieldError>}
      <DialogFooter>
        <Button type="submit" disabled={!canSave || saving}>
          {saving && <Spinner />}
          {secret ? "Save" : "Add secret"}
        </Button>
      </DialogFooter>
    </form>
  )
}
