import { presenceLabels, type Presence } from "@/hooks/use-teammate-presence"
import { cn } from "@/lib/utils"
import { $api } from "@/lib/api/client"

/** Up to two initials: the first letters of the first and last words. */
function initials(name: string) {
  const words = name
    .trim()
    .split(/[\s._-]+/)
    .filter(Boolean)
  const letters =
    words.length > 1 ? [words[0], words[words.length - 1]] : words.slice(0, 1)
  return letters.map((w) => Array.from(w)[0]?.toUpperCase() ?? "").join("")
}

/** A hue derived from the name, so a teammate keeps its color everywhere. */
function hue(name: string) {
  let h = 0
  for (const c of name) h = (h * 31 + (c.codePointAt(0) ?? 0)) % 360
  return h
}

/** The generated avatars: fixed shapes in a 24-unit square, drawn in white. */
const shapePaths: Partial<Record<string, string[]>> = {
  circle: ["M12 5a7 7 0 1 0 0 14 7 7 0 1 0 0-14Z"],
  square: ["M5 5h14v14H5Z"],
  diamond: ["M12 3 21 12 12 21 3 12Z"],
  triangle: ["M12 4 21 19H3Z"],
  pentagon: ["M12 4 19.6 9.5 16.7 18.5H7.3L4.4 9.5Z"],
  hexagon: ["M20 12 16 18.9H8L4 12 8 5.1H16Z"],
  star: [
    "M12 3 14.4 8.8 20.6 9.2 15.8 13.2 17.3 19.3 12 16 6.7 19.3 8.2 13.2 3.4 9.2 9.7 8.8Z",
  ],
  cross: ["M10 4h4v6h6v4h-6v6h-4v-6H4v-4h6Z"],
}

/**
 * A teammate's avatar: its emoji or generated shape when it has one, else its initials on a
 * color derived from its name, so the color stays the same everywhere.
 */
export function TeammateAvatar({
  name,
  avatar,
  className,
}: {
  name: string
  /** One emoji, `shape:<name>` for a generated avatar, or empty for the initials. */
  avatar?: string
  className?: string
}) {
  const shape = avatar?.startsWith("shape:")
    ? avatar.slice("shape:".length)
    : undefined
  const emoji = avatar && !shape ? avatar : undefined
  const paths = shape ? shapePaths[shape] : undefined
  return (
    <span
      aria-hidden
      className={cn(
        "inline-flex size-6 shrink-0 items-center justify-center overflow-hidden rounded-full text-[0.625rem] font-semibold text-white select-none",
        emoji && "bg-muted text-foreground",
        className
      )}
      style={
        emoji ? undefined : { backgroundColor: `oklch(0.55 0.13 ${hue(name)})` }
      }
    >
      {emoji ? (
        <span className="text-[1.5em] leading-none">{emoji}</span>
      ) : paths ? (
        <svg viewBox="0 0 24 24" className="size-3/5" fill="currentColor">
          {paths.map((d) => (
            <path key={d} d={d} />
          ))}
        </svg>
      ) : (
        initials(name)
      )}
    </span>
  )
}

/** Presence uses the status colors: working is running, waiting for you is waiting. */
const presenceDots: Record<Presence, string> = {
  working: "bg-(--st-running) animate-pulse",
  waiting: "bg-(--st-waiting)",
  idle: "bg-(--st-stopped)",
  offline: "ring-1 ring-(--st-stopped) ring-inset",
}

/** A teammate's presence as a dot. The words go in the title and beside it where there is room. */
export function PresenceDot({
  presence,
  className,
}: {
  presence: Presence
  className?: string
}) {
  return (
    <span
      aria-hidden
      title={presenceLabels[presence]}
      className={cn(
        "size-2 shrink-0 rounded-full",
        presenceDots[presence],
        className
      )}
    />
  )
}

/** The teammate owning a sandbox, as an avatar and name; nothing for an unowned sandbox. */
export function SandboxOwner({
  env,
  teammateId,
  className,
}: {
  env: string
  teammateId?: string
  className?: string
}) {
  const teammates = $api.useQuery(
    "get",
    "/api/environments/{env}/teammates",
    { params: { path: { env } } },
    { enabled: !!teammateId }
  )
  if (!teammateId) return null
  const owner = teammates.data?.find((p) => p.id === teammateId)
  const name = owner?.name ?? "…"
  return (
    <span
      className={cn("inline-flex min-w-0 items-center gap-1.5", className)}
      title={`Owned by ${name}`}
    >
      <TeammateAvatar
        name={name}
        avatar={owner?.avatar}
        className="size-5 text-[0.5625rem]"
      />
      <span className="truncate">{name}</span>
    </span>
  )
}
