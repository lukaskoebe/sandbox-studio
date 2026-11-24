import { cn } from "@/lib/utils"

/** How a teammate's page splits between its conversation and its computer. */
export type PageLayout = "conversation" | "both" | "computer"

/** Small pictures of the layouts: the conversation's lines, the two side by side, a prompt. */
const glyphs: Record<PageLayout, string> = {
  conversation: "M4 5h10M4 8h7",
  both: "M8 1v12M3.5 5h3M3.5 8h2.5M10.5 5l2 2-2 2M13.5 9.5h2",
  computer: "M4.5 5l2.5 2-2.5 2M9 9.5h4.5",
}

function Glyph({ layout }: { layout: PageLayout }) {
  return (
    <svg
      aria-hidden
      viewBox="0 0 18 14"
      className="h-3.5 w-[18px] fill-none stroke-current stroke-[1.4] [stroke-linecap:round] [stroke-linejoin:round]"
    >
      <rect x="1" y="1" width="16" height="12" rx="2" />
      <path d={glyphs[layout]} />
    </svg>
  )
}

/**
 * The one control for a teammate's page layout, at the right of the top bar: the conversation
 * alone, both side by side, or the computer alone. A narrow window has no room for both, so it
 * offers the other two. conversation names the left side: "Chat" in a thread, "Desk" without.
 */
export function LayoutSwitch({
  value,
  onChange,
  conversation,
  narrow,
}: {
  value: PageLayout
  onChange: (layout: PageLayout) => void
  conversation: string
  narrow: boolean
}) {
  const layouts: PageLayout[] = narrow
    ? ["conversation", "computer"]
    : ["conversation", "both", "computer"]
  const label = (l: PageLayout) =>
    l === "conversation"
      ? `${conversation} only`
      : l === "both"
        ? "Both"
        : "Computer only"
  return (
    <div
      role="group"
      aria-label="Layout"
      className="ml-1 flex shrink-0 items-center gap-px rounded-md bg-foreground/5 p-0.5"
    >
      {layouts.map((l) => (
        <button
          key={l}
          type="button"
          aria-label={label(l)}
          aria-pressed={value === l}
          title={label(l)}
          onClick={() => value !== l && onChange(l)}
          className={cn(
            "grid h-6 w-7 place-items-center rounded-[5px] text-muted-foreground outline-none hover:text-foreground focus-visible:ring-2 focus-visible:ring-ring/50",
            value === l &&
              "bg-background text-foreground shadow-[0_1px_2px_rgb(0_0_0/8%),0_0_0_1px_color-mix(in_oklab,var(--border)_70%,transparent)] dark:bg-accent"
          )}
        >
          <Glyph layout={l} />
        </button>
      ))}
    </div>
  )
}
