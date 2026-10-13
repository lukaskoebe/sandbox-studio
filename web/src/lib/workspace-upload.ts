import { errorMessage } from "@/lib/api/client"
import type { components } from "@/lib/api/schema.gen"

/** The largest file an upload takes; the server refuses more. */
export const maxUploadBytes = 100 << 20

/** What happens when the name is taken in the folder: refuse, replace, or keep both. */
export type UploadConflict = "fail" | "replace" | "keep"

type UploadedFile = components["schemas"]["UploadedFile"]

/**
 * Uploads one file into a folder of a sandbox's workspace. It uses XMLHttpRequest, since fetch
 * cannot report upload progress. A refusal rejects with the server's message.
 */
export function uploadWorkspaceFile(
  env: string,
  sandboxId: string,
  folder: string,
  file: File,
  conflict: UploadConflict,
  onProgress: (percent: number) => void
): Promise<UploadedFile> {
  return new Promise((resolve, reject) => {
    const query = new URLSearchParams({
      path: folder,
      name: file.name,
      conflict,
    })
    const xhr = new XMLHttpRequest()
    xhr.open(
      "POST",
      `/api/environments/${encodeURIComponent(env)}/sandboxes/${encodeURIComponent(sandboxId)}/files?${query}`
    )
    xhr.setRequestHeader("Content-Type", "application/octet-stream")
    xhr.responseType = "json"
    xhr.upload.onprogress = (e) => {
      if (e.lengthComputable) onProgress((e.loaded / e.total) * 100)
    }
    xhr.onload = () => {
      if (xhr.status === 401)
        window.dispatchEvent(new Event("studio-auth-expired"))
      if (xhr.status === 201) resolve(xhr.response as UploadedFile)
      else
        reject(
          new Error(
            xhr.response
              ? errorMessage(xhr.response)
              : `Upload failed (${xhr.status})`
          )
        )
    }
    xhr.onerror = () => reject(new Error("The upload was interrupted"))
    xhr.send(file)
  })
}
