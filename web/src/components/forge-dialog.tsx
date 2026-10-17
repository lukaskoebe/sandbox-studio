import { useEffect, useState, type FormEvent } from "react"
import { useQueryClient } from "@tanstack/react-query"
import { toast } from "sonner"
import { CopyIcon, ShieldCheckIcon } from "@phosphor-icons/react"
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
  authLabels,
  kindLabels,
  remoteHost,
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
  onOpenChange,
}: {
  env: string
  open: boolean
  onOpenChange: (open: boolean) => void
}) {
  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent>
        {/* The form, including a typed token, exists only while the dialog is open. */}
        {open && <ForgeForm env={env} onDone={() => onOpenChange(false)} />}
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

const defaultBaseURLs: Record<Kind, string> = {
  forgejo: "https://codeberg.org",
  github: "https://github.com",
  gitlab: "https://gitlab.com",
  https: "",
  ssh: "",
  local: "",
}

const namePlaceholders: Record<Kind, string> = {
  forgejo: "codeberg",
  github: "github",
  gitlab: "gitlab",
  https: "other",
  ssh: "myhost",
  local: "local",
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

function ForgeForm({ env, onDone }: { env: string; onDone: () => void }) {
  const invalidate = useInvalidateForges()
  const [repoURL, setRepoURL] = useState("")
  const [repoPath, setRepoPath] = useState("")
  const [kind, setKind] = useState<Kind>("github")
  const [name, setName] = useState("")
  const [baseUrl, setBaseUrl] = useState(defaultBaseURLs.github)
  const [auth, setAuth] = useState<Auth>("agent")
  const [username, setUsername] = useState("")
  const [token, setToken] = useState("")
  const [scanned, setScanned] = useState<Scanned>()
  const [confirmed, setConfirmed] = useState(false)
  const [created, setCreated] = useState<Forge>()
  const [error, setError] = useState<string>()
  const [saving, setSaving] = useState(false)
  const parse = $api.useMutation(
    "post",
    "/api/environments/{env}/forges/parse-url"
  )
  const { mutate: parseURL, data: parsed } = parse
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
  useEffect(() => {
    if (!parsed) return
    setKind(parsed.kind)
    setBaseUrl(parsed.baseUrl)
    setName(parsed.name)
    setRepoPath(parsed.path)
    setAuth(parsed.auth === "key" ? "key" : "agent")
    setScanned(undefined)
    setConfirmed(false)
    setError(undefined)
  }, [parsed])
  const ssh = kind === "ssh"
  const local = kind === "local"
  const canSave =
    name.trim() !== "" &&
    baseUrl.trim() !== "" &&
    (local || (ssh ? scanned !== undefined && confirmed : token !== ""))

  const pickKind = (k: Kind) => {
    setKind(k)
    setBaseUrl(defaultBaseURLs[k])
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
        name: name.trim(),
        kind,
        baseUrl: baseUrl.trim(),
        ...(kind === "https" && username.trim()
          ? { username: username.trim() }
          : {}),
        ...(local ? {} : ssh ? { hostKey: scanned?.key, auth } : { token }),
      },
    })
    setSaving(false)
    if (res.error) {
      setError(errorMessage(res.error))
      return
    }
    invalidate()
    if (ssh && res.data.auth === "key") setCreated(res.data)
    else onDone()
  }

  if (created) return <PublicKeyNotice forge={created} onDone={onDone} />

  return (
    <form onSubmit={submit} className="grid gap-4">
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
          {kind === "forgejo" && "Forgejo or Gitea, such as Codeberg."}
          {kind === "github" && "github.com or GitHub Enterprise Server."}
          {kind === "gitlab" &&
            "gitlab.com or a self-hosted GitLab. Approved pushes to other branches get a merge request."}
          {kind === "https" &&
            "Any host serving git over HTTPS with a token, such as Bitbucket. No pull requests."}
          {ssh && "Any host serving git over SSH. No pull requests."}
          {local &&
            "Repositories in a folder here, such as ~/git. Approved pushes go in with your git, as git push to the folder would; no pull requests."}
        </FieldDescription>
      </Field>
      <Field>
        <FieldLabel htmlFor="forge-name">Short name</FieldLabel>
        <Input
          id="forge-name"
          value={name}
          placeholder={namePlaceholders[kind]}
          autoComplete="off"
          onChange={(e) => setName(e.target.value.toLowerCase())}
        />
        <FieldDescription>
          Used in sandbox remote URLs. Sandboxes use{" "}
          <Ident>
            {`https://${remoteHost}/${name.trim() || "<name>"}/${repoPath || (local ? "repo" : "owner/repo")}.git`}
          </Ident>
        </FieldDescription>
      </Field>
      <Field>
        <FieldLabel htmlFor="forge-url">
          {ssh ? "SSH address" : local ? "Folder" : "URL"}
        </FieldLabel>
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
          <FieldDescription>
            The folder the repositories are in. Sandboxes can fetch every
            repository in it; nothing goes in without your approval.
          </FieldDescription>
        )}
        {ssh && (
          <FieldDescription>
            user@host, or ssh://user@host:port. The user is git if you leave it
            out. A clone URL such as git@github.com:owner/repo.git works too.
          </FieldDescription>
        )}
      </Field>
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
      {local ? null : ssh ? (
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
            {kind === "github" ? (
              <>
                A{" "}
                <a
                  href={`${baseUrl.replace(/\/+$/, "") || "https://github.com"}/settings/personal-access-tokens/new`}
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
          </FieldDescription>
        </Field>
      )}
      {error && <FieldError>{error}</FieldError>}
      <DialogFooter>
        <Button type="submit" disabled={!canSave || saving}>
          {saving && <Spinner />}
          Add forge
        </Button>
      </DialogFooter>
    </form>
  )
}
