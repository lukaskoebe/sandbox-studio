import { useEffect, useRef, useState } from "react"
import { FileIcon, ImageIcon, XIcon } from "@phosphor-icons/react"
import { LargePicture } from "@/components/chat/chat-picture"
import {
  Attachment,
  AttachmentAction,
  AttachmentActions,
  AttachmentContent,
  AttachmentDescription,
  AttachmentGroup,
  AttachmentMedia,
  AttachmentTitle,
  AttachmentTrigger,
} from "@/components/ui/attachment"
import { Dialog, DialogTrigger } from "@/components/ui/dialog"
import { cn, formatBytes } from "@/lib/utils"

/** A file of a message, sent or about to be; picture is where its thumbnail loads from. */
export type ShownFile = {
  name: string
  mimeType: string
  size: number
  picture?: string
}

/** The pictures the thread and the composer show as thumbnails, as Studio keeps them. */
export const pictureTypes = [
  "image/png",
  "image/jpeg",
  "image/gif",
  "image/webp",
]

/**
 * The files of a message: a picture as a small thumbnail, which a click shows large, and any
 * other file, or a picture that won't load, as a chip with its name and size. With onRemove
 * each has an ✕, as in the composer.
 */
export function AttachmentList({
  files,
  onRemove,
  className,
}: {
  files: ShownFile[]
  onRemove?: (i: number) => void
  className?: string
}) {
  return (
    <AttachmentGroup className={cn("items-center", className)}>
      {files.map((f, i) => (
        <Shown
          key={`${f.name}-${i}`}
          file={f}
          onRemove={onRemove && (() => onRemove(i))}
        />
      ))}
    </AttachmentGroup>
  )
}

function Shown({
  file: f,
  onRemove,
}: {
  file: ShownFile
  onRemove?: () => void
}) {
  const [broken, setBroken] = useState<string>()
  const remove = onRemove && (
    <AttachmentActions className="group-data-[orientation=vertical]/attachment:top-1 group-data-[orientation=vertical]/attachment:right-1">
      <AttachmentAction
        aria-label={`Remove ${f.name}`}
        variant={f.picture ? "secondary" : "ghost"}
        className={cn(f.picture && "size-5 rounded-full shadow-xs")}
        onClick={onRemove}
      >
        <XIcon />
      </AttachmentAction>
    </AttachmentActions>
  )
  if (f.picture && broken !== f.picture) {
    const label = `${f.name}, ${formatBytes(f.size)}`
    return (
      // At size sm a vertical attachment's picture stays 32px wide.
      <Attachment orientation="vertical" className="w-18" title={label}>
        <AttachmentMedia variant="image">
          <img
            src={f.picture}
            alt={f.name}
            onError={() => setBroken(f.picture)}
          />
        </AttachmentMedia>
        <Dialog>
          <DialogTrigger
            render={
              <AttachmentTrigger
                aria-label={`Show ${f.name} large`}
                className="cursor-zoom-in rounded-lg focus-visible:ring-2 focus-visible:ring-ring/50"
              />
            }
          />
          <LargePicture src={f.picture} alt={f.name} />
        </Dialog>
        {remove}
      </Attachment>
    )
  }
  return (
    <Attachment size="sm">
      <AttachmentMedia>
        {f.mimeType.startsWith("image/") ? <ImageIcon /> : <FileIcon />}
      </AttachmentMedia>
      <AttachmentContent>
        <AttachmentTitle>{f.name}</AttachmentTitle>
        <AttachmentDescription>{formatBytes(f.size)}</AttachmentDescription>
      </AttachmentContent>
      {remove}
    </Attachment>
  )
}

/**
 * Where each picture about to be sent shows from: an object URL made once per file, revoked
 * once the file is removed or sent. Other files have none.
 */
export function useFilePictures(files: File[]): (string | undefined)[] {
  const made = useRef(new Map<File, string>())
  const [, shown] = useState(0)
  useEffect(() => {
    const m = made.current
    let changed = false
    for (const f of files)
      if (pictureTypes.includes(f.type) && !m.has(f)) {
        m.set(f, URL.createObjectURL(f))
        changed = true
      }
    for (const [f, u] of m)
      if (!files.includes(f)) {
        URL.revokeObjectURL(u)
        m.delete(f)
        changed = true
      }
    if (changed) shown((n) => n + 1)
  }, [files])
  useEffect(() => {
    const m = made.current
    return () => {
      for (const u of m.values()) URL.revokeObjectURL(u)
      m.clear()
    }
  }, [])
  // The map only changes in the effects, which render again when it does.
  return files.map((f) => made.current.get(f))
}
