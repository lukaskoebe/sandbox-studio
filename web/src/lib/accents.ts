/**
 * The areas that get a hue: network, git, browser, memory and chat. One hue per area, used
 * sparingly for the rule of an approval card, its kind chip and section tiles
 * (docs/design/ui-style.md). Status colors are never an area. The hues are the --acc-* tokens
 * in styles.css.
 */
export type Area = "network" | "git" | "browser" | "memory" | "chat"

/**
 * Tailwind classes per area. They are written out in full so Tailwind generates them.
 * `rule` is a card's left edge; `tile` is an icon's square, a faint tint with the hue on top.
 */
export const areaClasses: Record<Area, { rule: string; tile: string }> = {
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
