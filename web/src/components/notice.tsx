import type { ComponentProps, ReactNode } from "react"
import { cva, type VariantProps } from "class-variance-authority"
import { cn } from "@/lib/utils"

const noticeVariants = cva(
  "flex flex-wrap items-center gap-x-3 gap-y-2 rounded-(--radius) border px-4 py-3 text-xs/relaxed",
  {
    variants: {
      tone: {
        default: "bg-card text-muted-foreground",
        waiting: "border-(--st-waiting)/40 bg-(--st-waiting)/10",
      },
    },
    defaultVariants: { tone: "default" },
  }
)

/**
 * A page's notice above its content: an icon, a sentence or two and what to do about it, such
 * as Home's notifications, previews and base image notices. They share one look, so every
 * notice on a page lines up: the text wraps beside the icon and the actions follow it, or go
 * under it on a phone.
 */
export function Notice({
  icon,
  actions,
  tone,
  className,
  children,
  ...props
}: Omit<ComponentProps<"section">, "children"> &
  VariantProps<typeof noticeVariants> & {
    icon: ReactNode
    /** Buttons and links after the text, the dismiss button last. */
    actions?: ReactNode
    children: ReactNode
  }) {
  return (
    <section
      data-slot="notice"
      className={cn(noticeVariants({ tone }), className)}
      {...props}
    >
      <span
        aria-hidden
        className={cn(
          "flex shrink-0 [&>svg]:size-4",
          tone === "waiting"
            ? "text-(--st-waiting-fg) dark:text-(--st-waiting)"
            : "text-muted-foreground"
        )}
      >
        {icon}
      </span>
      <p className="min-w-48 flex-1">{children}</p>
      {actions && <div className="flex items-center gap-2">{actions}</div>}
    </section>
  )
}
