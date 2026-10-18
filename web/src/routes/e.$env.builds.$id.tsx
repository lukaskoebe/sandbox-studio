import { useRef, useState } from "react"
import { createFileRoute, useNavigate } from "@tanstack/react-router"
import { SettingsLink } from "@/components/settings/link"
import { BuildDialog } from "@/components/build-dialog"
import { BuildJobView, shortId } from "@/components/build-job-sheet"
import { CreateTemplateSandboxDialog } from "@/components/create-template-sandbox-dialog"
import { Ident } from "@/components/ident"
import { EnvironmentLabel, PageHeader } from "@/components/page-header"
import { Button } from "@/components/ui/button"
import { ScrollArea } from "@/components/ui/scroll-area"

export const Route = createFileRoute("/e/$env/builds/$id")({
  component: BuildPage,
})

/**
 * One template build as a page of its own, so it can be linked to: the same details, log and
 * spec as the sheet on Settings → Templates.
 */
function BuildPage() {
  const { env, id } = Route.useParams()
  const navigate = useNavigate()
  const [creating, setCreating] = useState<string | null>(null)
  const [submission, setSubmission] = useState<{
    source: string
    key: number
  } | null>(null)
  const submissionKey = useRef(0)

  return (
    <>
      <PageHeader>
        <h1 className="text-sm font-medium">
          Build <Ident>{shortId(id)}</Ident>
        </h1>
        <EnvironmentLabel env={env} />
        <Button
          variant="ghost"
          size="sm"
          className="ml-auto"
          render={<SettingsLink section="templates" />}
        >
          All builds
        </Button>
      </PageHeader>
      <ScrollArea className="min-h-0 flex-1">
        <div className="mx-auto max-w-3xl text-xs">
          <BuildJobView
            key={id}
            env={env}
            id={id}
            onUseSpec={(source) => {
              submissionKey.current += 1
              setSubmission({ source, key: submissionKey.current })
            }}
            onCreate={setCreating}
          />
        </div>
      </ScrollArea>
      {creating && (
        <CreateTemplateSandboxDialog
          key={creating}
          env={env}
          templateId={creating}
          open
          onOpenChange={(open) => {
            if (!open) setCreating(null)
          }}
        />
      )}
      {submission && (
        <BuildDialog
          key={submission.key}
          env={env}
          open
          source={submission.source}
          onOpenChange={(open) => {
            if (!open) setSubmission(null)
          }}
          onSubmitted={(job) => {
            setSubmission(null)
            void navigate({
              to: "/e/$env/builds/$id",
              params: { env, id: job.id },
            })
          }}
        />
      )}
    </>
  )
}
