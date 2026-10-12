import { CopyIcon } from "@phosphor-icons/react"
import { toast } from "sonner"
import { Button } from "@/components/ui/button"
import { copyText } from "@/lib/clipboard"
import { cn } from "@/lib/utils"

/**
 * An identifier in monospace: a host, port, sandbox or template ID, path or command. Long
 * values wrap rather than widen their container. With `copy`, a button copies the value, or
 * `value` when the shown text is shorter, such as a short commit SHA.
 */
export function Ident({
  children,
  copy = false,
  value = children,
  className,
}: {
  children: string
  copy?: boolean
  value?: string
  className?: string
}) {
  return (
    <span
      className={cn(
        "inline-flex max-w-full items-center gap-1 font-mono",
        className
      )}
    >
      <span className="min-w-0 break-all">{children}</span>
      {copy && (
        <Button
          variant="ghost"
          size="icon-xs"
          title="Copy"
          aria-label={`Copy ${value}`}
          onClick={() => copyValue(value)}
        >
          <CopyIcon />
        </Button>
      )}
    </span>
  )
}

async function copyValue(text: string) {
  try {
    await copyText(text)
    toast.success("Copied")
  } catch {
    toast.error("Could not copy")
  }
}
