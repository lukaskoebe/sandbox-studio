import { useEffect, useState, type FormEvent } from "react"
import { useQueryClient } from "@tanstack/react-query"
import { toast } from "sonner"
import {
  CopyIcon,
  FolderOpenIcon,
  ShieldCheckIcon,
} from "@phosphor-icons/react"
import { FolderPickerDialog } from "@/components/folder-picker"
import { Ident } from "@/components/ident"
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
import { $api, errorMessage, fetchClient, type Forge } from "@/lib/api/client"
import { copyText } from "@/lib/clipboard"
import {
  apiLabels,
  authLabels,
  defaultAPIURL,
  freeName,
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
}: {
  env: string
  baseUrl: string
  scanned?: Scanned
  onScanned: (s: Scanned | undefined, baseUrl?: string) => void
  confirmed: boolean
  onConfirmed: (confirmed: boolean) => void
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

/** Whether a URL read as kind may still be a host Studio can ask which forge it runs. */
const undetected = (r: { kind: Kind; apiKind?: string }) =>
  r.kind === "https" || (r.kind === "ssh" && !r.apiKind)

/**
 * Whether an SSH address is a repository's clone URL: [user@]host:path, with a path that isn't a
 * port, or ssh://host/path.
 */
function isSSHCloneURL(v: string) {
  const scp = /^(?:[^\s/:@]+@)?[^\s/:@]+:([^\s/][^\s]*)$/.exec(v)
  if (scp) return !/^\d+$/.test(scp[1])
  return /^ssh:\/\/[^\s/]+\/[^\s/][^\s]*$/.test(v)
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
  const parse = $api.useMutation(
    "post",
    "/api/environments/{env}/forges/parse-url"
  )
  const { mutate: parseURL, data: parsed } = parse
  // A host Studio doesn't know is asked which forge it runs, which takes a moment longer.
  const detect = $api.useMutation(
    "post",
    "/api/environments/{env}/forges/parse-url"
  )
  const {
    mutate: detectURL,
    data: detected,
    reset: resetDetect,
    isPending: detecting,
  } = detect
  const forges = $api.useQuery("get", "/api/environments/{env}/forges", {
    params: { path: { env } },
  })
  const autoName = freeName(suggestedName(kind, baseUrl), forges.data ?? [])
  const forgeName = nameEdited ? name.trim() : autoName
  // A pasted repository URL fills in the form, after a short pause in typing.
  useEffect(() => {
    const url = repoURL.trim()
    if (url === "") return
    const timer = setTimeout(
      () => parseURL({ params: { path: { env } }, body: { url } }),
      300
    )
    return () => clearTimeout(timer)
  }, [repoURL, env, parseURL])
  const fill = (r: NonNullable<typeof parsed>) => {
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
  }
  useEffect(() => {
    if (!parsed) return
    fill(parsed)
    resetDetect()
    if (undetected(parsed))
      detectURL({
        params: { path: { env } },
        body: { url: parse.variables.body.url, detect: true },
      })
  }, [parsed])
  useEffect(() => {
    if (detected && !undetected(detected)) fill(detected)
  }, [detected])
  useEffect(() => {
    setRefused(false)
    setSkipCheck(false)
  }, [kind, baseUrl, token, apiKind, apiUrl])
  const ssh = kind === "ssh"
  const local = kind === "local"
  const canSave =
    forgeName !== "" &&
    baseUrl.trim() !== "" &&
    (local ||
      (ssh
        ? scanned !== undefined &&
          confirmed &&
          (apiKind === "" || (apiUrl.trim() !== "" && token !== ""))
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
    resetDetect()
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
                ...(apiKind ? { apiKind, apiUrl: apiUrl.trim(), token } : {}),
              }
            : { token }),
        ...(skipCheck ? { skipCheck } : {}),
      },
    })
    setSaving(false)
    if (res.error) {
      setError(errorMessage(res.error))
      setRefused(checkRefused(res.error))
      return
    }
    invalidate()
    if (ssh && res.data.auth === "key") setCreated(res.data)
    else onDone()
  }

  if (created) return <PublicKeyNotice forge={created} onDone={onDone} />

  return (
    <form onSubmit={submit} className="grid gap-4">
      <FolderPickerDialog
        open={browsing}
        onOpenChange={setBrowsing}
        start={baseUrl}
        onPick={setBaseUrl}
      />
      <DialogHeader>
        <DialogTitle>Add forge</DialogTitle>
        <DialogDescription>
          A git host, or a folder of repositories on this computer, that Studio
          fetches from and pushes to for the sandboxes.
        </DialogDescription>
      </DialogHeader>
      <Field>
        <FieldLabel htmlFor="forge-repo-url">Repository URL</FieldLabel>
        <Input
          id="forge-repo-url"
          value={repoURL}
          placeholder="git@github.com:owner/repo.git or ~/git/repo"
          autoComplete="off"
          onChange={(e) => setRepoURL(e.target.value)}
        />
        <FieldDescription>
          Paste a repository&apos;s clone URL, or its folder on this computer.
          Studio fills in the rest, which you can change.
        </FieldDescription>
        {repoURL.trim() !== "" && parse.error && (
          <FieldError>{errorMessage(parse.error)}</FieldError>
        )}
      </Field>
      {/* Short fields in pairs, so the form stays short on a wide screen. */}
      <div className="grid items-start gap-4 sm:grid-cols-2">
        <Field>
          <FieldLabel>Kind</FieldLabel>
          <Select
            value={kind}
            onValueChange={(v) => v && pickKind(v)}
            items={kindLabels}
          >
            <SelectTrigger className="w-full">
              <SelectValue />
            </SelectTrigger>
            <SelectContent>
              {(Object.keys(kindLabels) as Kind[]).map((k) => (
                <SelectItem key={k} value={k}>
                  {kindLabels[k]}
                </SelectItem>
              ))}
            </SelectContent>
          </Select>
          <FieldDescription>
            {detecting ? (
              <span className="flex items-center gap-2">
                <Spinner />
                Asking the host which forge it runs…
              </span>
            ) : (
              <>
                {kind === "forgejo" && "Forgejo or Gitea, such as Codeberg."}
                {kind === "github" && "github.com or GitHub Enterprise Server."}
                {kind === "gitlab" &&
                  "gitlab.com or a self-hosted GitLab. Approved pushes to other branches get a merge request."}
                {kind === "https" &&
                  "Any host serving git over HTTPS with a token, such as Bitbucket. No pull requests."}
                {ssh &&
                  "Any host serving git over SSH. Pull requests with the forge's API, below."}
                {local &&
                  "Repositories in a folder here, such as ~/git. Approved pushes go in with your git, as git push to the folder would; no pull requests."}
              </>
            )}
          </FieldDescription>
        </Field>
        <Field>
          <FieldLabel htmlFor="forge-url">
            {ssh ? "SSH address" : local ? "Folder" : "URL"}
          </FieldLabel>
          <div className="flex gap-2">
            <Input
              id="forge-url"
              value={baseUrl}
              placeholder={
                ssh
                  ? "git@git.example.org"
                  : local
                    ? "~/git"
                    : "https://git.example.org"
              }
              autoComplete="off"
              onChange={(e) => {
                const v = e.target.value
                // A clone URL pasted here fills in the form as in Repository URL.
                if (ssh && isSSHCloneURL(v.trim())) setRepoURL(v.trim())
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
          {local && (
            <FieldDescription>
              The folder the repositories are in. Sandboxes can fetch every
              repository in it; nothing goes in without your approval.
            </FieldDescription>
          )}
          {ssh && (
            <FieldDescription>
              user@host, or ssh://user@host:port. The user is git if you leave
              it out. A clone URL such as git@github.com:owner/repo.git works
              too.
            </FieldDescription>
          )}
        </Field>
      </div>
      {(kind === "https" || ssh) && (
        <div className="grid items-start gap-4 sm:grid-cols-2">
          {kind === "https" && (
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
                The user git sends with the token. Bitbucket wants x-token-auth;
                most hosts take any.
              </FieldDescription>
            </Field>
          )}
          {ssh && (
            <Field>
              <FieldLabel>Authentication</FieldLabel>
              <Select
                value={auth}
                onValueChange={(v) => v && setAuth(v)}
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
          )}
        </div>
      )}
      {local ? null : ssh ? (
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
          />
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
        </>
      ) : (
        <Field>
          <FieldLabel htmlFor="forge-token">Access token</FieldLabel>
          <Input
            id="forge-token"
            type="password"
            value={token}
            autoComplete="off"
            onChange={(e) => setToken(e.target.value)}
          />
          <FieldDescription>
            <TokenHint kind={kind} baseUrl={baseUrl} />
          </FieldDescription>
        </Field>
      )}
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
          . Filled in from the {local ? "folder" : "host"}; most people keep it.
        </FieldDescription>
      </Field>
      {error && <FieldError>{error}</FieldError>}
      {refused && (
        <SkipCheck checked={skipCheck} onCheckedChange={setSkipCheck} />
      )}
      <DialogFooter>
        <Button type="submit" disabled={!canSave || saving}>
          {saving && <Spinner />}
          Add forge
        </Button>
      </DialogFooter>
    </form>
  )
}
