import { useState } from "react"
import { Link, createFileRoute } from "@tanstack/react-router"
import { PlusIcon, UsersThreeIcon } from "@phosphor-icons/react"
import { Badge } from "@/components/ui/badge"
import { Button } from "@/components/ui/button"
import { EmptyState } from "@/components/empty-state"
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
import { PageHeader } from "@/components/page-header"
import { TeammateAvatar } from "@/components/teammate-avatar"
import { TeammateDialog } from "@/components/teammate-dialog"
import { Readiness } from "@/components/teammate-readiness"
import { LeadGlyph } from "@/components/lead/glyph"
import { useActivity, type Presence } from "@/hooks/use-teammate-presence"
import { $api } from "@/lib/api/client"
import { harnessLabels } from "@/lib/teammates"
import { cn } from "@/lib/utils"

export const Route = createFileRoute("/e/$env/teammates/")({
  component: TeammatesPage,
})

const explanation =
  "Teammates are the agents of an environment: a name, a role and instructions, the agent they run, the provider and model they use, and who they commit as. Computers can belong to a teammate, and network rules can be scoped to one."

/**
 * A teammate's state in the words threads use; off while none of its computers runs, and, as
 * in the sidebar, "no computer" while it has none.
 */
const stateWords: Record<Presence, string> = {
  working: "working",
  waiting: "needs you",
  idle: "idle",
  offline: "off",
}

function StateWord({ presence, word }: { presence: Presence; word?: string }) {
  return (
    <span
      className={cn(
        "inline-flex items-center gap-1.5 whitespace-nowrap",
        presence === "waiting" && "text-(--st-waiting)",
        presence === "offline" && "text-muted-foreground"
      )}
    >
      <span
        aria-hidden
        className={cn(
          "size-1.5 rounded-full",
          presence === "working" && "animate-pulse bg-(--st-running)",
          presence === "waiting" && "bg-(--st-waiting)",
          presence === "idle" && "bg-muted-foreground/60",
          presence === "offline" && "ring-1 ring-muted-foreground/60 ring-inset"
        )}
      />
      {word ?? stateWords[presence]}
    </span>
  )
}

