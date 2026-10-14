import { Link } from "@tanstack/react-router"
import { screenTitles, screens, type Direction } from "./shared"
import { browserPitch } from "./browser/page"
import { conceptInfo, concepts } from "./teammate/frame"

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
      <h2 className="mt-12 text-lg font-semibold tracking-tight">
        Teammate page concepts
      </h2>
      <p className="mt-1 text-muted-foreground">
        Interactive, with static data. The pill at the bottom switches the
        concept, the teammate's presence (free, working, waiting) and the theme.
      </p>
      <div className="mt-4 grid gap-4 md:grid-cols-3">
        {concepts.map((c) => (
          <Link
            key={c}
            to="/mockups/teammate/$concept"
            params={{ concept: c }}
            className="rounded-lg border p-5 hover:bg-muted"
          >
            <h3 className="font-semibold">{conceptInfo[c].name}</h3>
            <p className="mt-1 text-muted-foreground">{conceptInfo[c].pitch}</p>
          </Link>
        ))}
      </div>
      <h2 className="mt-12 text-lg font-semibold tracking-tight">
        Browser revamp
      </h2>
      <p className="mt-1 text-muted-foreground">
        Interactive, with static data and a simulated agent. The pill at the
        bottom switches where the browser sits, the state it starts from and the
        theme.
      </p>
      <Link
        to="/mockups/browser"
        className="mt-4 block rounded-lg border p-5 hover:bg-muted"
      >
        <h3 className="font-semibold">Browser window</h3>
        <p className="mt-1 text-muted-foreground">{browserPitch}</p>
      </Link>
    </main>
  )
}
