import { useEffect, useState } from "react"
import { ImageIcon, WarningIcon } from "@phosphor-icons/react"
import { Button } from "@/components/ui/button"
import {
  Dialog,
  DialogContent,
  DialogTitle,
  DialogTrigger,
} from "@/components/ui/dialog"

/**
 * Whether src is a file on the computer, which Studio copies out of it, and not a picture on
 * the web or in the text, as the chat service tells them apart (internal/chat/images.go).
 */
export function isComputerPicture(src: string) {
  const s = src.replace(/^file:\/\//, "")
  return (
    s !== "" &&
    !s.includes("://") &&
    !s.startsWith("//") &&
    !s.startsWith("data:")
  )
}

export function chatPictureURL(
  env: string,
  chatId: string,
  itemId: string,
  src: string
) {
  return `/api/environments/${encodeURIComponent(env)}/chats/${encodeURIComponent(chatId)}/items/${encodeURIComponent(itemId)}/image?src=${encodeURIComponent(src)}`
}

type Shown = { url: string; picture?: string; error?: string }

/**
 * A picture an agent's message shows, as Studio copied it out of the computer; a click shows it
 * large. While the message is being written it waits, as the copy is made once it is. One that
 * can't be shown says why, with a way to try again, as after starting a stopped computer.
 */
export function ChatPicture({
  url,
  src,
  alt,
  writing,
}: {
  url: string
  src: string
  alt: string
  writing: boolean
}) {
  const [attempt, setAttempt] = useState(0)
  const at = attempt ? `${url}&try=${attempt}` : url
  const [shown, setShown] = useState<Shown | null>(null)
  useEffect(() => {
    if (writing) return
    let gone = false
    let picture = ""
    fetch(at)
      .then(async (r) => {
        if (!r.ok) {
          const error =
            (await r.text()).trim() || `Studio answered ${r.status}.`
          if (!gone) setShown({ url: at, error })
          return
        }
        const blob = await r.blob()
        if (gone) return
        picture = URL.createObjectURL(blob)
        setShown({ url: at, picture })
      })
      .catch(() => {
        if (!gone) setShown({ url: at, error: "Studio couldn't be reached." })
      })
    return () => {
      gone = true
      if (picture) URL.revokeObjectURL(picture)
    }
  }, [at, writing])
  const now = shown?.url === at ? shown : null
  const caption = alt && (
    <span className="block text-meta text-muted-foreground">{alt}</span>
  )

  if (writing || !now)
    return (
      <span className="my-1 grid w-fit max-w-full gap-1">
        <span
          aria-label={
            writing
              ? `${alt || src}, shown once the message is written`
              : `Loading ${alt || src}`
          }
          className="flex h-32 w-56 max-w-full animate-pulse items-center justify-center rounded-md border bg-muted/50 text-muted-foreground"
        >
          <ImageIcon className="size-5" />
        </span>
        {caption}
      </span>
    )
  if (now.error)
    return (
      <span
        role="note"
        className="my-1 flex w-fit max-w-full items-start gap-2 rounded-md border bg-muted/40 px-2.5 py-2 text-muted-foreground"
      >
        <WarningIcon className="mt-0.5 size-3.5 shrink-0" />
        <span className="grid min-w-0 gap-1">
          <span className="break-words text-foreground">
            {alt ? `${alt}: ` : ""}
            {now.error}
          </span>
          <span>
            <Button
              variant="outline"
              size="xs"
              onClick={() => setAttempt((a) => a + 1)}
            >
              Try again
            </Button>
          </span>
        </span>
      </span>
    )
  return (
    <span className="my-1 grid w-fit max-w-full gap-1">
      <Dialog>
        <DialogTrigger
          render={
            <button
              type="button"
              title={`${alt || src}: click to see it large`}
              className="block w-fit max-w-full cursor-zoom-in overflow-hidden rounded-md border bg-muted/30 outline-none focus-visible:ring-2 focus-visible:ring-ring/50"
            />
          }
        >
          <img
            src={now.picture}
            alt={alt || src}
            className="block max-h-80 max-w-full object-contain"
          />
        </DialogTrigger>
        <LargePicture src={now.picture} alt={alt || src} />
      </Dialog>
      {caption}
    </span>
  )
}

/** A picture shown large, in a dialog its thumbnail opens. */
export function LargePicture({ src, alt }: { src?: string; alt: string }) {
  return (
    // As wide as the picture, up to the screen's width: with w-auto the dialog, fixed at the
    // screen's middle, would only have the right half of the screen to fit into.
    <DialogContent bare className="w-max p-2 pt-10 sm:max-w-[calc(100vw-4rem)]">
      <DialogTitle className="sr-only">{alt}</DialogTitle>
      <img
        src={src}
        alt={alt}
        className="block max-h-[calc(100dvh-6rem)] max-w-full rounded-md object-contain"
      />
    </DialogContent>
  )
}
