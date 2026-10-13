import { useState, type ReactNode } from "react"
import { useQueryClient } from "@tanstack/react-query"
import { toast } from "sonner"
import {
  GitBranchIcon,
  IdentificationCardIcon,
  RobotIcon,
  ShieldCheckIcon,
  SparkleIcon,
  type Icon,
} from "@phosphor-icons/react"
import { SectionHeader } from "@/components/section-header"
import { Button } from "@/components/ui/button"
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
import { TeammateAvatar } from "@/components/teammate-avatar"
import {
  $api,
  errorMessage,
  errorStatus,
  type Harness,
  type Teammate,
  type Provider,
} from "@/lib/api/client"
import type { Area } from "@/lib/accents"
import {
  approvalScopeLabels,
  approvalScopes,
  harnessLabels,
  isSubscription,
  kindLabels,
} from "@/lib/teammates"
import { cn } from "@/lib/utils"

const maxSoulBytes = 16 * 1024

/** The emoji a teammate can wear, besides the generated avatars. */
const emojiAvatars = [
  "🦊",
  "🐙",
  "🦉",
  "🐢",
  "🦄",
  "🐝",
  "🐳",
  "🦋",
  "🌵",
  "🍄",
  "🚀",
  "🧭",
  "🔭",
  "🧪",
  "🔧",
  "📚",
  "🎨",
  "🎯",
  "🌊",
  "🔥",
  "⚡",
  "🌟",
  "🧩",
  "🪴",
]
const shapeAvatars = [
  "circle",
  "square",
  "diamond",
  "triangle",
  "pentagon",
  "hexagon",
  "star",
  "cross",
]

/** The slug the server derives the default git email from. */
function slug(name: string) {
  return name
    .toLowerCase()
    .replace(/[^a-z0-9]+/g, "-")
    .replace(/^-+|-+$/g, "")
}

