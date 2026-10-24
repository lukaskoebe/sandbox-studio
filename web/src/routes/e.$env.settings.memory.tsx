import { createFileRoute } from "@tanstack/react-router"
import { SettingsPage } from "@/components/settings/page"
import { MemoryTabs, sharedScope } from "@/components/memory-tabs"
import { MemoryUsage } from "@/components/memory-sessions"
import { MemoryQuality } from "@/components/memory-quality"
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
    <SettingsPage
      title="Shared memory"
      intro={
        <>
          {explanation}{" "}
          <a
            href="https://lukaskoebe.github.io/sandbox-studio/memory/"
            target="_blank"
            rel="noreferrer"
            className="underline underline-offset-4"
          >
            Learn more
          </a>
        </>
      }
    >
      <div className="grid gap-1">
        {current && (
          <p className="text-xs text-muted-foreground">
            {current.facts} facts, {current.pages} pages; core pages use{" "}
            {current.coreChars} of {current.coreBudget} characters.
          </p>
        )}
        <MemoryUsage env={env} shared />
      </div>
      <MemoryQuality env={env} />
      <MemoryTabs env={env} scope={sharedScope} />
    </SettingsPage>
  )
}
