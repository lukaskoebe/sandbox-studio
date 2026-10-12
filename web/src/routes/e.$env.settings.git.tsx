import { useEffect, useState, type FormEvent } from "react"
import { createFileRoute } from "@tanstack/react-router"
import { useQueryClient } from "@tanstack/react-query"
import { toast } from "sonner"
import {
  CopyIcon,
  KeyIcon,
  PlugsConnectedIcon,
  PlusIcon,
  TrashIcon,
  WarningIcon,
  XIcon,
} from "@phosphor-icons/react"
import {
  AlertDialog,
  AlertDialogAction,
  AlertDialogCancel,
  AlertDialogContent,
  AlertDialogDescription,
  AlertDialogFooter,
  AlertDialogHeader,
  AlertDialogTitle,
  AlertDialogTrigger,
} from "@/components/ui/alert-dialog"
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
  Empty,
  EmptyContent,
  EmptyDescription,
  EmptyHeader,
  EmptyTitle,
} from "@/components/ui/empty"
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
import { ScrollArea } from "@/components/ui/scroll-area"
import { Spinner } from "@/components/ui/spinner"
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "@/components/ui/table"
import {
  GitOutcome,
  GitReviewSheet,
  isGitApproval,
} from "@/components/git-review"
import { EnvironmentLabel, PageHeader } from "@/components/page-header"
import { ApprovalStatusBadge } from "@/components/status-badge"
import {
  $api,
  errorMessage,
  fetchClient,
  type AgentStatus,
  type Forge,
} from "@/lib/api/client"
import { gitSummary } from "@/lib/approval-kinds"
import { copyText } from "@/lib/clipboard"
import { formatAge } from "@/lib/utils"

export const Route = createFileRoute("/e/$env/settings/git")({
  component: GitPage,
})

const remoteHost = "git.studio.internal"

type Kind = Forge["kind"]
type Auth = "agent" | "key"

const kindLabels: Record<Kind, string> = {
  forgejo: "Forgejo",
  github: "GitHub",
  https: "Other HTTPS",
  ssh: "Other SSH",
}

const authLabels: Record<Auth, string> = {
  agent: "My SSH agent",
  key: "A Studio key",
}

const hasAPI = (kind: Kind) => kind === "forgejo" || kind === "github"

