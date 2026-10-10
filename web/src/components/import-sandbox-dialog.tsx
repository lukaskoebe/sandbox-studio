import { useEffect, useRef, useState } from "react"
import { useQueryClient } from "@tanstack/react-query"
import { Link } from "@tanstack/react-router"
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
import type { components } from "@/lib/api/schema.gen"
import { $api, errorMessage, fetchClient } from "@/lib/api/client"

type SandboxImport = components["schemas"]["SandboxImport"]

const stateLabel: Record<SandboxImport["state"], string> = {
  uploading: "Uploading",
  building: "Building the template",
  creating: "Creating the sandbox",
  importing: "Copying the workspace",
  ready: "Ready",
  failed: "Failed",
}

/** Uploads with XMLHttpRequest, since fetch cannot report upload progress. */
function upload(
  env: string,
  file: File,
  onProgress: (fraction: number) => void,
  signal: AbortSignal
): Promise<SandboxImport> {
  return new Promise((resolve, reject) => {
    const xhr = new XMLHttpRequest()
    xhr.open(
      "POST",
      `/api/environments/${encodeURIComponent(env)}/sandbox-imports`
    )
    xhr.setRequestHeader("Content-Type", "application/octet-stream")
    xhr.responseType = "json"
    xhr.upload.onprogress = (e) => {
      if (e.lengthComputable) onProgress(e.loaded / e.total)
    }
    xhr.onload = () => {
      if (xhr.status === 401)
        window.dispatchEvent(new Event("studio-auth-expired"))
      if (xhr.status === 202) resolve(xhr.response as SandboxImport)
      else reject(xhr.response ?? new Error(`Upload failed (${xhr.status})`))
    }
    xhr.onerror = () => reject(new Error("The upload was interrupted"))
    xhr.onabort = () => reject(new Error("The upload was cancelled"))
    signal.addEventListener("abort", () => xhr.abort())
    xhr.send(file)
  })
}

export function ImportSandboxDialog({
  env,
  open,
  onOpenChange,
}: {
  env: string
  open: boolean
  onOpenChange: (open: boolean) => void
}) {
  const [file, setFile] = useState<File | null>(null)
  const [progress, setProgress] = useState<number | null>(null)
  const [error, setError] = useState<string | null>(null)
  const [importId, setImportId] = useState<string | null>(null)
  const abort = useRef<AbortController | null>(null)
  const queryClient = useQueryClient()

  const status = $api.useQuery(
    "get",
    "/api/environments/{env}/sandbox-imports/{id}",
    { params: { path: { env, id: importId ?? "" } } },
    {
      enabled: importId !== null,
      refetchInterval: (q) =>
        q.state.data?.state === "ready" || q.state.data?.state === "failed"
          ? false
          : 1000,
    }
  )
  const state = status.data?.state
  useEffect(() => {
    if (state === "ready")
      queryClient.invalidateQueries({
        queryKey: ["get", "/api/environments/{env}/sandboxes"],
      })
  }, [state, queryClient])

  const percent = Math.round((progress ?? 0) * 100)
  const uploading = progress !== null && importId === null && error === null
  const working =
    uploading ||
    (importId !== null && !state) ||
    (state !== undefined && state !== "ready" && state !== "failed")

  function reset() {
    abort.current?.abort()
    abort.current = null
    setFile(null)
    setProgress(null)
    setError(null)
    setImportId(null)
  }

  async function start() {
    if (!file) return
    const controller = new AbortController()
    abort.current = controller
    setError(null)
    setProgress(0)
    try {
      const imp = await upload(env, file, setProgress, controller.signal)
      setImportId(imp.id)
    } catch (err) {
      if (!controller.signal.aborted) setError(errorMessage(err))
    }
  }

  async function cancel() {
    if (importId) {
      await fetchClient.POST(
        "/api/environments/{env}/sandbox-imports/{id}/cancel",
        { params: { path: { env, id: importId } } }
      )
      status.refetch()
    } else {
      reset()
    }
  }

  return (
    <Dialog
      open={open}
      onOpenChange={(next) => {
        // Closing during an upload abandons it; a started import keeps running.
        if (!next) reset()
        onOpenChange(next)
      }}
    >
      <DialogContent>
        <form
          className="contents"
          onSubmit={(e) => {
            e.preventDefault()
            start()
          }}
        >
          <DialogHeader>
            <DialogTitle>Import sandbox</DialogTitle>
            <DialogDescription>
              Create a sandbox from a .studio-sandbox file exported from this or
              another Studio. Its template is rebuilt here if needed.
            </DialogDescription>
          </DialogHeader>
          <Field
            data-invalid={error !== null || state === "failed" || undefined}
          >
            <FieldLabel htmlFor="import-file">Export file</FieldLabel>
            <Input
              id="import-file"
              type="file"
              accept=".studio-sandbox"
              disabled={working || importId !== null}
              onChange={(e) => {
                setFile(e.target.files?.[0] ?? null)
                setError(null)
              }}
            />
            {error !== null ? (
              <FieldError>{error}</FieldError>
            ) : state === "failed" ? (
              <FieldError>
                {status.data?.error ?? "The import failed"}
              </FieldError>
            ) : state === "ready" && status.data?.sandboxId ? (
              <FieldDescription>
                Imported as{" "}
                <Link
                  to="/e/$env/sandboxes/$id"
                  params={{ env, id: status.data.sandboxId }}
                  className="underline underline-offset-4 hover:text-foreground"
                  onClick={() => onOpenChange(false)}
                >
                  {status.data.name}
                </Link>
                .
              </FieldDescription>
            ) : uploading ? (
              <FieldDescription>Uploading… {percent}%</FieldDescription>
            ) : state ? (
              <FieldDescription>{stateLabel[state]}…</FieldDescription>
            ) : (
              <FieldDescription>
                Secrets and checkpoints are not part of an export.
              </FieldDescription>
            )}
          </Field>
          <DialogFooter>
            {working && (
              <Button type="button" variant="outline" onClick={cancel}>
                Cancel import
              </Button>
            )}
            {state === "ready" || state === "failed" ? (
              <Button type="button" onClick={reset}>
                Import another
              </Button>
            ) : (
              <Button type="submit" disabled={!file || working}>
                {working && <Spinner />}
                Import
              </Button>
            )}
          </DialogFooter>
        </form>
      </DialogContent>
    </Dialog>
  )
}
