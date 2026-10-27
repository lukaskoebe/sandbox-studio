import { useState } from "react"
import { toast } from "sonner"
import { DownloadSimpleIcon, PaperPlaneTiltIcon } from "@phosphor-icons/react"
import { Button } from "@/components/ui/button"
import {
  Empty,
  EmptyDescription,
  EmptyHeader,
  EmptyTitle,
} from "@/components/ui/empty"
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select"
import { Spinner } from "@/components/ui/spinner"
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "@/components/ui/table"
import { $api, errorMessage } from "@/lib/api/client"
import { formatAge, formatBytes } from "@/lib/utils"

const downloadsPath =
  "/api/environments/{env}/teammates/{teammate}/browser/downloads"
const sendPath = `${downloadsPath}/send`

/** The files the teammate's browser saved, each sendable to one of the teammate's sandboxes. */
export function BrowserDownloads({
  env,
  teammate,
}: {
  env: string
  teammate: string
}) {
  const path = { env, teammate }
  const downloads = $api.useQuery(
    "get",
    downloadsPath,
    { params: { path } },
    { refetchInterval: 5000 }
  )
  const sandboxes = $api.useQuery(
    "get",
    "/api/environments/{env}/sandboxes",
    { params: { path: { env } } },
    { refetchInterval: 5000 }
  )
  const owned = (sandboxes.data ?? []).filter((s) => s.teammateId === teammate)
  const [target, setTarget] = useState("")
  const send = $api.useMutation("post", sendPath, {
    onSuccess: (file) => {
      const sandbox = owned.find((s) => s.id === target)?.name ?? "the sandbox"
      toast.success(`Sent ${file.name} to ${sandbox}`, {
        description: `It is in ${file.path}`,
      })
    },
    onError: (err) =>
      toast.error("Could not send the download", {
        description: errorMessage(err),
      }),
  })

  if (downloads.isPending) return <Spinner />
  const files = downloads.data ?? []
  if (files.length === 0)
    return (
      <Empty className="max-w-5xl border">
        <EmptyHeader>
          <EmptyTitle>No downloads</EmptyTitle>
          <EmptyDescription>
            Files the browser saves show here while it runs. Send one to a
            sandbox to copy it into its downloads folder; the file is never
            opened on Studio's host.
          </EmptyDescription>
        </EmptyHeader>
      </Empty>
    )

  return (
    <div className="flex max-w-5xl flex-col gap-3">
      <div className="flex flex-wrap items-center gap-2">
        <span className="text-xs text-muted-foreground">Send to</span>
        <Select value={target} onValueChange={(v) => setTarget(v ?? "")}>
          <SelectTrigger size="sm" aria-label="Sandbox to send to">
            <SelectValue placeholder="Choose a sandbox" />
          </SelectTrigger>
          <SelectContent>
            {owned.map((s) => (
              <SelectItem key={s.id} value={s.id}>
                {s.name}
              </SelectItem>
            ))}
          </SelectContent>
        </Select>
      </div>
      <Table>
        <TableHeader>
          <TableRow>
            <TableHead>File</TableHead>
            <TableHead>From</TableHead>
            <TableHead>Size</TableHead>
            <TableHead>Saved</TableHead>
            <TableHead>
              <span className="sr-only">Actions</span>
            </TableHead>
          </TableRow>
        </TableHeader>
        <TableBody>
          {files.map((f) => (
            <TableRow key={`${f.session}/${f.name}`}>
              <TableCell className="max-w-72 truncate font-mono text-xs">
                {f.name}
              </TableCell>
              <TableCell className="text-xs text-muted-foreground">
                {f.session ? "Ephemeral session" : "Profile"}
              </TableCell>
              <TableCell className="text-xs">{formatBytes(f.size)}</TableCell>
              <TableCell className="text-xs text-muted-foreground">
                {formatAge(f.saved)}
              </TableCell>
              <TableCell className="text-right">
                <Button
                  size="sm"
                  variant="outline"
                  disabled={!target || send.isPending}
                  onClick={() =>
                    send.mutate({
                      params: { path },
                      body: {
                        session: f.session,
                        name: f.name,
                        sandboxId: target,
                      },
                    })
                  }
                >
                  {send.isPending ? <Spinner /> : <PaperPlaneTiltIcon />}
                  Send to sandbox
                </Button>
              </TableCell>
            </TableRow>
          ))}
        </TableBody>
      </Table>
      <p className="flex items-center gap-1.5 text-xs text-muted-foreground">
        <DownloadSimpleIcon />
        Listed while the browser runs. Ephemeral sessions' files go when their
        session closes.
      </p>
    </div>
  )
}