function GitPage() {
  const { env } = Route.useParams()
  const [adding, setAdding] = useState(false)
  const [reviewing, setReviewing] = useState<string>()
  const forges = $api.useQuery("get", "/api/environments/{env}/forges", {
    params: { path: { env } },
  })
  const approvals = $api.useQuery("get", "/api/environments/{env}/approvals", {
    params: { path: { env }, query: { status: "all" } },
  })
  const pushes = (approvals.data ?? []).filter(isGitApproval)
  const list = forges.data ?? []
  const usesAgent = list.some((f) => f.auth === "agent")
  const agent = $api.useQuery("get", "/api/system/ssh-agent", undefined, {
    enabled: usesAgent,
  })

  return (
    <>
      <PageHeader>
        <h1 className="text-sm font-medium">Git</h1>
        <EnvironmentLabel env={env} />
        <Button size="sm" className="ml-auto" onClick={() => setAdding(true)}>
          <PlusIcon />
          Add forge
        </Button>
      </PageHeader>
      <ScrollArea className="min-h-0 flex-1" viewportClassName="space-y-6 p-4">
        <p className="max-w-2xl text-xs/relaxed text-muted-foreground">
          Sandboxes reach your forges through Studio, which keeps their
          credentials and holds their pushes for your review.{" "}
          <a
            href="https://lukaskoebe.github.io/sandbox-studio/git-review/"
            target="_blank"
            rel="noreferrer"
            className="underline"
          >
            Learn more
          </a>
        </p>

        <section className="space-y-2">
          <h2 className="text-xs font-medium">Forges</h2>
          {forges.isPending ? (
            <Spinner className="mx-auto block" />
          ) : list.length ? (
            <div className="rounded-lg ring-1 ring-foreground/10">
              <Table>
                <TableHeader>
                  <TableRow>
                    <TableHead>Name</TableHead>
                    <TableHead>Forge</TableHead>
                    <TableHead>Remote URL</TableHead>
                    <TableHead>
                      <span className="sr-only">Actions</span>
                    </TableHead>
                  </TableRow>
                </TableHeader>
                <TableBody>
                  {list.map((f) => (
                    <ForgeRow
                      key={f.id}
                      env={env}
                      forge={f}
                      agent={agent.data}
                    />
                  ))}
                </TableBody>
              </Table>
            </div>
          ) : (
            <Empty>
              <EmptyHeader>
                <EmptyTitle>No forges yet</EmptyTitle>
                <EmptyDescription>
                  Add GitHub, a Forgejo instance such as Codeberg, or any git
                  host over HTTPS or SSH. Sandboxes can then use it through
                  Studio.
                </EmptyDescription>
              </EmptyHeader>
              <EmptyContent>
                <Button onClick={() => setAdding(true)}>
                  <PlusIcon />
                  Add forge
                </Button>
              </EmptyContent>
            </Empty>
          )}
        </section>

        <section className="space-y-2">
          <h2 className="text-xs font-medium">Pushes</h2>
          {approvals.isPending ? (
            <Spinner className="mx-auto block" />
          ) : pushes.length ? (
            <ul className="divide-y rounded-lg ring-1 ring-foreground/10">
              {pushes.slice(0, 100).map((a) => (
                <li
                  key={a.id}
                  className="flex flex-wrap items-center gap-x-2 gap-y-1 p-3 text-xs"
                >
                  <span className="min-w-0 truncate">{gitSummary(a)}</span>
                  <span className="text-muted-foreground">
                    {a.git?.review?.sandbox}
                  </span>
                  <ApprovalStatusBadge status={a.status} />
                  {a.git && a.status !== "pending" && (
                    <span className="max-w-md min-w-0">
                      <GitOutcome git={a.git} />
                    </span>
                  )}
                  <span className="ml-auto text-muted-foreground">
                    {formatAge(a.createdAt)}
                  </span>
                  <Button
                    size="xs"
                    variant={a.status === "pending" ? "default" : "ghost"}
                    onClick={() => setReviewing(a.id)}
                  >
                    {a.status === "pending" ? "Review" : "Show"}
                  </Button>
                </li>
              ))}
            </ul>
          ) : (
            <p className="text-xs text-muted-foreground">No pushes yet.</p>
          )}
        </section>
      </ScrollArea>
      <ForgeDialog env={env} open={adding} onOpenChange={setAdding} />
      <GitReviewSheet
        approval={pushes.find((a) => a.id === reviewing)}
        onOpenChange={(open) => {
          if (!open) setReviewing(undefined)
        }}
      />
    </>
  )
}

