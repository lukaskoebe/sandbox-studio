import { createContext, useContext, useState, type ReactNode } from "react"
import { createPortal } from "react-dom"
import { useQueryClient } from "@tanstack/react-query"
import { toast } from "sonner"
import {
  GitBranchIcon,
  IdentificationCardIcon,
  PlusIcon,
  RobotIcon,
  ShieldCheckIcon,
  SignInIcon,
  SparkleIcon,
  type Icon,
} from "@phosphor-icons/react"
import { scopeChoice } from "@/components/approval-decision"
import { DisclosureRow } from "@/components/disclosure-row"
import { useSavePart } from "@/components/auto-save"
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
import { Switch } from "@/components/ui/switch"
import { ToggleGroup, ToggleGroupItem } from "@/components/ui/toggle-group"
import { ModelInput, knownModels } from "@/components/model-input"
import { ModelsInput } from "@/components/models-input"
import { ModelSuggestions, ProviderDialog } from "@/components/provider-dialog"
import { TeammateAvatar } from "@/components/teammate-avatar"
import { spriteFor, spriteLabel, spriteNames } from "@/lib/sprites"
import {
  $api,
  errorMessage,
  errorStatus,
  type Harness,
  type Teammate,
  type Provider,
} from "@/lib/api/client"
import type { Area } from "@/lib/accents"
import { useProviderModels } from "@/lib/provider-models"
import {
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

/**
 * What the chosen provider needs before its agents can start, with the button that opens its
 * sign-in or key; a form without providers offers to add one.
 */
function ProviderNeeds({
  provider,
  none,
  onOpen,
}: {
  provider?: Provider
  none: boolean
  onOpen: () => void
}) {
  let text: string
  let action: string
  if (none) {
    text = "A teammate's model runs on a provider's account."
    action = "Add provider…"
  } else if (provider?.state === "login_required") {
    text = `${provider.name} needs a sign-in before its agents start.`
    action = "Sign in…"
  } else if (provider?.state === "expired") {
    text = `${provider.name}'s sign-in has expired.`
    action = "Sign in again…"
  } else if (provider?.state === "key_required") {
    text = `${provider.name} needs its API key before its agents start.`
    action = "Add key…"
  } else {
    return null
  }
  return (
    <FieldDescription className="flex flex-wrap items-center gap-x-2 gap-y-1">
      <span>{text}</span>
      <Button type="button" size="xs" variant="outline" onClick={onOpen}>
        {none ? <PlusIcon /> : <SignInIcon />}
        {action}
      </Button>
    </FieldDescription>
  )
}

export function TeammateForm({
  env,
  teammate,
  providers,
  onSaved,
  sections,
  approvalsIn,
}: {
  env: string
  /** The teammate to edit; absent for a new one. */
  teammate?: Teammate
  providers: Provider[]
  onSaved: () => void
  /**
   * Shows each section as a row that opens to its fields, with the open one named here. In an
   * AutoSave the changes save as they are made; otherwise the open row has a Save. Without it
   * the sections follow each other with one Save.
   */
  sections?: { open?: string; onOpen: (id: string | undefined) => void }
  /**
   * Where the approvals row goes instead of after Commits as, such as the settings' Network
   * group; null until that place is in the page.
   */
  approvalsIn?: HTMLElement | null
}) {
  const queryClient = useQueryClient()
  const [name, setName] = useState(teammate?.name ?? "")
  const [role, setRole] = useState(teammate?.role ?? "")
  const [providerId, setProviderId] = useState(
    teammate?.providerId ?? providers.at(0)?.id ?? ""
  )
  const provider = providers.find((p) => p.id === providerId)
  // The provider dialog, opened from here to sign in to the chosen provider, add its key or add
  // a first provider, without leaving the form.
  const [providerDialog, setProviderDialog] = useState(false)
  // An OpenAI-compatible endpoint's own models, listed with the provider's stored key.
  const compatible =
    provider?.kind === "openai_compatible" && provider.state === "ready"
  const suggestions = useProviderModels({
    env,
    baseUrl: compatible ? (provider.baseUrl ?? "") : "",
    providerId: compatible ? provider.id : undefined,
  })
  const supported = provider?.harnesses ?? []
  const [harness, setHarness] = useState<Harness | undefined>(
    teammate?.harness ?? supported[0]
  )
  const [model, setModel] = useState(teammate?.model ?? "")
  const modelSuggestions = compatible
    ? suggestions.models
    : knownModels(provider?.kind, harness)
  const [models, setModels] = useState<string[]>(teammate?.models ?? [])
  const [gitName, setGitName] = useState(teammate?.gitName ?? "")
  const [gitEmail, setGitEmail] = useState(teammate?.gitEmail ?? "")
  const [useHostGit, setUseHostGit] = useState(
    teammate?.useHostGitIdentity ?? false
  )
  // The identity "Use my git identity" commits with: the host's git config --global.
  const hostGit = $api.useQuery("get", "/api/system/git-identity").data
  const hostGitSet = !!hostGit?.name && !!hostGit.email
  const [soul, setSoul] = useState(teammate?.soul ?? "")
  const [avatar, setAvatar] = useState(teammate?.avatar ?? "")
  const [approvalScope, setApprovalScope] = useState<Teammate["approvalScope"]>(
    teammate?.approvalScope ?? "environment"
  )
  const [formError, setFormError] = useState<string>()
  const envName = $api
    .useQuery("get", "/api/environments")
    .data?.find((e) => e.id === env)?.name
  // The scopes as a request's Options word them, for any computer of this teammate.
  const covers = (s: Teammate["approvalScope"]) =>
    scopeChoice(s, {
      sandboxName: "the computer that asked",
      owner: name.trim() || "the teammate",
      envName,
    })

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

  // Only a valid form is saved, and a valid form has a harness.
  const body = (h: Harness) => ({
    name: name.trim(),
    role: role.trim() || undefined,
    soul: soul || undefined,
    harness: h,
    providerId,
    model: model.trim() || undefined,
    models: models.filter((m) => m !== model.trim()),
    gitName: gitName.trim() || undefined,
    gitEmail: gitEmail.trim() || undefined,
    useHostGitIdentity: useHostGit || undefined,
    avatar: avatar || undefined,
    approvalScope,
  })
  const submit = () => {
    if (!valid || saving) return
    setFormError(undefined)
    if (teammate)
      update.mutate({
        params: { path: { env, id: teammate.id } },
        body: body(harness),
      })
    else create.mutate({ params: { path: { env } }, body: body(harness) })
  }

  // What differs from the saved teammate, for the page's AutoSave.
  const dirty =
    teammate !== undefined &&
    (name !== teammate.name ||
      role !== teammate.role ||
      providerId !== teammate.providerId ||
      harness !== teammate.harness ||
      model !== teammate.model ||
      models.join(" ") !== (teammate.models ?? []).join(" ") ||
      gitName !== teammate.gitName ||
      gitEmail !== teammate.gitEmail ||
      useHostGit !== teammate.useHostGitIdentity ||
      soul !== teammate.soul ||
      avatar !== teammate.avatar ||
      approvalScope !== teammate.approvalScope)
  const inBar = useSavePart("teammate", {
    dirty,
    valid,
    signature: harness ? JSON.stringify(body(harness)) : "",
    save: () => {
      if (!teammate || !harness) return Promise.resolve()
      setFormError(undefined)
      return update.mutateAsync({
        params: { path: { env, id: teammate.id } },
        body: body(harness),
      })
    },
  })

  const soulLine = soul
    .split("\n")
    .map((l) => l.replace(/^#+\s*/, "").trim())
    .find(Boolean)
  const summaries = {
    identity: [name.trim(), role.trim()].filter(Boolean).join(" · "),
    agent: [
      model.trim() || undefined,
      harness && harnessLabels[harness],
      provider?.name,
      models.length > 0 ? `+${models.length} in chats` : undefined,
    ]
      .filter(Boolean)
      .join(" · "),
    git:
      useHostGit && hostGitSet
        ? `${hostGit.name} <${hostGit.email}>, yours`
        : `${gitName.trim() || name.trim()} <${gitEmail.trim() || `${slug(name) || "name"}@agents.invalid`}>`,
    approvals: covers(approvalScope),
    soul: soulLine ?? "None",
  }
  const saveRow = inBar ? (
    formError && <FieldError>{formError}</FieldError>
  ) : (
    <>
      {formError && <FieldError>{formError}</FieldError>}
      <div className="flex justify-end">
        <Button type="submit" disabled={!valid || saving}>
          {saving && <Spinner />}
          {teammate ? "Save" : "Add a teammate"}
        </Button>
      </div>
    </>
  )

  const approvals = (
    <FormSection
      id="approvals"
      summary={summaries.approvals}
      title="New rules cover"
      icon={ShieldCheckIcon}
      area="network"
    >
      <div
        role="radiogroup"
        aria-label="New rules cover"
        className="grid gap-1.5"
      >
        {approvalScopes.map((s) => (
          <label
            key={s}
            className="flex cursor-pointer items-center gap-2 rounded-(--radius) border px-3 py-2 text-xs has-checked:border-primary has-checked:bg-primary/5 has-focus-visible:ring-2 has-focus-visible:ring-ring/30"
          >
            <input
              type="radio"
              name="teammate-approval-scope"
              className="sr-only"
              checked={approvalScope === s}
              onChange={() => setApprovalScope(s)}
            />
            {covers(s)}
          </label>
        ))}
      </div>
      <FieldDescription>
        When one of its computers asks to reach a host, the request starts with
        this choice for the rule it makes, and you can pick another on it.
      </FieldDescription>
    </FormSection>
  )

  return (
    <FormSections.Provider value={sections && { ...sections, saveRow }}>
      <form
        className={cn("grid grid-cols-1", sections ? "gap-0.5" : "gap-6")}
        onSubmit={(e) => {
          e.preventDefault()
          submit()
        }}
      >
        <FormSection
          id="identity"
          summary={summaries.identity}
          title="Identity"
          icon={IdentificationCardIcon}
          area="chat"
        >
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
              {avatar
                ? "A drawn sprite, the initials, a shape or an emoji, on the teammate's color."
                : "Until you pick one, the teammate wears the sprite its name picks."}
            </FieldDescription>
          </Field>
        </FormSection>
        <FormSection
          id="agent"
          summary={summaries.agent}
          title="Model"
          icon={RobotIcon}
          area="chat"
        >
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
            <ProviderNeeds
              provider={provider}
              none={providers.length === 0}
              onOpen={() => setProviderDialog(true)}
            />
          </Field>
          <Field>
            <FieldLabel>Agent</FieldLabel>
            <ToggleGroup
              aria-label="Agent"
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
            <FieldDescription>The agents this provider runs.</FieldDescription>
          </Field>
          <Field>
            <FieldLabel htmlFor="teammate-model">Default model</FieldLabel>
            <ModelInput
              id="teammate-model"
              placeholder={
                provider?.kind === "openai_compatible"
                  ? provider.model
                  : provider?.kind === "openai_api"
                    ? "gpt-6.1-sol"
                    : needsModel
                      ? "claude-sonnet-5-5"
                      : "The subscription's default"
              }
              suggestions={modelSuggestions}
              value={model}
              onChange={setModel}
            />
            {compatible && <ModelSuggestions {...suggestions} />}
          </Field>
          <Field>
            <FieldLabel htmlFor="teammate-offered">
              Also offered in chats
            </FieldLabel>
            <ModelsInput
              id="teammate-offered"
              value={models}
              onChange={setModels}
              exclude={model.trim()}
              suggestions={modelSuggestions}
            />
            <FieldDescription>
              A chat picks one of these or the default in its composer. The
              terminal, check-ins and scheduled tasks use the default.
            </FieldDescription>
          </Field>
        </FormSection>
        <FormSection
          id="git"
          summary={summaries.git}
          title="Commits as"
          icon={GitBranchIcon}
          area="git"
        >
          <div className="space-y-1.5">
            <label className="flex items-center gap-2 text-xs">
              <Switch checked={useHostGit} onCheckedChange={setUseHostGit} />
              Use my git identity
            </label>
            <p className="text-xs text-muted-foreground">
              {hostGitSet ? (
                <>
                  Commit as{" "}
                  <span className="font-mono">
                    {hostGit.name} &lt;{hostGit.email}&gt;
                  </span>
                  , the identity in your git config on this machine.
                </>
              ) : (
                "Your git config on this machine has no user.name and user.email yet, so the name and email below are used."
              )}
            </p>
          </div>
          <div
            className={cn(
              "grid gap-4 sm:grid-cols-2",
              useHostGit && hostGitSet && "hidden"
            )}
          >
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
        {approvalsIn === undefined
          ? approvals
          : approvalsIn && createPortal(approvals, approvalsIn)}
        <FormSection
          id="soul"
          summary={summaries.soul}
          title="Instructions"
          icon={SparkleIcon}
          area="chat"
        >
          <Field data-invalid={soulBytes > maxSoulBytes || undefined}>
            {/* The section's title is the visible label; the textarea keeps its name. */}
            <FieldLabel htmlFor="teammate-soul" className="sr-only">
              Instructions
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
              <FieldError>The instructions are limited to 16 KiB.</FieldError>
            ) : (
              <FieldDescription>
                How it works and what it should know, in markdown.
              </FieldDescription>
            )}
          </Field>
        </FormSection>
        {!sections && saveRow}
      </form>
      {/* Outside the form: React sends the dialog's submit up its tree, which would save this. */}
      <ProviderDialog
        env={env}
        provider={providers.length ? provider : undefined}
        open={providerDialog}
        onOpenChange={setProviderDialog}
      />
    </FormSections.Provider>
  )
}

/** The avatar choices by group, with each one's name for people. */
const avatarGroups: { label: string; choices: [string, string][] }[] = [
  {
    label: "Sprites",
    choices: spriteNames.map((n) => [`sprite:${n}`, spriteLabel(n)]),
  },
  {
    label: "Initials and shapes",
    choices: [
      ["initials", "Initials"],
      ...shapeAvatars.map((shape): [string, string] => [
        `shape:${shape}`,
        shape[0].toUpperCase() + shape.slice(1),
      ]),
    ],
  },
  { label: "Emoji", choices: emojiAvatars.map((e) => [e, e]) },
]

/**
 * The avatars to pick from: the sprites, the initials and shapes, then the emoji. Without a
 * chosen avatar the sprite the name picks is marked; picking it keeps it when the name changes.
 */
function AvatarPicker({
  name,
  value,
  onChange,
}: {
  name: string
  value: string
  onChange: (avatar: string) => void
}) {
  const own = name.trim() || "?"
  const worn = value || `sprite:${spriteFor(own)}`
  return (
    <div role="group" aria-label="Avatar" className="grid gap-2">
      {avatarGroups.map((group) => (
        <div
          key={group.label}
          role="group"
          aria-label={group.label}
          className="flex flex-wrap gap-1"
        >
          {group.choices.map(([choice, label]) => (
            <button
              key={choice}
              type="button"
              aria-pressed={worn === choice}
              aria-label={label}
              title={label}
              onClick={() => onChange(choice)}
              className={cn(
                "rounded-full p-0.5 outline-none focus-visible:ring-2 focus-visible:ring-ring",
                worn === choice && "ring-2 ring-primary"
              )}
            >
              <TeammateAvatar
                name={own}
                avatar={choice}
                className="size-8 text-caption"
              />
            </button>
          ))}
        </div>
      ))}
    </div>
  )
}

const FormSections = createContext<
  | {
      open?: string
      onOpen: (id: string | undefined) => void
      saveRow: ReactNode
    }
  | undefined
>(undefined)

/**
 * A group of fields under a section title, the same sections as the teammate's overview; or,
 * in a teammate's settings, a row with the section's summary that opens to them.
 */
function FormSection({
  id,
  summary,
  title,
  icon,
  area,
  children,
}: {
  id: string
  summary: string
  title: string
  icon: Icon
  area?: Area
  children: ReactNode
}) {
  const sections = useContext(FormSections)
  if (sections)
    return (
      <DisclosureRow
        id={`teammate-${id}`}
        icon={icon}
        title={title}
        summary={summary}
        open={sections.open === id}
        onOpenChange={(open) => sections.onOpen(open ? id : undefined)}
      >
        {children}
        {sections.saveRow}
      </DisclosureRow>
    )
  return (
    <section className="grid gap-4">
      <SectionHeader title={title} icon={icon} area={area} />
      {children}
    </section>
  )
}
