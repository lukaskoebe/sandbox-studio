import { ColumnsIcon, RowsIcon } from "@phosphor-icons/react"
import { ToggleGroup, ToggleGroupItem } from "@/components/ui/toggle-group"
import { setDiffStyle, useDiffStyle } from "@/lib/diff-style"
import { cn } from "@/lib/utils"

/**
 * Stacked lines or old and new side by side, for every full-length diff in this browser. Kept
 * apart from the diff view, which loads late, so a review's bar can hold it.
 */
export function DiffStyleSwitch({ className }: { className?: string }) {
  const style = useDiffStyle()
  return (
    <ToggleGroup
      aria-label="Diff layout"
      variant="outline"
      size="sm"
      spacing={0}
      className={cn("justify-self-end", className)}
      value={[style]}
      onValueChange={(v) => {
        if (v[0] === "unified" || v[0] === "split") setDiffStyle(v[0])
      }}
    >
      <ToggleGroupItem value="unified" title="Old and new lines in one column">
        <RowsIcon />
        Stacked
      </ToggleGroupItem>
      <ToggleGroupItem
        value="split"
        title="Old lines on the left, new on the right"
      >
        <ColumnsIcon />
        Split
      </ToggleGroupItem>
    </ToggleGroup>
  )
}