function ForgeRow({
  env,
  forge,
  agent,
}: {
  env: string
  forge: Forge
  agent?: AgentStatus
}) {
  const remote = `https://${remoteHost}/${forge.name}/<path>.git`
  const invalidate = useInvalidateForges()
  const [testing, setTesting] = useState(false)
  const [replacing, setReplacing] = useState(false)
  const [pinning, setPinning] = useState(false)
  const [keyAdded, setKeyAdded] = useState<Forge>()
  const switchAuth = $api.useMutation(
    "put",
    "/api/environments/{env}/forges/{id}/auth",
    {
      onSuccess: (res) => {
        invalidate()
        if (res.auth === "key") setKeyAdded(res)
      },
      onError: (err) =>
        toast.error("Could not change how Studio signs", {
          description: errorMessage(err),
        }),
    }
  )
  const dismissHint = $api.useMutation(
    "post",
    "/api/environments/{env}/forges/{id}/dismiss-agent-hint",
    { onSuccess: invalidate }
  )
  const switchTo = (auth: Auth) =>
    switchAuth.mutate({
      params: { path: { env, id: forge.id } },
      body: { auth },
    })
  return (
    <TableRow>
      <TableCell className="align-top font-mono">{forge.name}</TableCell>
      <TableCell className="align-top">
        <span className="font-mono">{forge.baseUrl}</span>
        <span className="ml-1.5 text-muted-foreground">
          {kindLabels[forge.kind]}
        </span>
        {forge.kind === "https" && forge.username && (
          <span className="ml-1.5 text-muted-foreground">
            as {forge.username}
          </span>
        )}
        {forge.kind === "ssh" && (
          <div className="mt-1 space-y-1 text-muted-foreground">
            <div>
              Host key{" "}
              <span className="font-mono">{forge.hostKeyFingerprint}</span>
            </div>
            {forge.hostKeyMismatch && (
              <div className="flex items-center gap-1 text-amber-600 dark:text-amber-400">
                <WarningIcon className="size-3 shrink-0" />
                The host offered{" "}
                <span className="font-mono">{forge.hostKeyMismatch}</span>{" "}
                instead. Check with the host&apos;s admin, then re-pin.
              </div>
            )}
            {forge.auth === "agent" ? (
              <>
                <div className="flex flex-wrap items-center gap-x-2">
                  Signs with your SSH agent.
                  <Button
                    variant="link"
                    size="xs"
                    disabled={switchAuth.isPending}
                    onClick={() => switchTo("key")}
                  >
                    Use a Studio key
                  </Button>
                </div>
                {agent && !agent.available && (
                  <div className="flex items-start gap-1 text-amber-600 dark:text-amber-400">
                    <WarningIcon className="mt-0.5 size-3 shrink-0" />
                    <span>
                      {agent.message} {agent.fix}
                    </span>
                  </div>
                )}
                {forge.agentSlowAt && !forge.agentHintDismissed && (
                  <div className="flex items-start gap-2 rounded-md bg-muted p-2 text-foreground">
                    <span className="min-w-0 flex-1">
                      Your SSH agent asked for confirmation. Set up a dedicated
                      key for Studio so fetches and pushes don&apos;t wait for
                      it.
                    </span>
                    <Button
                      size="xs"
                      disabled={switchAuth.isPending}
                      onClick={() => switchTo("key")}
                    >
                      Set up a Studio key
                    </Button>
                    <Button
                      variant="ghost"
                      size="icon-xs"
                      title="Dismiss"
                      aria-label="Dismiss"
                      disabled={dismissHint.isPending}
                      onClick={() =>
                        dismissHint.mutate({
                          params: { path: { env, id: forge.id } },
                        })
                      }
                    >
                      <XIcon />
                    </Button>
                  </div>
                )}
              </>
            ) : (
              <>
                <div className="flex items-center gap-1">
                  <span className="block max-w-80 truncate font-mono">
                    {forge.publicKey}
                  </span>
                  <Button
                    variant="ghost"
                    size="icon-xs"
                    title="Copy public key"
                    aria-label="Copy public key"
                    onClick={() => copy(forge.publicKey ?? "")}
                  >
                    <CopyIcon />
                  </Button>
                </div>
                <div className="flex flex-wrap items-center gap-x-2">
                  Signs with a Studio key.
                  <Button
                    variant="link"
                    size="xs"
                    disabled={switchAuth.isPending}
                    onClick={() => switchTo("agent")}
                  >
                    Use my SSH agent
                  </Button>
                </div>
              </>
            )}
          </div>
        )}
      </TableCell>
      <TableCell className="align-top">
        <div className="flex items-center gap-1">
          <span className="block max-w-80 truncate font-mono text-muted-foreground">
            {remote}
          </span>
          <Button
            variant="ghost"
            size="icon-xs"
            title="Copy remote URL"
            aria-label="Copy remote URL"
            onClick={() => copy(remote)}
          >
            <CopyIcon />
          </Button>
        </div>
      </TableCell>
      <TableCell className="align-top">
        <div className="flex justify-end gap-0.5">
          {forge.kind === "ssh" ? (
            <Button
              variant={forge.hostKeyMismatch ? "outline" : "ghost"}
              size="sm"
              onClick={() => setPinning(true)}
            >
              Re-pin
            </Button>
          ) : (
            <Button
              variant="ghost"
              size="icon-sm"
              title="Replace token"
              onClick={() => setReplacing(true)}
            >
              <KeyIcon />
            </Button>
          )}
          <Button
            variant="ghost"
            size="icon-sm"
            title="Test forge"
            onClick={() => setTesting(true)}
          >
            <PlugsConnectedIcon />
          </Button>
          <DeleteForge env={env} forge={forge} />
        </div>
        <Dialog open={testing} onOpenChange={setTesting}>
          <DialogContent>
            {testing && <TestForm env={env} forge={forge} />}
          </DialogContent>
        </Dialog>
        <Dialog open={replacing} onOpenChange={setReplacing}>
          <DialogContent>
            {replacing && (
              <TokenForm
                env={env}
                forge={forge}
                onDone={() => setReplacing(false)}
              />
            )}
          </DialogContent>
        </Dialog>
        <Dialog open={pinning} onOpenChange={setPinning}>
          <DialogContent>
            {pinning && (
              <PinForm
                env={env}
                forge={forge}
                onDone={() => setPinning(false)}
              />
            )}
          </DialogContent>
        </Dialog>
        <Dialog
          open={keyAdded !== undefined}
          onOpenChange={(open) => {
            if (!open) setKeyAdded(undefined)
          }}
        >
          <DialogContent>
            {keyAdded && (
              <PublicKeyNotice
                forge={keyAdded}
                onDone={() => setKeyAdded(undefined)}
              />
            )}
          </DialogContent>
        </Dialog>
      </TableCell>
    </TableRow>
  )
}

