import { Link, createFileRoute } from "@tanstack/react-router"
import { useQueryClient } from "@tanstack/react-query"
import { toast } from "sonner"
import { GitOutcome } from "@/components/git-review"
import { Ident } from "@/components/ident"
import { KindChip } from "@/components/kind-chip"
import { PageHeader } from "@/components/page-header"
import { Button } from "@/components/ui/button"
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
import type { Area } from "@/lib/accents"
import { $api, errorMessage, type Approval } from "@/lib/api/client"
import {
  approvalDecision,
  approvalKind,
  approvalSource,
  gitSummary,
} from "@/lib/approval-kinds"
import { cn, formatAge, hostPort } from "@/lib/utils"

const chips: { kind?: Area; label: string }[] = [
  { label: "All" },
  { kind: "network", label: "Network" },
  { kind: "git", label: "Git" },
  { kind: "chat", label: "Chats" },
  { kind: "browser", label: "Browser" },
  { kind: "memory", label: "Memory" },
]

export const Route = createFileRoute("/e/$env/settings/activity")({
  validateSearch: (s: Record<string, unknown>): { kind?: Area } => ({
    kind: chips.find((c) => c.kind && c.kind === s.kind)?.kind,
  }),
  component: ActivitySettings,
})

/** At most this many decisions are listed. */
const shownMax = 200

/**
 * Every decision in the environment, newest first: yours, the security level's and the
 * reviewer's, on network requests, pushes, tool permissions, browser actions and memory.
 */
function ActivitySettings() {
  const { env } = Route.useParams()
  const { kind } = Route.useSearch()
  const queryClient = useQueryClient()
  const approvals = $api.useQuery(
    "get",
    "/api/environments/{env}/approvals",
    { params: { path: { env }, query: { status: "all" } } },
    { refetchInterval: 5000 }
  )
  const revert = $api.useMutation(
    "post",
    "/api/environments/{env}/approvals/{id}/revert",
    {
      onSuccess: () =>
        queryClient.invalidateQueries({
          queryKey: ["get", "/api/environments/{env}/approvals"],
        }),
      onError: (err) =>
        toast.error("Could not revert the decision", {
          description: errorMessage(err),
        }),
    }
  )
  const decided = (approvals.data ?? [])
    .filter(
      (a) => a.status !== "pending" && (!kind || approvalKind(a).area === kind)
    )
    .sort((a, b) => b.updatedAt.localeCompare(a.updatedAt))
    .slice(0, shownMax)

  return (
    <>
      <PageHeader>
        <h1 className="text-sm font-medium">Activity</h1>
      </PageHeader>
      <ScrollArea className="min-h-0 flex-1" viewportClassName="space-y-4 p-4">
        <p className="max-w-2xl text-xs/relaxed text-muted-foreground">
          Every decision in this environment: yours, the security level's and
          the reviewer's. Requests still waiting are on Home.
        </p>
        <div
          role="group"
          aria-label="Kind"
          className="flex flex-wrap items-center gap-1.5"
        >
          {chips.map((c) => (
            <Link
              key={c.label}
              from={Route.fullPath}
              search={{ kind: c.kind }}
              replace
              aria-pressed={kind === c.kind}
              className={cn(
                "rounded-full border px-2.5 py-0.5 text-xs text-muted-foreground hover:text-foreground",
                kind === c.kind &&
                  "border-foreground/30 bg-foreground/8 font-medium text-foreground"
              )}
            >
              {c.label}
            </Link>
          ))}
        </div>
        {approvals.isPending ? (
          <Spinner className="mx-auto block" />
        ) : decided.length === 0 ? (
          <p className="rounded-(--radius) border border-dashed px-3 py-2.5 text-xs text-muted-foreground">
            No decisions yet.
          </p>
        ) : (
          <div className="rounded-lg ring-1 ring-foreground/10">
            <Table>
              <TableHeader>
                <TableRow>
                  <TableHead>Decided</TableHead>
                  <TableHead>What</TableHead>
                  <TableHead>From</TableHead>
                  <TableHead>Decision</TableHead>
                  <TableHead>Details</TableHead>
                </TableRow>
              </TableHeader>
              <TableBody>
                {decided.map((a) => (
                  <TableRow key={a.id}>
                    <TableCell className="text-muted-foreground">
                      <time
                        dateTime={a.updatedAt}
                        title={new Date(a.updatedAt).toLocaleString()}
                      >
                        {formatAge(a.updatedAt)}
                      </time>
                    </TableCell>
                    <TableCell>
                      <What approval={a} />
                    </TableCell>
                    <TableCell className="text-muted-foreground">
                      {approvalSource(a)}
                    </TableCell>
                    <TableCell>
                      <span className="inline-flex flex-wrap items-center gap-2">
                        {approvalDecision(a)}
                        {a.network?.decided === "reviewer" && !a.reverted && (
                          <Button
                            variant="outline"
                            size="xs"
                            disabled={revert.isPending}
                            onClick={() =>
                              revert.mutate({
                                params: { path: { env, id: a.id } },
                              })
                            }
                          >
                            Revert
                          </Button>
                        )}
                      </span>
                    </TableCell>
                    <TableCell className="max-w-72 text-muted-foreground">
                      {a.git ? (
                        <div className="whitespace-normal">
                          <GitOutcome git={a.git} />
                        </div>
                      ) : (
                        <span className="block truncate">
                          {a.network?.reason}
                        </span>
                      )}
                    </TableCell>
                  </TableRow>
                ))}
              </TableBody>
            </Table>
          </div>
        )}
      </ScrollArea>
    </>
  )
}

/** The kind of a decision and what it was about; a push links to its review. */
function What({ approval: a }: { approval: Approval }) {
  const kind = approvalKind(a)
  const what = a.git ? (
    <Link
      to="/e/$env/git/reviews/$id"
      params={{ env: a.environmentId, id: a.id }}
      className="block max-w-96 truncate underline-offset-4 hover:underline"
    >
      {gitSummary(a)}
    </Link>
  ) : a.network ? (
    <Ident>{hostPort(a.network.host, a.network.port)}</Ident>
  ) : (
    <span className="block max-w-96 truncate">{kind.summary(a)}</span>
  )
  return (
    <div className="flex items-center gap-2">
      <KindChip area={kind.area} icon={kind.icon}>
        {kind.title}
      </KindChip>
      {what}
    </div>
  )
}
