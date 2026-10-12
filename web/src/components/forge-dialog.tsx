import { useEffect, useState, type FormEvent } from "react"
import { useQueryClient } from "@tanstack/react-query"
import { toast } from "sonner"
import { CopyIcon } from "@phosphor-icons/react"
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

export type Scanned = { key: string; fingerprint: string; algorithm: string }

/** Scans an SSH host for its key and asks the user to confirm the fingerprint. */
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
    onSuccess: (res) =>
      onScanned(
        {
          key: res.hostKey,
          fingerprint: res.fingerprint,
          algorithm: res.algorithm,
        },
        res.baseUrl
      ),
    onError: () => onScanned(undefined),
  })
  return (
    <Field>
      <FieldLabel>Host key</FieldLabel>
      {scanned ? (
        <>
          <p className="text-xs">
            <span className="text-muted-foreground">{scanned.algorithm}</span>{" "}
            <span className="font-mono break-all">{scanned.fingerprint}</span>
          </p>
          <label className="flex items-center gap-2 text-xs">
            <Checkbox
              checked={confirmed}
              onCheckedChange={(c) => onConfirmed(c === true)}
            />
            This is the fingerprint the host publishes
          </label>
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
          Add this public key to {forge.baseUrl} with write access. A deploy key
          for one repository is enough.
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
  https: "",
  ssh: "",
}

const namePlaceholders: Record<Kind, string> = {
  forgejo: "codeberg",
  github: "github",
  https: "gitlab",
  ssh: "myhost",
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
  const canSave =
    name.trim() !== "" &&
    baseUrl.trim() !== "" &&
    (ssh ? scanned !== undefined && confirmed : token !== "")

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
        ...(ssh ? { hostKey: scanned?.key, auth } : { token }),
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
          A git host Studio fetches from and pushes to for the sandboxes.
        </DialogDescription>
      </DialogHeader>
      <Field>
        <FieldLabel htmlFor="forge-repo-url">Repository URL</FieldLabel>
        <Input
          id="forge-repo-url"
          value={repoURL}
          placeholder="git@github.com:owner/repo.git"
          autoComplete="off"
          onChange={(e) => setRepoURL(e.target.value)}
        />
        <FieldDescription>
          Paste a repository&apos;s clone URL. Studio fills in the rest, which
          you can change.
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
          {kind === "https" &&
            "Any host serving git over HTTPS with a token, such as GitLab or Bitbucket. No pull requests."}
          {ssh && "Any host serving git over SSH. No pull requests."}
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
          Used in sandbox remote URLs. Sandboxes use https://{remoteHost}/
          {name.trim() || "<name>"}/{repoPath || "owner/repo"}.git
        </FieldDescription>
      </Field>
      <Field>
        <FieldLabel htmlFor="forge-url">
          {ssh ? "SSH address" : "URL"}
        </FieldLabel>
        <Input
          id="forge-url"
          value={baseUrl}
          placeholder={ssh ? "git@git.example.org" : "https://git.example.org"}
          autoComplete="off"
          onChange={(e) => {
            setBaseUrl(e.target.value)
            setScanned(undefined)
            setConfirmed(false)
          }}
        />
        {ssh && (
          <FieldDescription>
            user@host, or ssh://user@host:port. The user is git if you leave it
            out.
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
            The user git sends with the token. Bitbucket wants x-token-auth,
            GitLab takes any.
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
      {ssh ? (
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
