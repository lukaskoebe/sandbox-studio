import { createFileRoute, useNavigate } from "@tanstack/react-router"
import { ColumnPage } from "@/components/column-page"
import { MemoryPanel, sharedScope } from "@/components/memory-panel"
import { MemoryQuality } from "@/components/memory-quality"

export const Route = createFileRoute("/e/$env/memory/")({
  component: MemoryPage,
  // The open page or group of the index.
  validateSearch: (s: Record<string, unknown>): { open?: string } =>
    typeof s.open === "string" && s.open !== "" ? { open: s.open } : {},
})

const explanation =
  "What teammates remember about you and your work: shared memory is visible to all of them, and your edits here outrank anything inferred."

/** Shared memory of the environment; each teammate's private memory is on its own page. */
function MemoryPage() {
  const { env } = Route.useParams()
  const { open } = Route.useSearch()
  const navigate = useNavigate({ from: Route.fullPath })

  return (
    <ColumnPage
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
      <MemoryPanel
        env={env}
        scope={sharedScope}
        summary={<MemoryQuality env={env} />}
        open={open}
        onOpen={(o) => void navigate({ search: o ? { open: o } : {} })}
      />
    </ColumnPage>
  )
}
