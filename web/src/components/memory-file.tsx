import { useRef, type ChangeEvent } from "react"
import { useQueryClient } from "@tanstack/react-query"
import { toast } from "sonner"
import { DownloadSimpleIcon, UploadSimpleIcon } from "@phosphor-icons/react"
import { Button } from "@/components/ui/button"
import { Spinner } from "@/components/ui/spinner"
import { invalidateMemory } from "@/components/memory-dialogs"
import { $api, errorMessage, fetchClient } from "@/lib/api/client"

/** The largest memory file an import takes; the server checks it again. */
const maxImportBytes = 2 << 20

/** Exports a scope's memory as one markdown file, and imports such a file back into the scope. */
export function MemoryFileActions({
  env,
  scope,
}: {
  env: string
  scope: string
}) {
  const queryClient = useQueryClient()
  const picker = useRef<HTMLInputElement>(null)
  const importMemory = $api.useMutation(
    "post",
    "/api/environments/{env}/memory/import",
    {
      onSuccess: (r) =>
        toast.success("Memory imported", {
          description: [
            `${r.pages} pages and ${r.facts} facts added; ${r.skipped} were there already.`,
            ...(r.notes ?? []),
          ].join(" "),
        }),
      onSettled: () => invalidateMemory(queryClient),
      onError: (err) =>
        toast.error("Could not import the memory file", {
          description: errorMessage(err),
        }),
    }
  )

  const exportMemory = async () => {
    const result = await fetchClient.GET(
      "/api/environments/{env}/memory/export",
      {
        params: { path: { env }, query: { scope } },
      }
    )
    if (result.error) {
      toast.error("Could not export the memory", {
        description: errorMessage(result.error),
      })
      return
    }
    downloadMarkdown(result.data.filename, result.data.markdown)
  }

  const onFile = async (e: ChangeEvent<HTMLInputElement>) => {
    const file = e.target.files?.[0]
    e.target.value = ""
    if (!file) return
    if (file.size > maxImportBytes) {
      toast.error("Could not import the memory file", {
        description: "The file is larger than 2 MiB.",
      })
      return
    }
    importMemory.mutate({
      params: { path: { env } },
      body: { scope, markdown: await file.text() },
    })
  }

  return (
    <div className="flex flex-wrap gap-2">
      <Button variant="outline" size="sm" onClick={() => void exportMemory()}>
        <DownloadSimpleIcon />
        Export
      </Button>
      <Button
        variant="outline"
        size="sm"
        disabled={importMemory.isPending}
        onClick={() => picker.current?.click()}
      >
        {importMemory.isPending ? <Spinner /> : <UploadSimpleIcon />}
        Import
      </Button>
      <input
        ref={picker}
        type="file"
        accept=".md,text/markdown"
        className="hidden"
        aria-label="Memory file"
        onChange={(e) => void onFile(e)}
      />
    </div>
  )
}

function downloadMarkdown(filename: string, markdown: string) {
  const url = URL.createObjectURL(
    new Blob([markdown], { type: "text/markdown;charset=utf-8" })
  )
  const link = document.createElement("a")
  link.href = url
  link.download = filename
  link.hidden = true
  document.body.appendChild(link)
  link.click()
  link.remove()
  URL.revokeObjectURL(url)
}
