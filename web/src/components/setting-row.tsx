import type { ReactNode } from "react"
import type { Icon } from "@phosphor-icons/react"
import { tileClass, type Area } from "@/lib/accents"
import { cn } from "@/lib/utils"

/**
 * A group of settings: a mono label, then its rows in one bordered card with rules between
 * them. `label` names the group, such as "Studio" or "This environment".
 */
export function SettingsGroup({
  label,
  aside,
  children,
  className,
}: {
  label: string
  aside?: ReactNode
  children: ReactNode
  className?: string
}) {
  return (
    <section aria-label={label} className={cn("grid gap-2", className)}>
      <div className="flex h-5 items-center gap-2">
        <h2 className="font-mono text-[0.625rem] tracking-wider text-muted-foreground uppercase">
          {label}
        </h2>
        {aside}
      </div>
      <div className="divide-y overflow-hidden rounded-(--radius) border bg-card">
        {children}
      </div>
    </section>
  )
}

/**
 * One setting: an icon tile, its title and what it does on the left, its control on the
 * right. Anything under the description (a state, a path, a warning) goes in `children`. On
 * phones the control drops below the text.
 */
export function SettingRow({
  icon: IconComp,
  area,
  title,
  description,
  control,
  children,
}: {
  icon?: Icon
  area?: Area
  title: string
  description?: ReactNode
  control?: ReactNode
  children?: ReactNode
}) {
  return (
    <div
      role="group"
      aria-label={title}
      className="grid gap-x-6 gap-y-2 px-4 py-3 sm:grid-cols-[minmax(0,1fr)_auto] sm:items-center"
    >
      <div className="flex min-w-0 gap-3">
        {IconComp && (
          <span
            className={cn(
              "mt-px inline-flex size-5 shrink-0 items-center justify-center rounded-[4px]",
              tileClass(area)
            )}
          >
            <IconComp weight="bold" className="size-3" aria-hidden />
          </span>
        )}
        <div className="grid min-w-0 gap-1">
          <h3 className="text-[0.8125rem] leading-5 font-semibold tracking-tight">
            {title}
          </h3>
          {description && (
            <p className="max-w-xl text-xs/relaxed text-muted-foreground">
              {description}
            </p>
          )}
          {children && <div className="grid gap-1.5 text-xs">{children}</div>}
        </div>
      </div>
      {control && (
        <div className="flex flex-wrap items-center gap-2 max-sm:pl-8 sm:justify-end">
          {control}
        </div>
      )}
    </div>
  )
}
