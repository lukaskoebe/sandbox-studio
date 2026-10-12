import { createFileRoute } from "@tanstack/react-router"
import { ScrollArea } from "@/components/ui/scroll-area"
import { PageHeader } from "@/components/page-header"
import { RuntimeSection } from "@/components/runtime-card"

export const Route = createFileRoute("/e/$env/settings/runtime")({
  component: RuntimePage,
})

function RuntimePage() {
  return (
    <>
      <PageHeader>
        <h1 className="text-sm font-medium">Runtime</h1>
      </PageHeader>
      <ScrollArea className="min-h-0 flex-1" viewportClassName="space-y-4 p-4">
        <RuntimeSection />
      </ScrollArea>
    </>
  )
}
