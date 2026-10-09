import { useState } from "react"
import { useQueryClient } from "@tanstack/react-query"
import { useNavigate } from "@tanstack/react-router"
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
  Field,
  FieldDescription,
  FieldError,
  FieldLabel,
} from "@/components/ui/field"
import { Input } from "@/components/ui/input"
import { Spinner } from "@/components/ui/spinner"
import { $api, errorMessage } from "@/lib/api/client"

const namePattern = /^[a-z0-9]([a-z0-9-]{0,38}[a-z0-9])?$/

export function CreateTemplateSandboxDialog({
  env,
  templateId,
  open,
  onOpenChange,
}: {
  env: string
  templateId: string
  open: boolean
  onOpenChange: (open: boolean) => void
}) {
  const [name, setName] = useState("")
  const [serverError, setServerError] = useState<string | null>(null)
  const queryClient = useQueryClient()
  const navigate = useNavigate()
  const template = $api.useQuery(
    "get",
    "/api/environments/{env}/templates/{id}",
    { params: { path: { env, id: templateId } } },
    { enabled: open }
  )
  const create = $api.useMutation(
    "post",
    "/api/environments/{env}/templates/{id}/sandboxes",
    {
      onSuccess: async (sandbox) => {
        await queryClient.invalidateQueries({
          queryKey: ["get", "/api/environments/{env}/sandboxes"],
        })
        setName("")
        setServerError(null)
        onOpenChange(false)
        navigate({
          to: "/e/$env/sandboxes/$id",
          params: { env, id: sandbox.id },
        })
      },
      onError: (error) => setServerError(errorMessage(error)),
    }
  )
  const invalid = name !== "" && !namePattern.test(name)
  const pending = create.isPending

  function handleOpenChange(nextOpen: boolean) {
    if (!nextOpen && pending) return
    onOpenChange(nextOpen)
  }

  return (
    <Dialog open={open} onOpenChange={handleOpenChange}>
      <DialogContent
        className="max-h-[calc(100dvh-2rem)] overflow-y-auto"
        showCloseButton={!pending}
      >
        <form
          className="contents"
          onSubmit={(event) => {
            event.preventDefault()
            if (
              !template.data ||
              template.isError ||
              !name ||
              invalid ||
              pending
            )
              return
            setServerError(null)
            create.mutate({
              params: { path: { env, id: templateId } },
              body: { name },
            })
          }}
        >
          <DialogHeader>
            <DialogTitle>New sandbox from template</DialogTitle>
            <DialogDescription>
              Start with the template image and resources. The workspace and
              Docker disks are fresh and contain no data from earlier sandboxes.
            </DialogDescription>
          </DialogHeader>

          {template.isPending ? (
            <div
              className="grid min-h-24 place-items-center"
              aria-label="Loading template"
            >
              <Spinner />
            </div>
          ) : template.isError ? (
            <div className="space-y-3 rounded-md border p-3" role="alert">
              <div className="space-y-1">
                <h3 className="font-medium">Template unavailable</h3>
                <p className="break-words text-muted-foreground">
                  {errorMessage(template.error)}
                </p>
              </div>
              <Button
                type="button"
                variant="outline"
                size="sm"
                onClick={() => void template.refetch()}
              >
                Retry
              </Button>
            </div>
          ) : (
            <>
              <section
                className="rounded-md border p-3"
                aria-label="Template resources"
              >
                <h3 className="mb-2 font-medium">Template resources</h3>
                <dl className="grid grid-cols-2 gap-x-4 gap-y-2">
                  <div>
                    <dt className="text-muted-foreground">CPU</dt>
                    <dd>
                      {template.data.resources.cpus}{" "}
                      {plural(template.data.resources.cpus, "CPU")}
                    </dd>
                  </div>
                  <div>
                    <dt className="text-muted-foreground">Memory</dt>
                    <dd>
                      {formatMiB(template.data.resources.memoryMiB)}
                      {template.data.resources.maxMemoryMiB !==
                        template.data.resources.memoryMiB &&
                        ` (up to ${formatMiB(template.data.resources.maxMemoryMiB)})`}
                    </dd>
                  </div>
                  <div>
                    <dt className="text-muted-foreground">Workspace disk</dt>
                    <dd>{formatMiB(template.data.resources.workspaceMiB)}</dd>
                  </div>
                  <div>
                    <dt className="text-muted-foreground">Docker disk</dt>
                    <dd>{formatMiB(template.data.resources.dockerMiB)}</dd>
                  </div>
                </dl>
              </section>

              <Field data-invalid={invalid || undefined}>
                <FieldLabel htmlFor="template-sandbox-name">Name</FieldLabel>
                <Input
                  id="template-sandbox-name"
                  autoFocus
                  autoComplete="off"
                  placeholder="my-project"
                  value={name}
                  aria-invalid={invalid}
                  disabled={pending}
                  onChange={(event) => {
                    setName(event.target.value.toLowerCase())
                    setServerError(null)
                  }}
                />
                {invalid ? (
                  <FieldError>
                    Lowercase letters, digits and dashes; up to 40 characters.
                  </FieldError>
                ) : (
                  <FieldDescription>
                    Also the sandbox&apos;s hostname.
                  </FieldDescription>
                )}
              </Field>
              {serverError && (
                <p role="alert" className="text-destructive">
                  Could not create the sandbox: {serverError}
                </p>
              )}
            </>
          )}

          <DialogFooter>
            <Button
              type="button"
              variant="outline"
              disabled={pending}
              onClick={() => handleOpenChange(false)}
            >
              Close
            </Button>
            <Button
              type="submit"
              disabled={
                !template.data ||
                template.isError ||
                !name ||
                invalid ||
                pending
              }
            >
              {pending && <Spinner />}
              Create sandbox
            </Button>
          </DialogFooter>
        </form>
      </DialogContent>
    </Dialog>
  )
}

function plural(count: number, word: string) {
  return count === 1 ? word : `${word}s`
}

function formatMiB(mib: number) {
  return mib % 1024 === 0 ? `${mib / 1024} GiB` : `${mib} MiB`
}