function useInvalidateForges() {
  const queryClient = useQueryClient()
  return () =>
    queryClient.invalidateQueries({
      queryKey: ["get", "/api/environments/{env}/forges"],
    })
}

function expiry(at: string) {
  const days = Math.floor((new Date(at).getTime() - Date.now()) / 86_400_000)
  const text =
    days < 0
      ? "The token has expired."
      : days === 0
        ? "The token expires today."
        : `The token expires in ${days} ${days === 1 ? "day" : "days"}.`
  return (
    <span
      className={
        days < 14
          ? "text-amber-600 dark:text-amber-400"
          : "text-muted-foreground"
      }
    >
      {text}
    </span>
  )
}

function TestForm({ env, forge }: { env: string; forge: Forge }) {
  const [repo, setRepo] = useState("")
  const test = $api.useMutation(
    "post",
    "/api/environments/{env}/forges/{id}/test",
    {
      onError: (err) =>
        toast.error("Could not test the forge", {
          description: errorMessage(err),
        }),
    }
  )
  const res = test.data
  const needsRepo = !hasAPI(forge.kind)
  const submit = (e: FormEvent) => {
    e.preventDefault()
    test.mutate({
      params: { path: { env, id: forge.id } },
      body: { repo: repo.trim() || undefined },
    })
  }
  return (
    <form onSubmit={submit} className="grid gap-4">
      <DialogHeader>
        <DialogTitle>Test {forge.name}</DialogTitle>
        <DialogDescription>
          {needsRepo
            ? "Studio lists the branches of a repository with its credential."
            : "Studio asks the forge whose token it has, and lists the branches of a repository if you name one."}
        </DialogDescription>
      </DialogHeader>
      <Field>
        <FieldLabel htmlFor="forge-test-repo">Repository</FieldLabel>
        <Input
          id="forge-test-repo"
          value={repo}
          placeholder={needsRepo ? "group/project" : "owner/repo (optional)"}
          autoComplete="off"
          onChange={(e) => setRepo(e.target.value)}
        />
      </Field>
      {res &&
        (res.ok ? (
          <p className="text-xs/relaxed">
            {forge.name} works.
            {res.user && ` The token belongs to ${res.user}.`}
            {res.repo &&
              ` ${res.repo} has ${res.branches ?? 0} ${res.branches === 1 ? "branch" : "branches"}.`}{" "}
            {res.expiresAt && expiry(res.expiresAt)}
          </p>
        ) : (
          <FieldError>{res.message}</FieldError>
        ))}
      <DialogFooter>
        <Button
          type="submit"
          disabled={test.isPending || (needsRepo && repo.trim() === "")}
        >
          {test.isPending && <Spinner />}
          Test
        </Button>
      </DialogFooter>
    </form>
  )
}

function TokenForm({
  env,
  forge,
  onDone,
}: {
  env: string
  forge: Forge
  onDone: () => void
}) {
  const invalidate = useInvalidateForges()
  const [token, setToken] = useState("")
  const replace = $api.useMutation(
    "put",
    "/api/environments/{env}/forges/{id}/token",
    {
      onSuccess: () => {
        invalidate()
        toast.success(`Replaced the token of ${forge.name}`)
        onDone()
      },
    }
  )
  return (
    <form
      onSubmit={(e) => {
        e.preventDefault()
        replace.mutate({
          params: { path: { env, id: forge.id } },
          body: { token },
        })
      }}
      className="grid gap-4"
    >
      <DialogHeader>
        <DialogTitle>Replace the token of {forge.name}</DialogTitle>
        <DialogDescription>
          Pushes and fetches use the new token from now on.
        </DialogDescription>
      </DialogHeader>
      <Field>
        <FieldLabel htmlFor="forge-new-token">Access token</FieldLabel>
        <Input
          id="forge-new-token"
          type="password"
          value={token}
          autoComplete="off"
          onChange={(e) => setToken(e.target.value)}
        />
      </Field>
      {replace.error && <FieldError>{errorMessage(replace.error)}</FieldError>}
      <DialogFooter>
        <Button type="submit" disabled={token === "" || replace.isPending}>
          {replace.isPending && <Spinner />}
          Replace token
        </Button>
      </DialogFooter>
    </form>
  )
}

