import { useState, type FormEvent } from "react"
import { Link, createFileRoute } from "@tanstack/react-router"
import { useQueryClient } from "@tanstack/react-query"
import { toast } from "sonner"
import {
  CloudIcon,
  CopyIcon,
  GitBranchIcon,
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
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog"
import { Field, FieldError, FieldLabel } from "@/components/ui/field"
import { Input } from "@/components/ui/input"
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
import { Ident } from "@/components/ident"
import { KindChip } from "@/components/kind-chip"
import {
  ForgeDialog,
  HostKeyCheck,
  PublicKeyNotice,
  type Scanned,
} from "@/components/forge-dialog"
import {
  GitOutcome,
  GitReviewSheet,
  isGitApproval,
} from "@/components/git-review"
import { EnvironmentLabel, PageHeader } from "@/components/page-header"
import { SectionHeader } from "@/components/section-header"
import { StatusDot } from "@/components/status-dot"
import {
  $api,
  errorMessage,
  type AgentStatus,
  type Approval,
  type Forge,
} from "@/lib/api/client"
import { approvalKind, gitSummary } from "@/lib/approval-kinds"
import { copyText } from "@/lib/clipboard"
import { hasAPI, kindLabels, remoteHost, type Auth } from "@/lib/forges"
import { formatAge } from "@/lib/utils"

export const Route = createFileRoute("/e/$env/settings/git")({
  component: GitPage,
})

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
      <ScrollArea
        className="min-h-0 flex-1"
        viewportClassName="space-y-6 p-3 md:p-4"
      >
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
          <SectionHeader
            title="Forges"
            icon={CloudIcon}
            area="git"
            count={forges.data?.length || undefined}
          />
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
            <div className="flex flex-wrap items-center gap-3 rounded-(--radius) border border-dashed px-3 py-2.5 text-xs">
              <p className="min-w-0 flex-1 text-muted-foreground">
                No forges yet. Add GitHub, a Forgejo instance such as Codeberg,
                or any git host over HTTPS or SSH; sandboxes then use it through
                Studio.
              </p>
              <Button
                size="sm"
                variant="outline"
                onClick={() => setAdding(true)}
              >
                <PlusIcon />
                Add forge
              </Button>
            </div>
          )}
        </section>

        <section className="space-y-2">
          <SectionHeader
            title="Pushes"
            icon={GitBranchIcon}
            area="git"
            count={pushes.length || undefined}
          />
          {approvals.isPending ? (
            <Spinner className="mx-auto block" />
          ) : pushes.length ? (
            <div className="rounded-lg ring-1 ring-foreground/10">
              <Table>
                <TableHeader>
                  <TableRow>
                    <TableHead>Push</TableHead>
                    <TableHead>Sandbox</TableHead>
                    <TableHead>Status</TableHead>
                    <TableHead>Outcome</TableHead>
                    <TableHead>Requested</TableHead>
                    <TableHead>
                      <span className="sr-only">Actions</span>
                    </TableHead>
                  </TableRow>
                </TableHeader>
                <TableBody>
                  {pushes.slice(0, 100).map((a) => (
                    <PushRow
                      key={a.id}
                      approval={a}
                      onReview={() => setReviewing(a.id)}
                    />
                  ))}
                </TableBody>
              </Table>
            </div>
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

/** A push or pull request: what it asks, where it stands and the button that reviews it. */
function PushRow({
  approval,
  onReview,
}: {
  approval: Approval
  onReview: () => void
}) {
  const kind = approvalKind(approval)
  const pending = approval.status === "pending"
  return (
    <TableRow>
      <TableCell>
        <div className="flex items-center gap-2">
          <KindChip area={kind.area} icon={kind.icon}>
            {kind.title}
          </KindChip>
          <Link
            to="/e/$env/git/reviews/$id"
            params={{ env: approval.environmentId, id: approval.id }}
            className="max-w-96 truncate underline-offset-4 hover:underline"
          >
            {gitSummary(approval)}
          </Link>
        </div>
      </TableCell>
      <TableCell className="text-muted-foreground">
        {approval.git?.review?.sandbox}
      </TableCell>
      <TableCell>
        {pending ? (
          <StatusDot status="waiting" />
        ) : (
          <span className="capitalize">{approval.status}</span>
        )}
      </TableCell>
      <TableCell>
        {approval.git && !pending && (
          <div className="max-w-72 whitespace-normal">
            <GitOutcome git={approval.git} />
          </div>
        )}
      </TableCell>
      <TableCell className="text-muted-foreground">
        {formatAge(approval.createdAt)}
      </TableCell>
      <TableCell>
        <Button
          size="xs"
          variant={pending ? "default" : "ghost"}
          onClick={onReview}
        >
          {pending ? "Review" : "Show"}
        </Button>
      </TableCell>
    </TableRow>
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
      <TableCell className="align-top">
        <Ident>{forge.name}</Ident>
      </TableCell>
      <TableCell className="align-top">
        <Ident>{forge.baseUrl}</Ident>
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
              {forge.hostKeyFingerprint && (
                <Ident copy>{forge.hostKeyFingerprint}</Ident>
              )}
            </div>
            {forge.hostKeyMismatch && (
              <div className="flex items-center gap-1 text-amber-600 dark:text-amber-400">
                <WarningIcon className="size-3 shrink-0" />
                The host offered <Ident>{forge.hostKeyMismatch}</Ident> instead.
                Check with the host&apos;s admin, then re-pin.
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
            {res.repo && (
              <>
                {" "}
                <Ident>{res.repo}</Ident> has {res.branches ?? 0}{" "}
                {res.branches === 1 ? "branch" : "branches"}.
              </>
            )}{" "}
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
          {forge.hostKeyFingerprint && (
            <Ident>{forge.hostKeyFingerprint}</Ident>
          )}
          . Only re-pin a key the host&apos;s admin confirms: a changed key can
          mean someone is intercepting the connection.
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
