import { Fragment, useEffect, useState, type FormEvent } from "react"
import { useQueryClient } from "@tanstack/react-query"
import { toast } from "sonner"
import {
  CaretRightIcon,
  CheckIcon,
  CopyIcon,
  FolderOpenIcon,
  ShieldCheckIcon,
  WarningCircleIcon,
  WarningIcon,
  XIcon,
} from "@phosphor-icons/react"
import { FolderPickerDialog } from "@/components/folder-picker"
import { Ident } from "@/components/ident"
import { Button } from "@/components/ui/button"
import { Checkbox } from "@/components/ui/checkbox"
import {
  Collapsible,
  CollapsibleContent,
  CollapsibleTrigger,
} from "@/components/ui/collapsible"
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
  Tooltip,
  TooltipContent,
  TooltipTrigger,
} from "@/components/ui/tooltip"
import { $api, errorMessage, fetchClient, type Forge } from "@/lib/api/client"
import { copyText } from "@/lib/clipboard"
import { cn } from "@/lib/utils"
import {
  apiLabels,
  authLabels,
  defaultAPIURL,
  freeName,
  hasAPI,
  kindLabels,
  remoteHost,
  suggestedName,
  type APIKind,
  type Auth,
  type Kind,
} from "@/lib/forges"

export type Scanned = {
  key: string
  fingerprint: string
  algorithm: string
  /** The forge whose published key this is, such as GitHub; nothing to compare then. */
  publishedBy?: string
}

/**
 * Scans an SSH host for its key and asks the user to confirm the fingerprint, unless it is a
 * key Studio knows the forge publishes.
 */
export function HostKeyCheck({
  env,
  baseUrl,
  scanned,
  onScanned,
  confirmed,
  onConfirmed,
  auto,
}: {
  env: string
  baseUrl: string
  scanned?: Scanned
  onScanned: (s: Scanned | undefined, baseUrl?: string) => void
  confirmed: boolean
  onConfirmed: (confirmed: boolean) => void
  /** Scan as soon as there is an address, without the Scan button. */
  auto?: boolean
}) {
  const scan = $api.useMutation("post", "/api/environments/{env}/forges/scan", {
    onSuccess: (res) => {
      onScanned(
        {
          key: res.hostKey,
          fingerprint: res.fingerprint,
          algorithm: res.algorithm,
          publishedBy: res.publishedBy,
        },
        res.baseUrl
      )
      if (res.publishedBy) onConfirmed(true)
    },
    onError: () => onScanned(undefined),
  })
  const { mutate: scanHost } = scan
  const address = baseUrl.trim()
  // After a pause in typing, as the address may be edited under More.
  useEffect(() => {
    if (!auto || address === "" || scanned) return
    const t = setTimeout(
      () => scanHost({ params: { path: { env } }, body: { baseUrl: address } }),
      400
    )
    return () => clearTimeout(t)
  }, [auto, address, env, scanHost])
  return (
    <Field>
      <FieldLabel>Host key</FieldLabel>
      {scanned ? (
        <>
          <p className="text-xs">
            <span className="text-muted-foreground">{scanned.algorithm}</span>{" "}
            <Ident copy>{scanned.fingerprint}</Ident>
          </p>
          {scanned.publishedBy ? (
            <p className="flex items-center gap-2 text-xs">
              <ShieldCheckIcon
                className="size-3.5 shrink-0 text-primary"
                aria-hidden
              />
              The key {scanned.publishedBy} publishes
            </p>
          ) : (
            <label className="flex items-center gap-2 text-xs">
              <Checkbox
                checked={confirmed}
                onCheckedChange={(c) => onConfirmed(c === true)}
              />
              This is the fingerprint the host publishes
            </label>
          )}
        </>
      ) : (
        <div>
          <Button
            type="button"
            variant="outline"
            size="sm"
            disabled={baseUrl.trim() === "" || scan.isPending}
            onClick={() =>
              scan.mutate({
                params: { path: { env } },
                body: { baseUrl: baseUrl.trim() },
              })
            }
          >
            {scan.isPending && <Spinner />}
            Scan
          </Button>
        </div>
      )}
      <FieldDescription>
        Studio pins this key and refuses the host if it ever offers another.
      </FieldDescription>
      {scan.error && <FieldError>{errorMessage(scan.error)}</FieldError>}
    </Field>
  )
}

function useInvalidateForges() {
  const queryClient = useQueryClient()
  return () => {
    queryClient.invalidateQueries({
      queryKey: ["get", "/api/environments/{env}/forges"],
    })
    queryClient.invalidateQueries({ queryKey: ["get", "/api/setup"] })
  }
}