function TeammatesPage() {
  const { env } = Route.useParams()
  const [adding, setAdding] = useState(false)
  const teammates = $api.useQuery("get", "/api/environments/{env}/teammates", {
    params: { path: { env } },
  })
  const providers = $api.useQuery("get", "/api/environments/{env}/providers", {
    params: { path: { env } },
  })
  const lead = $api.useQuery("get", "/api/environments/{env}/lead", {
    params: { path: { env } },
  })
  const activity = useActivity(env)
  const sandboxes = $api.useQuery("get", "/api/environments/{env}/sandboxes", {
    params: { path: { env } },
  })
  const owners = new Set(sandboxes.data?.map((sb) => sb.teammateId))
  const list = teammates.data ?? []
  const providerList = providers.data ?? []
  const providerById = new Map(providerList.map((p) => [p.id, p]))
  const canAdd = providerList.length > 0

  const addButton = (
    <Button size="sm" onClick={() => setAdding(true)}>
      <PlusIcon />
      Add a teammate
    </Button>
  )

  return (
    <>
      <PageHeader>
        <h1 className="text-sm font-medium">Teammates</h1>
        {/* While there are none, the empty list has the one button. */}
        {list.length > 0 && <div className="ml-auto">{addButton}</div>}
      </PageHeader>
      <ScrollArea className="min-h-0 flex-1" viewportClassName="space-y-6 p-4">
        <p className="max-w-2xl text-xs/relaxed text-muted-foreground">
          {explanation}
        </p>
        {teammates.isPending || providers.isPending ? (
          <Spinner className="mx-auto block" />
        ) : list.length ? (
          <div className="rounded-lg ring-1 ring-foreground/10">
            <Table>
              <TableHeader>
                <TableRow>
                  <TableHead>Name</TableHead>
                  <TableHead>Agent</TableHead>
                  <TableHead>Provider</TableHead>
                  <TableHead>Model</TableHead>
                  <TableHead>State</TableHead>
                  <TableHead>Commits as</TableHead>
                </TableRow>
              </TableHeader>
              <TableBody>
                {/* Lead first, as in the sidebar: Studio runs it, and it commits nothing. */}
                {lead.data && (
                  <TableRow>
                    <TableCell>
                      <div className="flex items-center gap-2">
                        <LeadGlyph className="size-6" />
                        <div className="min-w-0">
                          <Link
                            to="/e/$env/lead"
                            params={{ env }}
                            className="font-medium hover:underline"
                          >
                            {lead.data.settings.name}
                          </Link>
                          <div className="max-w-56 truncate text-muted-foreground">
                            Writes the day
                          </div>
                        </div>
                      </div>
                    </TableCell>
                    <TableCell>
                      <Badge variant="secondary">Studio</Badge>
                    </TableCell>
                    <TableCell>{lead.data.model?.provider}</TableCell>
                    <TableCell>
                      {lead.data.model ? (
                        <Ident>{lead.data.model.model}</Ident>
                      ) : (
                        <span className="text-muted-foreground">none</span>
                      )}
                    </TableCell>
                    <TableCell>
                      <StateWord
                        presence={lead.data.writing ? "working" : "idle"}
                      />
                    </TableCell>
                    <TableCell className="text-muted-foreground">
                      <span aria-label="Nothing">—</span>
                    </TableCell>
                  </TableRow>
                )}
                {list.map((p) => (
                  <TableRow key={p.id}>
                    <TableCell>
                      <div className="flex items-center gap-2">
                        <TeammateAvatar name={p.name} avatar={p.avatar} />
                        <div className="min-w-0">
                          <Link
                            to="/e/$env/teammates/$id"
                            params={{ env, id: p.id }}
                            className="font-medium hover:underline"
                          >
                            {p.name}
                          </Link>
                          <div className="max-w-56 truncate text-muted-foreground">
                            {p.role}
                          </div>
                        </div>
                      </div>
                    </TableCell>
                    <TableCell>
                      <Badge variant="secondary">
                        {harnessLabels[p.harness]}
                      </Badge>
                    </TableCell>
                    <TableCell>
                      {providerById.get(p.providerId)?.name}
                    </TableCell>
                    <TableCell>
                      {p.model ? (
                        <Ident>{p.model}</Ident>
                      ) : (
                        <span className="text-muted-foreground">default</span>
                      )}
                    </TableCell>
                    <TableCell>
                      <div className="flex flex-wrap items-center gap-2">
                        <StateWord
                          presence={activity(p.id).presence}
                          word={
                            sandboxes.data &&
                            !owners.has(p.id) &&
                            activity(p.id).presence === "idle"
                              ? "no computer"
                              : undefined
                          }
                        />
                        <Readiness
                          teammate={p}
                          provider={providerById.get(p.providerId)}
                          problemsOnly
                        />
                      </div>
                    </TableCell>
                    <TableCell className="text-muted-foreground">
                      <span className="block max-w-56 truncate">
                        {p.gitName}
                      </span>
                      <Ident className="block max-w-56 truncate">
                        {p.gitEmail}
                      </Ident>
                    </TableCell>
                  </TableRow>
                ))}
              </TableBody>
            </Table>
          </div>
        ) : (
          <EmptyState
            icon={UsersThreeIcon}
            title="No teammates yet"
            action={addButton}
          >
            {canAdd
              ? "Add one to hand it work; it runs on its own computers."
              : "Adding one starts with the AI it runs on, then its name and model."}
          </EmptyState>
        )}
      </ScrollArea>
      <TeammateDialog
        env={env}
        providers={providerList}
        open={adding}
        onOpenChange={setAdding}
      />
    </>
  )
}
