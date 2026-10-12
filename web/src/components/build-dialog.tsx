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
import { TemplateSpecEditor } from "@/components/template-spec-editor"
import { errorMessage, fetchClient, type BuildJob } from "@/lib/api/client"
import {
  decodeTemplateSource,
  downloadTemplateSource,
  isTemplateSourceDownloadable,
  maxTemplateSourceBytes,
  TemplateSourceError,
  templateSourceByteLength,
} from "@/lib/template-source"

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
  // Bumped when an import replaces the draft, so the editor is rebuilt from the file's exact bytes.
  const [editorKey, setEditorKey] = useState(0)
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
      setEditorKey((key) => key + 1)
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
      <DialogContent className="sm:max-w-2xl" showCloseButton={!busy}>
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
              <FieldLabel>Template spec</FieldLabel>
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
            <TemplateSpecEditor
              key={editorKey}
              env={env}
              value={draft}
              invalid={Boolean(visibleError)}
              disabled={busy}
              describedBy="build-source-help"
              onChange={handleDraftChange}
            />
            <FieldDescription id="build-source-help">
              {sourceBytes.toLocaleString()} of 65,536 UTF-8 bytes. Problems are
              marked as you type. Tool version values must be quoted strings.
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