async function copy(text: string) {
  try {
    await copyText(text)
    toast.success("Copied")
  } catch {
    toast.error("Could not copy")
  }
}

export function ForgeDialog({
  env,
  open,
  initialURL,
  onOpenChange,
}: {
  env: string
  open: boolean
  /** A repository URL the form starts from, as if pasted. */
  initialURL?: string
  onOpenChange: (open: boolean) => void
}) {
  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent className="sm:max-w-xl lg:max-w-2xl">
        {/* The form, including a typed token, exists only while the dialog is open. */}
        {open && (
          <ForgeForm
            env={env}
            initialURL={initialURL}
            onDone={() => onOpenChange(false)}
          />
        )}
      </DialogContent>
    </Dialog>
  )
}

export function PublicKeyNotice({
  forge,
  onDone,
}: {
  forge: Forge
  onDone: () => void
}) {
  return (
    <div className="grid gap-4">
      <DialogHeader>
        <DialogTitle>Add Studio&apos;s key to the host</DialogTitle>
        <DialogDescription>
          Add this public key to <Ident>{forge.baseUrl}</Ident> with write
          access. A deploy key for one repository is enough.
        </DialogDescription>
      </DialogHeader>
      <div className="flex items-start gap-1">
        <code className="min-w-0 flex-1 rounded-md bg-muted p-2 text-xs break-all">
          {forge.publicKey}
        </code>
        <Button
          variant="ghost"
          size="icon-sm"
          title="Copy public key"
          aria-label="Copy public key"
          onClick={() => copy(forge.publicKey ?? "")}
        >
          <CopyIcon />
        </Button>
      </div>
      <DialogFooter>
        <Button onClick={onDone}>Done</Button>
      </DialogFooter>
    </div>
  )
}

/** What a forge's access token needs, for a forge or API of kind at baseUrl. */
export function TokenHint({ kind, baseUrl }: { kind: Kind; baseUrl: string }) {
  return (
    <>
      {kind === "github" ? (
        <>
          A{" "}
          <a
            href={`${baseUrl.trim().replace(/\/+$/, "") || "https://github.com"}/settings/personal-access-tokens/new`}
            target="_blank"
            rel="noreferrer"
            className="underline"
          >
            fine-grained token
          </a>{" "}
          for the repositories sandboxes may use, with Contents and Pull
          requests read and write.
        </>
      ) : kind === "forgejo" ? (
        "Needs read and write access to the repositories, and to issues for pull requests."
      ) : kind === "gitlab" ? (
        "A personal or project access token with the api and write_repository scopes."
      ) : (
        "Needs read and write access to the repositories."
      )}{" "}
      Studio keeps it; sandboxes never see it.
    </>
  )
}

/**
 * Whether a save was refused because Studio could not use the forge's API with the token: the
 * address isn't the API's, the forge isn't of that kind, or it refuses the token.
 */
export function checkRefused(error: unknown) {
  const errors =
    error && typeof error === "object"
      ? (error as { errors?: { location?: string }[] }).errors
      : undefined
  return !!errors?.some((e) => e.location === "body.skipCheck")
}

/** Saving a forge's API although Studio could not use it, as checkRefused tells. */
export function SkipCheck({
  checked,
  onCheckedChange,
}: {
  checked: boolean
  onCheckedChange: (checked: boolean) => void
}) {
  return (
    <label className="flex items-center gap-2 text-xs">
      <Checkbox
        checked={checked}
        onCheckedChange={(c) => onCheckedChange(c === true)}
      />
      Save it anyway, without the check
    </label>
  )
}

const apiChoices = { none: "None", ...apiLabels }

/**
 * The API of an SSH forge: which one, its HTTPS URL and a token. git still goes over SSH; the
 * API lists the forge's repositories and serves its pull requests. With optional, None is a
 * choice, and the other fields show only once an API is picked.
 */
