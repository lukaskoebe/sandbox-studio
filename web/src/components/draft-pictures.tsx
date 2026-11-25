import { useState } from "react"
import { ImageBrokenIcon } from "@phosphor-icons/react"
import type { components } from "@/lib/api/schema.gen"

type Picture = components["schemas"]["Picture"]

/** Where the card finds Studio's copy of the nth picture of an approval. */
export function draftPictureUrl(env: string, approval: string, n: number) {
  return `/api/environments/${encodeURIComponent(env)}/approvals/${encodeURIComponent(approval)}/pictures/${n}`
}

/**
 * The pictures a teammate's pull request, comment or review shows: Studio copied them out of its
 * computer when it asked, `{{attach:<path>}}` in the text places each, and those the text still
 * places upload with the user's account once approved. Once decided the copies are gone, and
 * only their names stay.
 */
export function DraftPictures({
  env,
  approval,
  pictures,
  pending,
}: {
  env: string
  approval: string
  pictures?: Picture[] | null
  pending: boolean
}) {
  if (!pictures?.length) return null
  return (
    <section aria-label="Pictures" className="grid gap-1.5 text-xs">
      <span className="font-medium">Pictures</span>
      {pending && (
        <p className="text-muted-foreground">
          Each uploads with your account where the text places it, as{" "}
          <code className="font-mono">{"{{attach:…}}"}</code>. Delete a
          placeholder to leave its picture out.
        </p>
      )}
      <ul className="flex flex-wrap gap-2">
        {pictures.map((p, i) => (
          <li key={p.path} className="grid w-36 min-w-0 gap-1">
            {pending ? (
              <Thumb src={draftPictureUrl(env, approval, i)} path={p.path} />
            ) : null}
            <code
              className="truncate font-mono text-caption text-muted-foreground"
              title={p.path}
            >
              {p.path.split("/").at(-1)}
            </code>
          </li>
        ))}
      </ul>
    </section>
  )
}

function Thumb({ src, path }: { src: string; path: string }) {
  const [broken, setBroken] = useState(false)
  const box =
    "grid h-24 w-36 place-items-center overflow-hidden rounded-md bg-muted/40 ring-1 ring-foreground/10"
  if (broken) {
    return (
      <div className={box} title={`Studio's copy of ${path} is gone`}>
        <ImageBrokenIcon className="size-5 text-muted-foreground" />
      </div>
    )
  }
  return (
    <a href={src} target="_blank" rel="noreferrer" className={box}>
      <img
        src={src}
        alt={path}
        loading="lazy"
        className="max-h-full max-w-full object-contain"
        onError={() => setBroken(true)}
      />
    </a>
  )
}