type Scanned = { key: string; fingerprint: string; algorithm: string }

/** Scans an SSH host for its key and asks the user to confirm the fingerprint. */
function HostKeyCheck({
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

function PinForm({
  env,
  forge,
  onDone,
}: {
  env: string
  forge: Forge
  onDone: () => void
}) {
  const invalidate = useInvalidateForges()
  const [scanned, setScanned] = useState<Scanned>()
  const [confirmed, setConfirmed] = useState(false)
  const pin = $api.useMutation(
    "put",
    "/api/environments/{env}/forges/{id}/host-key",
    {
      onSuccess: () => {
        invalidate()
        toast.success(`Pinned the host key of ${forge.name}`)
        onDone()
      },
    }
  )
  return (
    <form
      onSubmit={(e) => {
        e.preventDefault()
        if (scanned)
          pin.mutate({
            params: { path: { env, id: forge.id } },
            body: { hostKey: scanned.key },
          })
      }}
      className="grid gap-4"
    >
      <DialogHeader>
        <DialogTitle>Re-pin the host key of {forge.name}</DialogTitle>
        <DialogDescription>
          Pinned now:{" "}
          <span className="font-mono">{forge.hostKeyFingerprint}</span>. Only
          re-pin a key the host&apos;s admin confirms: a changed key can mean
          someone is intercepting the connection.
        </DialogDescription>
      </DialogHeader>
      <HostKeyCheck
        env={env}
        baseUrl={forge.baseUrl}
        scanned={scanned}
        onScanned={(s) => {
          setScanned(s)
          setConfirmed(false)
        }}
        confirmed={confirmed}
        onConfirmed={setConfirmed}
      />
      {pin.error && <FieldError>{errorMessage(pin.error)}</FieldError>}
      <DialogFooter>
        <Button type="submit" disabled={!confirmed || pin.isPending}>
          {pin.isPending && <Spinner />}
          Pin host key
        </Button>
      </DialogFooter>
    </form>
  )
}

async function copy(text: string) {
  try {
    await copyText(text)
    toast.success("Copied")
  } catch {
    toast.error("Could not copy")
  }
}

function DeleteForge({ env, forge }: { env: string; forge: Forge }) {
  const queryClient = useQueryClient()
  const remove = $api.useMutation(
    "delete",
    "/api/environments/{env}/forges/{id}",
    {
      onSettled: () => {
        queryClient.invalidateQueries({
          queryKey: ["get", "/api/environments/{env}/forges"],
        })
      },
      onError: (err) =>
        toast.error("Could not remove the forge", {
          description: errorMessage(err),
        }),
    }
  )
  return (
    <AlertDialog>
      <AlertDialogTrigger
        render={<Button variant="ghost" size="icon-sm" title="Remove forge" />}
      >
        <TrashIcon />
      </AlertDialogTrigger>
      <AlertDialogContent>
        <AlertDialogHeader>
          <AlertDialogTitle>Remove {forge.name}?</AlertDialogTitle>
          <AlertDialogDescription>
            Studio forgets its token or key. Pending pushes to it can no longer
            be approved.
          </AlertDialogDescription>
        </AlertDialogHeader>
        <AlertDialogFooter>
          <AlertDialogCancel>Cancel</AlertDialogCancel>
          <AlertDialogAction
            variant="destructive"
            disabled={remove.isPending}
            onClick={() =>
              remove.mutate({ params: { path: { env, id: forge.id } } })
            }
          >
            {remove.isPending && <Spinner />}
            Remove
          </AlertDialogAction>
        </AlertDialogFooter>
      </AlertDialogContent>
    </AlertDialog>
  )
}

function ForgeDialog({
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

function PublicKeyNotice({
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
