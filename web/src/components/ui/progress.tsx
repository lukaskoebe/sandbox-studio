import { Progress as ProgressPrimitive } from "@base-ui/react/progress"
import { cn } from "@/lib/utils"

function Progress({
  className,
  value,
  ...props
}: ProgressPrimitive.Root.Props) {
  return (
    <ProgressPrimitive.Root
      data-slot="progress"
      value={value}
      className={cn("flex w-full", className)}
      {...props}
    >
      <ProgressPrimitive.Track className="relative h-1 w-full overflow-hidden rounded-full bg-muted">
        <ProgressPrimitive.Indicator className="h-full bg-primary transition-all data-indeterminate:w-1/3 data-indeterminate:animate-pulse" />
      </ProgressPrimitive.Track>
    </ProgressPrimitive.Root>
  )
}

export { Progress }
