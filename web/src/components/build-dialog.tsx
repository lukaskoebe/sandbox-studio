import {
  type ChangeEvent,
  type FormEvent,
  useEffect,
  useRef,
  useState,
} from "react"
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
import {
  decodeTemplateSource,
  downloadTemplateSource,
  isTemplateSourceDownloadable,
  maxTemplateSourceBytes,
  TemplateSourceError,
  templateSourceByteLength,
} from "@/lib/template-source"

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
  const [sourceActionError, setSourceActionError] = useState<string | null>(
    null
  )
  const [pending, setPending] = useState(false)
  const [importPending, setImportPending] = useState(false)
  const fileInputRef = useRef<HTMLInputElement>(null)
  const importGenerationRef = useRef(0)
  const draftRevisionRef = useRef(0)
  const pendingRef = useRef(false)
  const importPendingRef = useRef(false)
  const mountedRef = useRef(false)
  const openRef = useRef(open)
  openRef.current = open

  useEffect(() => {
    mountedRef.current = true
    return () => {
      mountedRef.current = false
      importGenerationRef.current += 1
      importPendingRef.current = false
    }
  }, [])

  useEffect(() => {
    if (!open) {
      importGenerationRef.current += 1
      importPendingRef.current = false
      setImportPending(false)
    }
  }, [open])

  const queryClient = useQueryClient()
  const sourceBytes = templateSourceByteLength(draft)
  const sizeError =
    sourceBytes > maxTemplateSourceBytes
      ? "The template spec must be at most 65,536 UTF-8 bytes."
      : null
  const visibleError = sourceActionError ?? sizeError ?? serverError
  const busy = pending || importPending

  function handleOpenChange(nextOpen: boolean) {
    if (!nextOpen && (pendingRef.current || importPendingRef.current)) return
    onOpenChange(nextOpen)
  }

  async function importFile(file: File) {
    if (pendingRef.current) return
    const generation = importGenerationRef.current + 1
    importGenerationRef.current = generation
    const draftRevision = draftRevisionRef.current
    importPendingRef.current = true
    setImportPending(true)
    setSourceActionError(null)

    try {
      if (file.size > maxTemplateSourceBytes) {
        throw new TemplateSourceError("oversize")
      }
      const importedSource = decodeTemplateSource(await file.arrayBuffer())
      if (
        generation !== importGenerationRef.current ||
        draftRevision !== draftRevisionRef.current ||
        !mountedRef.current ||
        !openRef.current
      ) {
        return
      }

      setDraft(importedSource)
      draftRevisionRef.current += 1
      setServerError(null)
      setSourceActionError(null)
    } catch (error) {
      if (
        generation === importGenerationRef.current &&
        draftRevision === draftRevisionRef.current &&
        mountedRef.current &&
        openRef.current
      ) {
        setSourceActionError(
          error instanceof TemplateSourceError
            ? error.message
            : "Could not read the YAML file."
        )
      }
    } finally {
      if (generation === importGenerationRef.current && mountedRef.current) {
        importPendingRef.current = false
        setImportPending(false)
      }
    }
  }

  function handleFileChange(event: ChangeEvent<HTMLInputElement>) {
    const file = event.currentTarget.files?.[0]
    // Reset immediately so selecting the same file again fires another change.
    event.currentTarget.value = ""
    if (file) void importFile(file)
  }

  function handleDraftChange(value: string) {
    draftRevisionRef.current += 1
    if (importPendingRef.current) {
      importGenerationRef.current += 1
      importPendingRef.current = false
      setImportPending(false)
    }
    setDraft(value)
    setServerError(null)
    setSourceActionError(null)
  }

  function handleDownload() {
    if (!isTemplateSourceDownloadable(draft) || busy) return
    setSourceActionError(null)
    try {
      downloadTemplateSource(draft)
    } catch {
      setSourceActionError("Could not download the template spec.")
    }
  }

  async function submit(event: FormEvent<HTMLFormElement>) {
    event.preventDefault()
    if (pendingRef.current || importPendingRef.current) return
    if (!draft) {
      setServerError("Enter a template spec to build.")
      return
    }
    if (sizeError) return

    setServerError(null)
    setSourceActionError(null)
    pendingRef.current = true
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
      pendingRef.current = false
      setPending(false)
    }
  }

  return (
    <Dialog open={open} onOpenChange={handleOpenChange}>
      <DialogContent
        className="max-h-[calc(100dvh-2rem)] overflow-y-auto sm:max-w-2xl"
        showCloseButton={!busy}
      >
        <form className="contents" onSubmit={submit}>
          <DialogHeader>
            <DialogTitle>New template build</DialogTitle>
            <DialogDescription>
              Build a reusable template from a YAML spec. A ready build does not
              start a sandbox.
            </DialogDescription>
          </DialogHeader>
          <Field data-invalid={Boolean(visibleError) || undefined}>
            <div className="flex flex-wrap items-center justify-between gap-2">
              <FieldLabel htmlFor="build-source">Template spec</FieldLabel>
              <div className="flex flex-wrap gap-2">
                <input
                  ref={fileInputRef}
                  type="file"
                  accept=".yaml,.yml"
                  multiple={false}
                  className="sr-only"
                  tabIndex={-1}
                  aria-hidden="true"
                  disabled={busy}
                  onChange={handleFileChange}
                />
                <Button
                  type="button"
                  variant="outline"
                  size="sm"
                  disabled={busy}
                  onClick={() => {
                    setSourceActionError(null)
                    fileInputRef.current?.click()
                  }}
                >
                  {importPending && <Spinner />}
                  {importPending ? "Reading YAML…" : "Import YAML"}
                </Button>
                <Button
                  type="button"
                  variant="outline"
                  size="sm"
                  disabled={!isTemplateSourceDownloadable(draft) || busy}
                  onClick={handleDownload}
                >
                  Download YAML
                </Button>
              </div>
            </div>
            <textarea
              id="build-source"
              autoFocus
              autoComplete="off"
              spellCheck={false}
              maxLength={maxTemplateSourceBytes}
              disabled={busy}
              value={draft}
              aria-invalid={Boolean(visibleError)}
              aria-describedby="build-source-help"
              onChange={(event) => handleDraftChange(event.target.value)}
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
              disabled={busy}
              onClick={() => handleOpenChange(false)}
            >
              Close
            </Button>
            <Button
              type="submit"
              disabled={!draft || Boolean(sizeError) || busy}
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
