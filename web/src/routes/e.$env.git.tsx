import { useState, type FormEvent } from "react"
import { createFileRoute } from "@tanstack/react-router"
import { useQueryClient } from "@tanstack/react-query"
import { toast } from "sonner"
import {
  CopyIcon,
  PlugsConnectedIcon,
  PlusIcon,
  TrashIcon,
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
  gitSummary,
  isGitApproval,
} from "@/components/git-review"
import { PageHeader } from "@/components/page-header"
import { ApprovalStatusBadge } from "@/components/status-badge"
import { $api, errorMessage, fetchClient, type Forge } from "@/lib/api/client"
import { formatAge } from "@/lib/utils"

export const Route = createFileRoute("/e/$env/git")({
  component: GitPage,
})

const remoteHost = "git.studio.internal"

const explanation = `Sandboxes clone, fetch and push through https://${remoteHost}/<forge>/<owner>/<repo>.git. Studio adds the forge's token on the host, so sandboxes never see it. Pushes wait here for your review; approving one pushes it to the forge, and for a branch other than the default one, offers to open a pull request.`

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

  return (
    <>
      <PageHeader>
        <h1 className="text-sm font-medium">Git</h1>
        <Button size="sm" className="ml-auto" onClick={() => setAdding(true)}>
          <PlusIcon />
          Add forge
        </Button>
      </PageHeader>
      <div className="flex-1 space-y-6 overflow-auto p-4">
        <p className="max-w-2xl text-xs/relaxed text-muted-foreground">
          {explanation}
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
                    <ForgeRow key={f.id} env={env} forge={f} />
                  ))}
                </TableBody>
              </Table>
            </div>
          ) : (
            <Empty>
              <EmptyHeader>
                <EmptyTitle>No forges yet</EmptyTitle>
                <EmptyDescription>
                  Add a Forgejo instance, such as Codeberg, with an access
                  token. Sandboxes can then use it through Studio.
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
      </div>
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

function ForgeRow({ env, forge }: { env: string; forge: Forge }) {
  const remote = `https://${remoteHost}/${forge.name}/<owner>/<repo>.git`
  const test = $api.useMutation(
    "post",
    "/api/environments/{env}/forges/{id}/test",
    {
      onSuccess: (res) =>
        res.ok
          ? toast.success(`${forge.name} works`, {
              description: `The token belongs to ${res.user}.`,
            })
          : toast.error(`${forge.name} does not work`, {
              description: res.message,
            }),
      onError: (err) =>
        toast.error("Could not test the forge", {
          description: errorMessage(err),
        }),
    }
  )
  return (
    <TableRow>
      <TableCell className="font-mono">{forge.name}</TableCell>
      <TableCell>
        <span className="font-mono">{forge.baseUrl}</span>
        <span className="ml-1.5 text-muted-foreground">{forge.kind}</span>
      </TableCell>
      <TableCell>
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
      <TableCell>
        <div className="flex justify-end gap-0.5">
          <Button
            variant="ghost"
            size="icon-sm"
            title="Test forge"
            disabled={test.isPending}
            onClick={() =>
              test.mutate({ params: { path: { env, id: forge.id } } })
            }
          >
            {test.isPending ? <Spinner /> : <PlugsConnectedIcon />}
          </Button>
          <DeleteForge env={env} forge={forge} />
        </div>
      </TableCell>
    </TableRow>
  )
}

async function copy(text: string) {
  try {
    await navigator.clipboard.writeText(text)
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
            Studio forgets its token and the pushes staged for it. Sandboxes can
            no longer reach it, and pending pushes can no longer be approved.
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

function ForgeForm({ env, onDone }: { env: string; onDone: () => void }) {
  const queryClient = useQueryClient()
  const [name, setName] = useState("")
  const [baseUrl, setBaseUrl] = useState("https://codeberg.org")
  const [token, setToken] = useState("")
  const [error, setError] = useState<string>()
  const [saving, setSaving] = useState(false)
  const canSave = name.trim() !== "" && baseUrl.trim() !== "" && token !== ""

  const submit = async (e: FormEvent) => {
    e.preventDefault()
    setSaving(true)
    setError(undefined)
    const res = await fetchClient.POST("/api/environments/{env}/forges", {
      params: { path: { env } },
      body: {
        name: name.trim(),
        kind: "forgejo",
        baseUrl: baseUrl.trim(),
        token,
      },
    })
    setSaving(false)
    if (res.error) {
      setError(errorMessage(res.error))
      return
    }
    queryClient.invalidateQueries({
      queryKey: ["get", "/api/environments/{env}/forges"],
    })
    onDone()
  }

  return (
    <form onSubmit={submit} className="grid gap-4">
      <DialogHeader>
        <DialogTitle>Add forge</DialogTitle>
        <DialogDescription>
          A Forgejo or Gitea instance. GitHub is not supported yet.
        </DialogDescription>
      </DialogHeader>
      <Field>
        <FieldLabel htmlFor="forge-name">Name</FieldLabel>
        <Input
          id="forge-name"
          value={name}
          placeholder="codeberg"
          autoComplete="off"
          onChange={(e) => setName(e.target.value.toLowerCase())}
        />
        <FieldDescription>
          Part of the remote URL: https://{remoteHost}/{name.trim() || "<name>"}
          /&lt;owner&gt;/&lt;repo&gt;.git
        </FieldDescription>
      </Field>
      <Field>
        <FieldLabel htmlFor="forge-url">URL</FieldLabel>
        <Input
          id="forge-url"
          value={baseUrl}
          autoComplete="off"
          onChange={(e) => setBaseUrl(e.target.value)}
        />
      </Field>
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
          Needs read and write access to the repositories, and to issues for
          pull requests. Studio keeps it; sandboxes never see it.
        </FieldDescription>
      </Field>
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
