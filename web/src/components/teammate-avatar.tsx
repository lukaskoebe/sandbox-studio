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

/** A teammate's initials on a color derived from its name. */
export function TeammateAvatar({
  name,
  className,
}: {
  name: string
  className?: string
}) {
  return (
    <span
      aria-hidden
      className={cn(
        "inline-flex size-6 shrink-0 items-center justify-center rounded-full text-[0.625rem] font-semibold text-white select-none",
        className
      )}
      style={{ backgroundColor: `oklch(0.55 0.13 ${hue(name)})` }}
    >
      {initials(name)}
    </span>
  )
}

const presenceDots: Record<Presence, string> = {
  working: "bg-sky-500 animate-pulse",
  waiting: "bg-amber-500",
  idle: "bg-muted-foreground/40",
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
  const name = teammates.data?.find((p) => p.id === teammateId)?.name ?? "…"
  return (
    <span
      className={cn("inline-flex min-w-0 items-center gap-1.5", className)}
      title={`Owned by ${name}`}
    >
      <TeammateAvatar name={name} className="size-5 text-[0.5625rem]" />
      <span className="truncate">{name}</span>
    </span>
  )
}
