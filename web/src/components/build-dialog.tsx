import { type FormEvent, useState } from "react"
import { useQueryClient } from "@tanstack/react-query"
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
import { Spinner } from "@/components/ui/spinner"
import { errorMessage, fetchClient, type BuildJob } from "@/lib/api/client"

const maxSourceBytes = 65_536

export const exampleTemplateSource = `resources:
  cpus: 1
  memory: 512MiB
  max_memory: 1024MiB
  workspace: 1024MiB
  docker: 1024MiB
setup: |
  printf 'Template ready\\n'
`

export function BuildDialog({
  env,
  open,
  source,
  onOpenChange,
  onSubmitted,
}: {
  env: string
  open: boolean
  source: string
  onOpenChange: (open: boolean) => void
  onSubmitted: (job: BuildJob) => void
}) {
  const [draft, setDraft] = useState(source)
  const [serverError, setServerError] = useState<string | null>(null)
  const [pending, setPending] = useState(false)
  const queryClient = useQueryClient()
  const sourceBytes = new TextEncoder().encode(draft).byteLength
  const sizeError =
    sourceBytes > maxSourceBytes
      ? "The template spec must be at most 65,536 UTF-8 bytes."
      : null
  const visibleError = sizeError ?? serverError

  async function submit(event: FormEvent<HTMLFormElement>) {
    event.preventDefault()
    if (pending) return
    if (!draft) {
      setServerError("Enter a template spec to build.")
      return
    }
    if (sizeError) return

    setServerError(null)
    setPending(true)
    try {
      const result = await fetchClient.POST("/api/environments/{env}/builds", {
        params: { path: { env } },
        body: { source: draft },
      })
      if (result.error) {
        setServerError(errorMessage(result.error))
        return
      }
      await queryClient.invalidateQueries({
        queryKey: ["get", "/api/environments/{env}/builds"],
      })
      onSubmitted(result.data)
    } catch (error) {
      setServerError(errorMessage(error))
    } finally {
      setPending(false)
    }
  }

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent className="max-h-[calc(100dvh-2rem)] overflow-y-auto sm:max-w-2xl">
        <form className="contents" onSubmit={submit}>
          <DialogHeader>
            <DialogTitle>New template build</DialogTitle>
            <DialogDescription>
              Build a reusable template from a YAML spec. A ready build does not
              start a sandbox.
            </DialogDescription>
          </DialogHeader>
          <Field data-invalid={Boolean(visibleError) || undefined}>
            <FieldLabel htmlFor="build-source">Template spec</FieldLabel>
            <textarea
              id="build-source"
              autoFocus
              autoComplete="off"
              spellCheck={false}
              maxLength={maxSourceBytes}
              value={draft}
              aria-invalid={Boolean(visibleError)}
              aria-describedby="build-source-help"
              onChange={(event) => {
                setDraft(event.target.value)
                setServerError(null)
              }}
              className="min-h-64 w-full resize-y rounded-md border border-input bg-input/20 p-3 font-mono text-xs/relaxed transition-colors outline-none placeholder:text-muted-foreground focus-visible:border-ring focus-visible:ring-2 focus-visible:ring-ring/30 aria-invalid:border-destructive aria-invalid:ring-2 aria-invalid:ring-destructive/20 dark:bg-input/30"
            />
            <FieldDescription id="build-source-help">
              {sourceBytes.toLocaleString()} of 65,536 UTF-8 bytes. Tool version
              values, if present, must be quoted strings.
            </FieldDescription>
            <FieldError>{visibleError}</FieldError>
          </Field>
          <DialogFooter>
            <Button
              type="button"
              variant="outline"
              disabled={pending}
              onClick={() => onOpenChange(false)}
            >
              Close
            </Button>
            <Button
              type="submit"
              disabled={!draft || Boolean(sizeError) || pending}
            >
              {pending && <Spinner />}
              Build template
            </Button>
          </DialogFooter>
        </form>
      </DialogContent>
    </Dialog>
  )
}
