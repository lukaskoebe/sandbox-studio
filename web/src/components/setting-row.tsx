import { useState, type ReactNode } from "react"
import type { Icon } from "@phosphor-icons/react"
import { tileClass, type Area } from "@/lib/accents"
import { cn } from "@/lib/utils"

/**
 * A group of settings: a mono label, then its rows in one bordered card with rules between
 * them. `label` names the group, such as "Network"; a page with one group leaves it out, as the
 * page's title already says it.
 */
export function SettingsGroup({
  label,
  aside,
  children,
  className,
}: {
  label?: string
  aside?: ReactNode
  children: ReactNode
  className?: string
}) {
  return (
    <section aria-label={label} className={cn("grid gap-2", className)}>
      {(label || aside) && (
        <div className="flex h-5 items-center gap-2">
          {label && (
            <h2 className="section-label text-muted-foreground">{label}</h2>
          )}
          {aside}
        </div>
      )}
      <div className="divide-y overflow-hidden rounded-(--radius) border bg-card">
        {children}
      </div>
    </section>
  )
}

const controlRows = ["", "sm:row-span-1", "sm:row-span-2", "sm:row-span-3"]

/**
 * One setting: an icon tile, its title and what it does on the left, its control on the
 * right. Anything under the description (a state, a path, a warning) goes in `children`. On
 * phones the control shares the title's row, and a description longer than a sentence keeps
 * its first one, with the rest behind More.
 */
export function SettingRow({
  icon: IconComp,
  area,
  title,
  titleHidden,
  description,
  control,
  children,
}: {
  icon?: Icon
  area?: Area
  title: string
  /** The page's title already says it, such as Updates on the Updates page. */
  titleHidden?: boolean
  description?: ReactNode
  control?: ReactNode
  children?: ReactNode
}) {
  const rows = 1 + (description && !titleHidden ? 1 : 0) + (children ? 1 : 0)
  return (
    <div role="group" aria-label={title} className="flex gap-3 px-4 py-3">
      {IconComp && (
        <span
          className={cn(
            "mt-px inline-flex size-5 shrink-0 items-center justify-center rounded-sm",
            tileClass(area)
          )}
        >
          <IconComp weight="bold" className="size-3" aria-hidden />
        </span>
      )}
      <div className="grid min-w-0 flex-1 content-center gap-y-1 sm:grid-cols-[minmax(0,1fr)_auto] sm:gap-x-6">
        {/* On phones the title and the control share a row, which wraps the control under
            the title when both don't fit; wider, the control sits right of the whole row. */}
        <div className="flex flex-wrap items-center justify-between gap-x-3 gap-y-2 sm:contents">
          <h3
            className={cn(
              "text-title leading-5 font-semibold tracking-tight sm:col-start-1 sm:row-start-1 sm:self-center",
              titleHidden && "sr-only"
            )}
          >
            {title}
          </h3>
          {control && (
            <div
              className={cn(
                "flex flex-wrap items-center gap-2 sm:col-start-2 sm:row-start-1 sm:justify-end sm:self-center",
                controlRows[rows]
              )}
            >
              {control}
            </div>
          )}
        </div>
        {description && (
          <Description
            className={cn(
              "max-w-xl text-xs/relaxed text-muted-foreground sm:col-start-1",
              // Without a title, the description takes its place beside the control.
              titleHidden && "max-sm:row-start-1 sm:row-start-1"
            )}
          >
            {description}
          </Description>
        )}
        {children && (
          <div className="grid gap-1.5 text-xs sm:col-start-1">{children}</div>
        )}
      </div>
    </div>
  )
}

/**
 * A setting's description. Written as text with more than one sentence, a phone shows the
 * first and a More button for the rest.
 */
function Description({
  children,
  className,
}: {
  children: ReactNode
  className?: string
}) {
  const [more, setMore] = useState(false)
  const cut = typeof children === "string" ? children.search(/\. \S/) : -1
  if (cut < 0) return <p className={className}>{children}</p>
  const text = children as string
  return (
    <p className={className}>
      {text.slice(0, cut + 1)}
      <span className={cn(!more && "max-sm:hidden")}>
        {text.slice(cut + 1)}
      </span>
      {!more && (
        <>
          {" "}
          <button
            type="button"
            onClick={() => setMore(true)}
            className="font-medium text-foreground underline underline-offset-4 sm:hidden"
          >
            More
          </button>
        </>
      )}
    </p>
  )
}
