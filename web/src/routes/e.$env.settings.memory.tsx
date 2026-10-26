import { createFileRoute } from "@tanstack/react-router"
import { SettingsPage } from "@/components/settings/page"
import { MemoryPanel, sharedScope } from "@/components/memory-panel"
import { MemoryQuality } from "@/components/memory-quality"

export const Route = createFileRoute("/e/$env/settings/memory")({
  component: MemoryPage,
})

const explanation =
  "What teammates remember about you and your work: shared memory is visible to all of them, and your edits here outrank anything inferred."

/** Shared memory of the environment; each teammate's private memory is on its own page. */
function MemoryPage() {
  const { env } = Route.useParams()

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
      <MemoryQuality env={env} />
      <MemoryPanel env={env} scope={sharedScope} />
    </SettingsPage>
  )
}