export function APIFields({
  sshAddress,
  kind,
  onKind,
  url,
  onURL,
  token,
  onToken,
  optional,
}: {
  sshAddress: string
  kind: APIKind | ""
  onKind: (kind: APIKind | "") => void
  url: string
  onURL: (url: string) => void
  token: string
  onToken: (token: string) => void
  optional?: boolean
}) {
  const choices = optional ? apiChoices : apiLabels
  return (
    <>
      <div className="grid items-start gap-4 sm:grid-cols-2">
        <Field>
          <FieldLabel>Pull requests</FieldLabel>
          <Select
            value={kind || "none"}
            onValueChange={(v) => {
              if (!v) return
              const k = v === "none" ? "" : (v as APIKind)
              onKind(k)
              if (k && url.trim() === "") onURL(defaultAPIURL(sshAddress))
            }}
            items={choices}
          >
            <SelectTrigger className="w-full">
              <SelectValue />
            </SelectTrigger>
            <SelectContent>
              {Object.entries(choices).map(([k, label]) => (
                <SelectItem key={k} value={k}>
                  {label}
                </SelectItem>
              ))}
            </SelectContent>
          </Select>
          <FieldDescription>
            {kind
              ? "Through the forge's API, with a token. git still goes over SSH."
              : "Give Studio the forge's API and a token, and agents can list its repositories and read, review and open pull requests."}
          </FieldDescription>
        </Field>
        {kind && (
          <Field>
            <FieldLabel htmlFor="forge-api-url">API URL</FieldLabel>
            <Input
              id="forge-api-url"
              value={url}
              placeholder="https://git.example.org"
              autoComplete="off"
              onChange={(e) => onURL(e.target.value)}
            />
            <FieldDescription>
              The forge&apos;s web address, as in your browser.
            </FieldDescription>
          </Field>
        )}
      </div>
      {kind && (
        <Field>
          <FieldLabel htmlFor="forge-api-token">Access token</FieldLabel>
          <Input
            id="forge-api-token"
            type="password"
            value={token}
            autoComplete="off"
            onChange={(e) => onToken(e.target.value)}
          />
          <FieldDescription>
            <TokenHint kind={kind} baseUrl={url} />
          </FieldDescription>
        </Field>
      )}
    </>
  )
}

const defaultBaseURLs: Record<Kind, string> = {
  forgejo: "https://codeberg.org",
  github: "https://github.com",
  gitlab: "https://gitlab.com",
  https: "",
  ssh: "",
  local: "",
}

/** The kinds whose address is an HTTPS URL. */
const httpsKinds: Kind[] = ["forgejo", "github", "gitlab", "https"]

/** The kinds in the order the picker shows them, with a word on each. */
const kindHints: Record<Kind, string> = {
  github: "or Enterprise Server",
  gitlab: "self-hosted or gitlab.com",
  forgejo: "or Gitea, such as Codeberg",
  https: "git only, no pull requests",
  ssh: "git over SSH",
  local: "such as ~/git",
}

/** What a kind is, under the picker. */
const kindWords: Record<Kind, string> = {
  forgejo: "Forgejo or Gitea, such as Codeberg.",
  github: "github.com or GitHub Enterprise Server.",
  gitlab:
    "gitlab.com or a self-hosted GitLab. Approved pushes to other branches get a merge request.",
  https:
    "Any host serving git over HTTPS with a token, such as Bitbucket. No pull requests.",
  ssh: "Any host serving git over SSH. Pull requests with the forge's API, below.",
  local:
    "Repositories in a folder here, such as ~/git. Approved pushes go in with your git, as git push to the folder would; no pull requests.",
}

/**
 * Whether an SSH address is a repository's clone URL: [user@]host:path, with a path that isn't a
 * port, or ssh://host/path.
 */
function isSSHCloneURL(v: string) {
  const scp = /^(?:[^\s/:@]+@)?[^\s/:@]+:([^\s/][^\s]*)$/.exec(v)
  if (scp) return !/^\d+$/.test(scp[1])
  return /^ssh:\/\/[^\s/]+\/[^\s/][^\s]*$/.test(v)
}

/** The host of a forge's address: an HTTPS URL's, or an SSH address's. */
function hostOf(kind: Kind, baseUrl: string) {
  const v = baseUrl.trim()
  return (
    (kind === "ssh"
      ? /^(?:ssh:\/\/)?(?:[^@/\s]+@)?([^:/\s]+)/.exec(v)?.[1]
      : /^https:\/\/(?:[^@/\s]+@)?([^/\s]+)/.exec(v)?.[1]) ?? v
  )
}

/** The steps of the dialog, with the one it is on. */
function Steps({ at }: { at: 1 | 2 }) {
  return (
    <ol
      aria-label="Steps"
      className="flex items-center gap-1.5 font-mono text-xs text-muted-foreground"
    >
      {(["Repository", "Forge"] as const).map((label, i) => (
        <Fragment key={label}>
          {i > 0 && <li aria-hidden>›</li>}
          <li
            aria-current={at === i + 1 ? "step" : undefined}
            className={cn(
              "border-b-2 border-transparent px-0.5 pb-0.5",
              at === i + 1 && "border-primary text-foreground"
            )}
          >
            {i + 1} {label}
          </li>
        </Fragment>
      ))}
    </ol>
  )
}

