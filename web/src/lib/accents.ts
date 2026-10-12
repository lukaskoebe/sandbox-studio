/**
 * The areas that get a hue: network, git, browser, memory and chat. One hue per area, used
 * sparingly for a card's rule, an icon tile and a kind chip (docs/design/ui-style.md). Status
 * colors are never an area. The hues are the --acc-* tokens in styles.css.
 */
export type Area = "network" | "git" | "browser" | "memory" | "chat"

// Written out in full so Tailwind generates them.
const classes: Record<Area, { rule: string; tile: string }> = {
  network: {
    rule: "border-l-(--acc-network)",
    tile: "bg-(--acc-network)/11 text-(--acc-network)",
  },
  git: {
    rule: "border-l-(--acc-git)",
    tile: "bg-(--acc-git)/11 text-(--acc-git)",
  },
  browser: {
    rule: "border-l-(--acc-browser)",
    tile: "bg-(--acc-browser)/11 text-(--acc-browser)",
  },
  memory: {
    rule: "border-l-(--acc-memory)",
    tile: "bg-(--acc-memory)/11 text-(--acc-memory)",
  },
  chat: {
    rule: "border-l-(--acc-chat)",
    tile: "bg-(--acc-chat)/11 text-(--acc-chat)",
  },
}

/** The left edge of a card of this area; a neutral edge without one. */
export function ruleClass(area?: Area): string {
  return area ? classes[area].rule : "border-l-border"
}

/** The icon tile of this area: a faint tint with the hue on top; neutral without an area. */
export function tileClass(area?: Area): string {
  return area ? classes[area].tile : "bg-foreground/[0.07] text-foreground/70"
}
