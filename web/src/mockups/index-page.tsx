import { Link } from "@tanstack/react-router"
import { screenTitles, screens, type Direction } from "./shared"

const directions: { id: Direction; name: string; pitch: string }[] = [
  {
    id: "a",
    name: "Workbench",
    pitch:
      "Dense and tool-like. Graphite neutrals, monospace identifiers, section headers with small colored tiles, lists over cards and a status bar per sandbox.",
  },
  {
    id: "b",
    name: "Calm studio",
    pitch:
      "Warm paper neutrals around the green, generous spacing, large titles with one-line intros, teammates shown as people, cards only for things you act on.",
  },
]

/** Lists both directions and their screens (N4, decision D3). */
export function MockupIndex() {
  return (
    <main className="mx-auto max-w-3xl p-6 text-sm md:p-12">
      <h1 className="text-2xl font-semibold tracking-tight">
        Visual identity mockups
      </h1>
      <p className="mt-1 text-muted-foreground">
        Two directions for N4, with static data. The brief is in{" "}
        <code className="font-mono text-xs">docs/design/ui-style.md</code>. Each
        page has a theme toggle in its sidebar.
      </p>
      <div className="mt-8 grid gap-4 md:grid-cols-2">
        {directions.map((d) => (
          <section key={d.id} className="rounded-lg border p-5">
            <h2 className="font-semibold">
              {d.id.toUpperCase()} · {d.name}
            </h2>
            <p className="mt-1 text-muted-foreground">{d.pitch}</p>
            <ul className="mt-4 grid gap-1">
              {screens.map((s) => (
                <li key={s}>
                  <Link
                    to="/mockups/$direction/$screen"
                    params={{ direction: d.id, screen: s }}
                    className="text-primary underline-offset-4 hover:underline"
                  >
                    {screenTitles[s]}
                  </Link>
                </li>
              ))}
            </ul>
          </section>
        ))}
      </div>
    </main>
  )
}
