import { createFileRoute } from "@tanstack/react-router"
import { BrowserMockup, type BrowserMockupSearch } from "@/mockups/browser/page"
import { places, type Place } from "@/mockups/browser/places"
import { presets, type Preset } from "@/mockups/browser/sim"

// The browser revamp, interactive with static data. Dev builds only, like every mockup.
export const Route = createFileRoute("/mockups/browser")({
  validateSearch: (search: Record<string, unknown>): BrowserMockupSearch => ({
    place: places.includes(search.place as Place)
      ? (search.place as Place)
      : undefined,
    state: presets.includes(search.state as Preset)
      ? (search.state as Preset)
      : undefined,
    theme:
      search.theme === "light" || search.theme === "dark"
        ? search.theme
        : undefined,
    bare: search.bare === true || search.bare === "true" || undefined,
  }),
  component: BrowserMockup,
})
