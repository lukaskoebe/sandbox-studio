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

/**
 * Uploads a file pasted or dropped into a sandbox's terminal, outside its workspace, and
 * returns its path in the sandbox, which the terminal pastes for the program to read: an image
 * reaches Claude Code or Codex that way.
 */
export async function pasteFile(
  env: string,
  sandboxId: string,
  file: File
): Promise<string> {
  const query = new URLSearchParams({ name: pasteName(file) })
  const res = await fetch(
    `/api/environments/${encodeURIComponent(env)}/sandboxes/${encodeURIComponent(sandboxId)}/pastes?${query}`,
    {
      method: "POST",
      headers: { "Content-Type": "application/octet-stream" },
      body: file,
    }
  )
  if (res.status === 401) window.dispatchEvent(new Event("studio-auth-expired"))
  const body: unknown = await res.json().catch(() => undefined)
  if (res.status !== 201)
    throw new Error(body ? errorMessage(body) : `Paste failed (${res.status})`)
  return (body as components["schemas"]["PastedFile"]).path
}

const imageExtensions: Partial<Record<string, string>> = {
  "image/png": "png",
  "image/jpeg": "jpg",
  "image/gif": "gif",
  "image/webp": "webp",
}

/**
 * A pasted file's name in the sandbox, safe to paste as a path without quotes. A screenshot
 * from the clipboard is "image.png" in every browser, so images get a name of their own.
 */
function pasteName(file: File) {
  const ext = imageExtensions[file.type]
  if (!ext && file.name)
    return file.name.replace(/[^\w.+-]+/g, "-").replace(/^[.-]+/, "") || "file"
  const stamp = new Date().toISOString().replace(/\D/g, "").slice(0, 14)
  return `paste-${stamp}-${Math.random().toString(36).slice(2, 6)}.${ext ?? "bin"}`
}