/** Whether Studio reaches the forge's pull requests, as a check or a cross with the reason. */
function PullRequests({ why, yes }: { yes: boolean; why: string }) {
  return (
    <Tooltip>
      <TooltipTrigger
        render={
          <span
            tabIndex={0}
            aria-label={`Pull requests: ${yes ? "yes" : "no"}. ${why}`}
            className={cn(
              "inline-flex cursor-help items-center gap-1 rounded-sm border px-1.5 py-px text-xs",
              yes
                ? "border-primary/30 bg-primary/5 text-primary"
                : "text-muted-foreground"
            )}
          />
        }
      >
        {yes ? (
          <CheckIcon weight="bold" className="size-3" aria-hidden />
        ) : (
          <XIcon weight="bold" className="size-3" aria-hidden />
        )}
        PRs
      </TooltipTrigger>
      <TooltipContent className="max-w-72">{why}</TooltipContent>
    </Tooltip>
  )
}

function ForgeForm({
  env,
  initialURL,
  onDone,
}: {
  env: string
  initialURL?: string
  onDone: () => void
}) {
  const invalidate = useInvalidateForges()
  const [step, setStep] = useState<1 | 2>(1)
  // Whether the second step shows the kind picker, rather than what Studio found.
  const [manual, setManual] = useState(false)
  const [more, setMore] = useState(false)
  const [repoURL, setRepoURL] = useState(initialURL ?? "")
  const [repoPath, setRepoPath] = useState("")
  const [kind, setKind] = useState<Kind>("github")
  // The name the user typed; until then the forge is named after its host.
  const [name, setName] = useState("")
  const [nameEdited, setNameEdited] = useState(false)
  const [browsing, setBrowsing] = useState(false)
  const [baseUrl, setBaseUrl] = useState(defaultBaseURLs.github)
  const [auth, setAuth] = useState<Auth>("agent")
  const [username, setUsername] = useState("")
  const [token, setToken] = useState("")
  // An SSH forge's API, for pull requests; its token is token.
  const [apiKind, setApiKind] = useState<APIKind | "">("")
  const [apiUrl, setApiUrl] = useState("")
  const [scanned, setScanned] = useState<Scanned>()
  const [confirmed, setConfirmed] = useState(false)
  const [created, setCreated] = useState<Forge>()
  const [error, setError] = useState<string>()
  // Whether the error is Studio failing to use the forge's API, which the user may skip.
  const [refused, setRefused] = useState(false)
  const [skipCheck, setSkipCheck] = useState(false)
  const [saving, setSaving] = useState(false)
  // Detect reads the URL, and asks a host Studio doesn't know which forge it runs.
  const detect = $api.useMutation(
    "post",
    "/api/environments/{env}/forges/parse-url"
  )
  const forges = $api.useQuery("get", "/api/environments/{env}/forges", {
    params: { path: { env } },
  })
  const autoName = freeName(suggestedName(kind, baseUrl), forges.data ?? [])
  const forgeName = nameEdited ? name.trim() : autoName
  const ssh = kind === "ssh"
  const local = kind === "local"
  const host = hostOf(kind, baseUrl)

  const fill = (r: NonNullable<typeof detect.data>) => {
    setKind(r.kind)
    setBaseUrl(r.baseUrl)
    setName(r.name)
    setNameEdited(false)
    setRepoPath(r.path)
    setAuth(r.auth === "key" ? "key" : "agent")
    setApiKind(r.apiKind ?? "")
    setApiUrl(r.apiUrl ?? "")
    setScanned(undefined)
    setConfirmed(false)
    setError(undefined)
    // A token is for the forge it was typed for.
    setToken("")
    // A host that answered as no forge Studio knows gets the picker.
    setManual(r.kind === "https")
    setStep(2)
  }
  const runDetect = (url: string) =>
    detect.mutate(
      { params: { path: { env } }, body: { url, detect: true } },
      { onSuccess: fill }
    )
  const { mutate: detectOnce } = detect
  // A URL the dialog opens with is detected straight away, as if pasted and Detect pressed.
  useEffect(() => {
    const url = initialURL?.trim()
    if (url)
      detectOnce(
        { params: { path: { env } }, body: { url, detect: true } },
        { onSuccess: fill }
      )
  }, [])
  useEffect(() => {
    setRefused(false)
    setSkipCheck(false)
  }, [kind, baseUrl, token, apiKind, apiUrl])

  // An SSH forge's API is optional: without a token, git alone.
  const sshAPI = ssh && apiKind !== "" && token !== ""
  const checked = !local && (ssh ? sshAPI : token !== "")
  const canSave =
    forgeName !== "" &&
    baseUrl.trim() !== "" &&
    (local ||
      (ssh
        ? scanned !== undefined &&
          confirmed &&
          // An API picked by hand needs its URL and token; a found one, a URL if it has a token.
          (manual && apiKind !== ""
            ? apiUrl.trim() !== "" && token !== ""
            : !sshAPI || apiUrl.trim() !== "")
        : token !== ""))

  const pickKind = (k: Kind) => {
    setKind(k)
    // An HTTPS address the user pasted or typed stays for another HTTPS kind: a Forgejo
    // read as Other HTTPS keeps its host when it is made a Forgejo.
    const url = baseUrl.trim()
    const keep =
      httpsKinds.includes(k) &&
      httpsKinds.includes(kind) &&
      url !== "" &&
      !Object.values(defaultBaseURLs).includes(url)
    if (!keep) setBaseUrl(defaultBaseURLs[k])
    setApiKind("")
    setApiUrl("")
    setScanned(undefined)
    setConfirmed(false)
    setError(undefined)
  }

  const submit = async (e: FormEvent) => {
    e.preventDefault()
    setSaving(true)
    setError(undefined)
    const res = await fetchClient.POST("/api/environments/{env}/forges", {
      params: { path: { env } },
      body: {
        name: forgeName,
        kind,
        baseUrl: baseUrl.trim(),
        ...(kind === "https" && username.trim()
          ? { username: username.trim() }
          : {}),
        ...(local
          ? {}
          : ssh
            ? {
                hostKey: scanned?.key,
                auth,
                ...(sshAPI ? { apiKind, apiUrl: apiUrl.trim(), token } : {}),
              }
            : { token }),
        ...(skipCheck ? { skipCheck } : {}),
      },
    })
    setSaving(false)
    if (res.error) {
      const r = checkRefused(res.error)
      // A refused check's detail already says why; its field only repeats it.
      setError(
        (r ? (res.error as { detail?: string }).detail : undefined) ??
          errorMessage(res.error)
      )
      setRefused(r)
      // An error that isn't about the token may be about a field under More.
      if (!r) setMore(true)
      return
    }
    invalidate()
    if (ssh && res.data.auth === "key") setCreated(res.data)
    else onDone()
  }

  if (created) return <PublicKeyNotice forge={created} onDone={onDone} />

  const header = (
    <DialogHeader>
      <div className="flex items-center gap-3 pr-8">
        <DialogTitle>Add forge</DialogTitle>
        <span className="ml-auto">
          <Steps at={step} />
        </span>
      </div>
      {step === 1 && (
        <DialogDescription>
          A git host, or a folder of repositories on this computer, that Studio
          fetches from and pushes to for the sandboxes.
        </DialogDescription>
      )}
    </DialogHeader>
  )

  if (step === 1)
    return (
      <form
        className="grid gap-4"
        onSubmit={(e) => {
          e.preventDefault()
          if (repoURL.trim() !== "") runDetect(repoURL.trim())
        }}
      >
        {header}
        <Field>
          <FieldLabel htmlFor="forge-repo-url">Repository</FieldLabel>
          <div className="flex gap-2">
            <Input
              id="forge-repo-url"
              className="font-mono"
              value={repoURL}
              placeholder="https://git.example.org/team/webshop.git"
              autoComplete="off"
              spellCheck={false}
              autoFocus
              onChange={(e) => {
                setRepoURL(e.target.value)
                detect.reset()
              }}
            />
            <Button
              type="submit"
              disabled={repoURL.trim() === "" || detect.isPending}
            >
              {detect.isPending && <Spinner />}
              Detect
            </Button>
          </div>
          <FieldDescription>
            {detect.isPending ? (
              "Asking the host which forge it runs…"
            ) : (
              <>
                The URL you clone it with, HTTPS or SSH, its page in the
                browser, or its folder here such as ~/git/webshop.{" "}
                <Button
                  type="button"
                  variant="link"
                  size="xs"
                  className="h-auto p-0 align-baseline"
                  onClick={() => setBrowsing(true)}
                >
                  <FolderOpenIcon />
                  Pick a folder on this computer…
                </Button>
              </>
            )}
          </FieldDescription>
          {detect.error && (
            <FieldError>{errorMessage(detect.error)}</FieldError>
          )}
        </Field>
        <div className="grid gap-2 border-t pt-3">
          <span className="text-xs text-muted-foreground">Works with</span>
          <ul className="flex flex-wrap gap-1.5 font-mono text-xs">
            {[
              "GitHub and Enterprise",
              "GitLab",
              "Forgejo and Gitea",
              "any HTTPS or SSH git host",
              "a folder here",
            ].map((w) => (
              <li key={w} className="rounded-sm border px-2 py-0.5">
                {w}
              </li>
            ))}
          </ul>
        </div>
        <DialogFooter className="sm:justify-start">
          <Button
            type="button"
            variant="link"
            className="px-0"
            onClick={() => {
              setManual(true)
              setStep(2)
            }}
          >
            Set it up by hand instead
          </Button>
        </DialogFooter>
        <FolderPickerDialog
          open={browsing}
          onOpenChange={setBrowsing}
          start={/^[~/]/.test(repoURL.trim()) ? repoURL : ""}
          repositories
          onPick={(path, git) => {
            setRepoURL(path)
            // A repository is one of the folder it is in; another folder is the forge itself.
            if (git) runDetect(path)
            else
              fill({
                kind: "local",
                baseUrl: path,
                name: "local",
                path: "",
                auth: "none",
              })
          }}
        />
      </form>
    )

  // The address, in the main form when picking the kind, under More when Studio found it.
  const address = (
    <Field>
      <FieldLabel htmlFor="forge-url">
        {ssh ? "SSH address" : local ? "Folder" : "URL"}
      </FieldLabel>
      <div className="flex gap-2">
        <Input
          id="forge-url"
          className="font-mono"
          value={baseUrl}
          placeholder={
            ssh
              ? "git@git.example.org"
              : local
                ? "~/git"
                : "https://git.example.org"
          }
          autoComplete="off"
          spellCheck={false}
          onChange={(e) => {
            const v = e.target.value
            // A clone URL pasted here is detected as in the first step.
            if (ssh && isSSHCloneURL(v.trim())) {
              setRepoURL(v.trim())
              runDetect(v.trim())
            }
            setBaseUrl(v)
            setScanned(undefined)
            setConfirmed(false)
          }}
        />
        {local && (
          <Button
            type="button"
            variant="outline"
            onClick={() => setBrowsing(true)}
          >
            <FolderOpenIcon />
            Browse…
          </Button>
        )}
      </div>
      <FieldDescription>
        {local
          ? "The folder the repositories are in. Sandboxes can fetch every repository in it; nothing goes in without your approval."
          : ssh
            ? "user@host, or ssh://user@host:port. The user is git if you leave it out. A clone URL such as git@github.com:owner/repo.git works too."
            : manual
              ? "From the URL you pasted. It stays when you pick another HTTPS kind."
              : "The forge's web address, as in your browser."}
      </FieldDescription>
    </Field>
  )
  const usernameField = kind === "https" && (
    <Field>
      <FieldLabel htmlFor="forge-username">Username</FieldLabel>
      <Input
        id="forge-username"
        value={username}
        placeholder="git"
        autoComplete="off"
        onChange={(e) => setUsername(e.target.value)}
      />
      <FieldDescription>
        The user git sends with the token. Bitbucket wants x-token-auth; most
        hosts take any.
      </FieldDescription>
    </Field>
  )
  const moreWords = [
    "the forge's address",
    ...(kind === "https" ? ["the git username"] : []),
    ...(ssh && apiKind && !manual ? ["the API's address"] : []),
  ]

  return (
    <form onSubmit={submit} className="grid gap-4">
      <FolderPickerDialog
        open={browsing}
        onOpenChange={setBrowsing}
        start={baseUrl}
        onPick={setBaseUrl}
      />
      {header}
      {manual ? (
        <>
          {kind === "https" && repoPath !== "" && (
            <p
              role="status"
              className="flex items-start gap-2 rounded-lg border border-(--st-waiting)/40 bg-(--st-waiting)/10 px-3 py-2 text-xs"
            >
              <WarningIcon className="mt-px size-3.5 shrink-0 text-(--st-waiting-fg)" />
              <span>
                <Ident>{host}</Ident> didn&apos;t answer as Forgejo, GitLab or
                GitHub. Pick what it runs for pull requests, or keep Other HTTPS
                for git alone.
              </span>
            </p>
          )}
          <Field>
            <FieldLabel id="forge-kind">Kind</FieldLabel>
            <div
              role="radiogroup"
              aria-labelledby="forge-kind"
              className="grid grid-cols-2 gap-2 sm:grid-cols-3"
            >
              {(Object.keys(kindHints) as Kind[]).map((k) => (
                <button
                  key={k}
                  type="button"
                  role="radio"
                  aria-checked={kind === k}
                  aria-label={kindLabels[k]}
                  onClick={() => kind !== k && pickKind(k)}
                  className={cn(
                    "grid gap-0.5 rounded-md border px-3 py-2 text-left outline-none hover:bg-muted/50 focus-visible:ring-2 focus-visible:ring-ring/30",
                    kind === k &&
                      "border-primary bg-primary/5 ring-1 ring-primary"
                  )}
                >
                  <span className="font-mono text-xs font-semibold">
                    {kindLabels[k]}
                  </span>
                  <span className="text-xs text-muted-foreground">
                    {kindHints[k]}
                  </span>
                </button>
              ))}
            </div>
            <FieldDescription>{kindWords[kind]}</FieldDescription>
          </Field>
          {address}
          {(usernameField || ssh) && (
            <div className="grid items-start gap-4 sm:grid-cols-2">
              {usernameField}
              {ssh && <AuthField auth={auth} onAuth={setAuth} />}
            </div>
          )}
        </>
      ) : (
        <section
          aria-label="What Studio found"
          className="flex flex-wrap items-center gap-x-2.5 gap-y-1 rounded-lg border bg-muted/40 px-3 py-2 text-xs"
        >
          <CheckIcon
            weight="bold"
            className="size-3.5 shrink-0 text-primary"
            aria-hidden
          />
          <span className="font-mono font-semibold">
            {local
              ? `${baseUrl} is a folder here`
              : ssh
                ? apiKind
                  ? `git over SSH; ${host} runs ${apiLabels[apiKind]}`
                  : `git over SSH on ${host}`
                : `${host} runs ${kindLabels[kind]}`}
          </span>
          {repoPath && (
            <span className="font-mono text-muted-foreground">{repoPath}</span>
          )}
          <PullRequests
            yes={hasAPI(kind) || !!apiKind}
            why={
              hasAPI(kind)
                ? `Studio lists, reviews and opens pull requests through ${kindLabels[kind]}'s API, with the token below.`
                : apiKind
                  ? `Through ${apiLabels[apiKind]}'s API at ${apiUrl}, with the token below. Without one, git alone.`
                  : local
                    ? "A folder has none: approved pushes go in with your git."
                    : `${host} didn't answer as Forgejo, GitLab or GitHub. Pick the kind to give Studio its API.`
            }
          />
          <Button
            type="button"
            variant="link"
            size="xs"
            className="ml-auto h-auto px-0"
            onClick={() => setManual(true)}
          >
            Not right? Pick the kind
          </Button>
        </section>
      )}
      {ssh && (
        <>
          <HostKeyCheck
            env={env}
            baseUrl={baseUrl}
            scanned={scanned}
            onScanned={(s, normalized) => {
              setScanned(s)
              setConfirmed(false)
              if (normalized) setBaseUrl(normalized)
            }}
            confirmed={confirmed}
            onConfirmed={setConfirmed}
            auto={!manual}
          />
          {manual ? (
            <APIFields
              sshAddress={baseUrl}
              kind={apiKind}
              onKind={setApiKind}
              url={apiUrl}
              onURL={setApiUrl}
              token={token}
              onToken={setToken}
              optional
            />
          ) : (
            <>
              <AuthField auth={auth} onAuth={setAuth} />
              {apiKind && (
                <Field>
                  <FieldLabel htmlFor="forge-api-token">
                    API token, for pull requests
                  </FieldLabel>
                  <Input
                    id="forge-api-token"
                    type="password"
                    value={token}
                    autoComplete="off"
                    onChange={(e) => setToken(e.target.value)}
                  />
                  <FieldDescription>
                    <TokenHint kind={apiKind} baseUrl={apiUrl} /> Leave it empty
                    for git alone; you can add it later in the forge&apos;s row.
                  </FieldDescription>
                </Field>
              )}
            </>
          )}
        </>
      )}
      {!local && !ssh && (
        <Field>
          <FieldLabel htmlFor="forge-token">Access token</FieldLabel>
          <Input
            id="forge-token"
            type="password"
            value={token}
            autoComplete="off"
            aria-invalid={refused || undefined}
            autoFocus={!manual}
            onChange={(e) => setToken(e.target.value)}
          />
          <FieldDescription>
            <TokenHint kind={kind} baseUrl={baseUrl} />
          </FieldDescription>
        </Field>
      )}
      <Collapsible
        open={more}
        onOpenChange={setMore}
        className="group/more grid gap-4 border-t pt-3"
      >
        <CollapsibleTrigger className="flex w-fit items-center gap-1.5 text-left text-xs text-muted-foreground outline-none hover:text-foreground focus-visible:ring-2 focus-visible:ring-ring/30">
          <CaretRightIcon className="size-3 shrink-0 transition-transform group-data-[open]/more:rotate-90" />
          <span>
            More: the name in Studio (
            <span className="font-mono">{forgeName || "none"}</span>)
            {!manual && <>, {moreWords.join(", ")}</>}
          </span>
        </CollapsibleTrigger>
        <CollapsibleContent className="grid gap-4">
          <Field>
            <FieldLabel htmlFor="forge-name">Name in Studio</FieldLabel>
            <Input
              id="forge-name"
              className="font-mono sm:max-w-64"
              value={nameEdited ? name : autoName}
              placeholder={autoName}
              autoComplete="off"
              spellCheck={false}
              maxLength={32}
              onChange={(e) => {
                setName(e.target.value.toLowerCase())
                setNameEdited(true)
              }}
              // Emptied, the name goes back to the one from the host.
              onBlur={() => name.trim() === "" && setNameEdited(false)}
            />
            <FieldDescription>
              How sandboxes refer to this forge, in URLs like{" "}
              <Ident>
                {`https://${remoteHost}/${forgeName || "<name>"}/${repoPath || (local ? "repo" : "owner/repo")}.git`}
              </Ident>
              .{" "}
              {local
                ? "Filled in from the folder."
                : "Filled in from the host, without git. or www. and the domain ending: forgejo.mycompany.com is forgejo-mycompany."}
            </FieldDescription>
          </Field>
          {!manual && (
            <>
              {address}
              {usernameField}
              {ssh && apiKind && (
                <Field>
                  <FieldLabel htmlFor="forge-api-url">API URL</FieldLabel>
                  <Input
                    id="forge-api-url"
                    className="font-mono"
                    value={apiUrl}
                    autoComplete="off"
                    onChange={(e) => setApiUrl(e.target.value)}
                  />
                  <FieldDescription>
                    The forge&apos;s web address, as in your browser.
                  </FieldDescription>
                </Field>
              )}
            </>
          )}
        </CollapsibleContent>
      </Collapsible>
      {error && (
        <div
          role="alert"
          className="grid gap-2 rounded-lg border border-destructive/30 bg-destructive/5 px-3 py-2 text-xs"
        >
          <p className="flex items-start gap-2 text-destructive">
            <WarningCircleIcon className="mt-px size-3.5 shrink-0" />
            {error}
          </p>
          {refused && (
            <span className="pl-5.5">
              <SkipCheck checked={skipCheck} onCheckedChange={setSkipCheck} />
            </span>
          )}
        </div>
      )}
      <DialogFooter className="items-center">
        <Button
          type="button"
          variant="outline"
          className="sm:mr-auto"
          onClick={() => {
            setStep(1)
            setError(undefined)
          }}
        >
          Back
        </Button>
        {checked && !refused && (
          <span className="text-xs text-muted-foreground">
            Studio checks the token with the API first
          </span>
        )}
        <Button type="submit" disabled={!canSave || saving}>
          {saving && <Spinner />}
          Add forge
        </Button>
      </DialogFooter>
    </form>
  )
}

/** How Studio signs in to an SSH forge. */
function AuthField({
  auth,
  onAuth,
}: {
  auth: Auth
  onAuth: (auth: Auth) => void
}) {
  return (
    <Field>
      <FieldLabel>Authentication</FieldLabel>
      <Select
        value={auth}
        onValueChange={(v) => v && onAuth(v)}
        items={authLabels}
      >
        <SelectTrigger className="w-full">
          <SelectValue />
        </SelectTrigger>
        <SelectContent>
          {(Object.keys(authLabels) as Auth[]).map((a) => (
            <SelectItem key={a} value={a}>
              {authLabels[a]}
            </SelectItem>
          ))}
        </SelectContent>
      </Select>
      <FieldDescription>
        {auth === "agent"
          ? "Studio signs with the keys in your SSH agent, as ssh does. A hardware key may ask you to confirm."
          : "Studio makes a key for this forge. You add its public key to the host."}
      </FieldDescription>
    </Field>
  )
}
