import { createFileRoute } from "@tanstack/react-router"
import { PageHeader } from "@/components/page-header"
import { MemoryTabs, sharedScope } from "@/components/memory-tabs"
import { MemoryUsage } from "@/components/memory-sessions"
import { MemoryQuality } from "@/components/memory-quality"
import { ScrollArea } from "@/components/ui/scroll-area"
import { $api } from "@/lib/api/client"

export const Route = createFileRoute("/e/$env/settings/memory")({
  component: MemoryPage,
})

const explanation =
  "What teammates remember about you and your work: shared memory is visible to all of them, and your edits here outrank anything inferred."

/** Shared memory of the environment; each teammate's private memory is on its own page. */
function MemoryPage() {
  const { env } = Route.useParams()
  const scopes = $api.useQuery("get", "/api/environments/{env}/memory/scopes", {
    params: { path: { env } },
  })
  const current = scopes.data?.find((s) => s.scope === sharedScope)

  return (
    <>
      <PageHeader>
        <h1 className="text-sm font-medium">Shared memory</h1>
      </PageHeader>
      <ScrollArea className="min-h-0 flex-1" viewportClassName="space-y-4 p-4">
        <p className="max-w-2xl text-xs/relaxed text-muted-foreground">
          {explanation}{" "}
          <a
            href="https://lukaskoebe.github.io/sandbox-studio/memory/"
            target="_blank"
            rel="noreferrer"
            className="underline underline-offset-4"
          >
            Learn more
          </a>
        </p>
        {current && (
          <p className="text-xs text-muted-foreground">
            {current.facts} facts, {current.pages} pages; core pages use{" "}
            {current.coreChars} of {current.coreBudget} characters.
          </p>
        )}
        <MemoryUsage env={env} shared />
        <MemoryQuality env={env} />
        <MemoryTabs env={env} scope={sharedScope} />
      </ScrollArea>
    </>
  )
}