export function TeammateForm({
  env,
  teammate,
  providers,
  prefill,
  onSaved,
}: {
  env: string
  /** The teammate to edit; absent for a new one. */
  teammate?: Teammate
  providers: Provider[]
  /** What a new teammate starts with, such as the default one's name. */
  prefill?: { name?: string; role?: string }
  onSaved: () => void
}) {
  const queryClient = useQueryClient()
  const [name, setName] = useState(teammate?.name ?? prefill?.name ?? "")
  const [role, setRole] = useState(teammate?.role ?? prefill?.role ?? "")
  const [providerId, setProviderId] = useState(
    teammate?.providerId ?? providers.at(0)?.id ?? ""
  )
  const provider = providers.find((p) => p.id === providerId)
  const supported = provider?.harnesses ?? []
  const [harness, setHarness] = useState<Harness | undefined>(
    teammate?.harness ?? supported[0]
  )
  const [model, setModel] = useState(teammate?.model ?? "")
  const [gitName, setGitName] = useState(teammate?.gitName ?? "")
  const [gitEmail, setGitEmail] = useState(teammate?.gitEmail ?? "")
  const [soul, setSoul] = useState(teammate?.soul ?? "")
  const [avatar, setAvatar] = useState(teammate?.avatar ?? "")
  const [approvalScope, setApprovalScope] = useState<Teammate["approvalScope"]>(
    teammate?.approvalScope ?? "environment"
  )
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

  const saved = () => {
    queryClient.invalidateQueries({
      queryKey: ["get", "/api/environments/{env}/teammates"],
    })
    onSaved()
  }
  const onFailed = (err: unknown) => {
    const status = errorStatus(err)
    if (status === 409 || status === 422) setFormError(errorMessage(err))
    else
      toast.error("Could not save the teammate", {
        description: errorMessage(err),
      })
  }
  const create = $api.useMutation("post", "/api/environments/{env}/teammates", {
    onSuccess: saved,
    onError: onFailed,
  })
  const update = $api.useMutation(
    "put",
    "/api/environments/{env}/teammates/{id}",
    { onSuccess: saved, onError: onFailed }
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
      avatar: avatar || undefined,
      approvalScope,
    }
    if (teammate)
      update.mutate({ params: { path: { env, id: teammate.id } }, body })
    else create.mutate({ params: { path: { env } }, body })
  }

  return (
    <form
      className="grid gap-6"
      onSubmit={(e) => {
        e.preventDefault()
        submit()
      }}
    >
      <FormSection title="Identity" icon={IdentificationCardIcon} area="chat">
        <div className="grid gap-4 sm:grid-cols-2">
          <Field>
            <FieldLabel htmlFor="teammate-name">Name</FieldLabel>
            <Input
              id="teammate-name"
              autoFocus
              autoComplete="off"
              maxLength={64}
              placeholder="Ada"
              value={name}
              onChange={(e) => setName(e.target.value)}
            />
          </Field>
          <Field>
            <FieldLabel htmlFor="teammate-role">Role</FieldLabel>
            <Input
              id="teammate-role"
              autoComplete="off"
              maxLength={200}
              placeholder="Code reviewer"
              value={role}
              onChange={(e) => setRole(e.target.value)}
            />
          </Field>
        </div>
        <Field>
          <FieldLabel>Avatar</FieldLabel>
          <AvatarPicker name={name} value={avatar} onChange={setAvatar} />
          <FieldDescription>
            An emoji or a generated shape. Without one, the teammate shows its
            initials.
          </FieldDescription>
        </Field>
      </FormSection>
      <FormSection title="Agent" icon={RobotIcon} area="chat">
        <Field>
          <FieldLabel htmlFor="teammate-provider">Provider</FieldLabel>
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
            <SelectTrigger id="teammate-provider" className="w-full">
              <SelectValue
                placeholder={
                  providers.length
                    ? "Choose a provider"
                    : "Add a provider first"
                }
              />
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
          <FieldLabel htmlFor="teammate-model">Model</FieldLabel>
          <Input
            id="teammate-model"
            className="font-mono"
            autoComplete="off"
            spellCheck={false}
            maxLength={128}
            placeholder={
              provider?.kind === "openai_compatible"
                ? provider.model
                : provider?.kind === "openai_api"
                  ? "gpt-sol-6.1"
                  : needsModel
                    ? "claude-sonnet-5-5"
                    : "The subscription's default"
            }
            value={model}
            onChange={(e) => setModel(e.target.value)}
          />
        </Field>
      </FormSection>
      <FormSection title="Git identity" icon={GitBranchIcon} area="git">
        <div className="grid gap-4 sm:grid-cols-2">
          <Field>
            <FieldLabel htmlFor="teammate-git-name">Git name</FieldLabel>
            <Input
              id="teammate-git-name"
              autoComplete="off"
              maxLength={100}
              placeholder={name.trim() || "The teammate's name"}
              value={gitName}
              onChange={(e) => setGitName(e.target.value)}
            />
          </Field>
          <Field>
            <FieldLabel htmlFor="teammate-git-email">Git email</FieldLabel>
            <Input
              id="teammate-git-email"
              className="font-mono"
              autoComplete="off"
              spellCheck={false}
              maxLength={254}
              placeholder={`${slug(name) || "name"}@agents.invalid`}
              value={gitEmail}
              onChange={(e) => setGitEmail(e.target.value)}
            />
          </Field>
        </div>
      </FormSection>
      <FormSection title="Approvals" icon={ShieldCheckIcon} area="network">
        <Field>
          <FieldLabel>New rules from approvals apply to</FieldLabel>
          <ToggleGroup
            aria-label="New rules from approvals apply to"
            variant="outline"
            value={[approvalScope]}
            onValueChange={(v) => {
              const next = approvalScopes.find((s) => s === v[0])
              if (next) setApprovalScope(next)
            }}
            className="w-full"
          >
            {approvalScopes.map((s) => (
              <ToggleGroupItem key={s} value={s} className="flex-1">
                {approvalScopeLabels[s]}
              </ToggleGroupItem>
            ))}
          </ToggleGroup>
          <FieldDescription>
            Preselected on the approval cards for this teammate's sandboxes. The
            card still lets you pick another scope.
          </FieldDescription>
        </Field>
      </FormSection>
      <FormSection title="Soul" icon={SparkleIcon} area="chat">
        <Field data-invalid={soulBytes > maxSoulBytes || undefined}>
          {/* The section's title is the visible label; the textarea keeps its name. */}
          <FieldLabel htmlFor="teammate-soul" className="sr-only">
            Soul
          </FieldLabel>
          <textarea
            id="teammate-soul"
            rows={8}
            spellCheck
            placeholder={
              "# Ada\n\nYou review changes carefully and explain why."
            }
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
      </FormSection>
      {formError && <FieldError>{formError}</FieldError>}
      <div className="flex justify-end">
        <Button type="submit" disabled={!valid || saving}>
          {saving && <Spinner />}
          {teammate ? "Save" : "Add a teammate"}
        </Button>
      </div>
    </form>
  )
}

/** The avatars to pick from: the initials, the generated shapes, then the emoji. */
function AvatarPicker({
  name,
  value,
  onChange,
}: {
  name: string
  value: string
  onChange: (avatar: string) => void
}) {
  const choices = [
    "",
    ...shapeAvatars.map((shape) => `shape:${shape}`),
    ...emojiAvatars,
  ]
  return (
    <div role="group" aria-label="Avatar" className="flex flex-wrap gap-1">
      {choices.map((choice) => (
        <button
          key={choice || "initials"}
          type="button"
          aria-pressed={value === choice}
          aria-label={choice || "Initials"}
          title={choice || "Initials"}
          onClick={() => onChange(choice)}
          className={cn(
            "rounded-full p-0.5 outline-none focus-visible:ring-2 focus-visible:ring-ring",
            value === choice && "ring-2 ring-primary"
          )}
        >
          <TeammateAvatar
            name={name.trim() || "?"}
            avatar={choice}
            className="size-7 text-[0.6875rem]"
          />
        </button>
      ))}
    </div>
  )
}

/** A group of fields under a section title, the same sections as the teammate's overview. */
function FormSection({
  title,
  icon,
  area,
  children,
}: {
  title: string
  icon: Icon
  area?: Area
  children: ReactNode
}) {
  return (
    <section className="grid gap-4">
      <SectionHeader title={title} icon={icon} area={area} />
      {children}
    </section>
  )
}
