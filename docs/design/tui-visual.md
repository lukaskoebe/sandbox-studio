# A more visual TUI (design)

Status: built (2026-10-17); [tui.md](../tui.md#look) describes the TUI as it is. Left out:

- Pictures in a graphics protocol (kitty, iTerm2, sixel). Bubble Tea v2 draws the screen
  as cells and diffs them between frames, so an image placed with escape sequences would be
  overwritten or left behind. Half blocks show the same picture on every terminal of 256
  colors or more.
- App previews: only the browser has a picture.

Where the build differs from the text:

- `NO_COLOR`, a terminal without colors, and `STUDIO_TUI=plain` all give today's look. That
  look is what the 80×24 goldens record.
- Usage is also read for the detail screen's sandbox, not only on Home.
- The Activity strip shows up to 3 hosts, or 5 on 30 rows and more, and needs 14 rows.
- Toasts report what `actionDone` and `attachDone` carry and the errors of background loads.
  Notices of a form or a prompt, progress ("Stopping…") and `open in browser:` URLs stay on
  the notice line, where they can be read and copied.
- Word highlights mark the unified diff as well as side by side; the plain look shows the diff
  as it comes. Lines that share no word keep their line's color only.
- The picture is in Home's detail pane, under the page's address, and needs 256 colors, not
  truecolor. It keeps the frame's shape, so a 1280×577 page is 40×9 cells. The page's title
  isn't shown: Studio doesn't know it.

Today's `studio tui` is deliberately plain: one screen at a time, the terminal's own colors
plus one cyan accent, laid out for 80×24 ([tui.md](../tui.md#look)). It works everywhere,
but it reads like a list of facts. The web has since settled on a visual language
([ui-style.md](ui-style.md)): one hue per area, a fixed status set, identifiers in mono, cards
with a colored rule. This concept brings that language to the terminal and uses the space a
modern terminal has, without giving up 80×24, `NO_COLOR` or plain SSH.

## Principles

- **80×24 stays the floor.** Every screen keeps its single-screen layout as the fallback, and
  today's goldens stay the 80×24 `NO_COLOR` reference. Everything below is added on wider or
  more capable terminals.
- **Same language as the web.** Area hues for kinds of things, a fixed status set, the word
  always next to the dot. Accents mark; they never fill backgrounds of text.
- **Detect, don't configure.** Width, color depth (16, 256, truecolor via Lip Gloss's
  profile detection), mouse and image support are read from the terminal. One override:
  `STUDIO_TUI=plain` gives today's look.
- **Still a client.** No new state, no new endpoints except where noted; it reads the same
  API.

## 1. Colors: area hues and status

The web's tokens mapped to terminal colors. Truecolor terminals get the web's oklch values
converted to sRGB; 256-color terminals the nearest xterm color; 16-color terminals the
ANSI fallback, so the user's palette still applies there.

| Role | Web token | 256 | 16-color fallback |
|---|---|---|---|
| Network | `--acc-network` sky | 74 | blue (4) |
| Git | `--acc-git` violet | 141 | magenta (5) |
| Browser | `--acc-browser` orange | 215 | yellow (3) |
| Memory | `--acc-memory` teal | 72 | cyan (6) |
| Chat and agent | `--acc-chat` green | 114 | green (2) |
| Running | `--st-running` | 77 | green (2) |
| Waiting for you | `--st-waiting` | 221 | yellow (3), bold |
| Failed | `--st-failed` | 203 | red (1) |
| Stopped | `--st-stopped` | 245, hollow `○` | dim |

Focus stays the one cyan accent of today, so focus and area never compete. The status word is
never colored, as on the web: `● Running` has a green dot and a plain word.

## 2. Header with tabs, footer with context

The header becomes a tab bar for the screens reached with one key today. The tab of the
screen on show is bold with an underline rule in the accent; counts sit on their tab, in the
waiting color when something waits.

```
 ◆ Studio  Default ▾   Sandboxes   Approvals 3   Activity   Git 1           14:02 
 ━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━
```

`◆` is Studio's cube in the brand teal. Below 100 columns the tabs shorten to their initial
and count (`S  A3  L  G1`); at 80 columns the header is today's.

The key bar stays, but groups keys by what they act on, with a dim `│` between the groups,
and moves rarely used keys behind `?`.

## 3. Wide terminals: list and detail side by side

At 120 columns and more, Home shows the selected sandbox next to the list, the way a mail
client does. Moving the cursor updates the right pane; `enter` still opens the full screen.

```
 ◆ Studio  Default ▾   Sandboxes   Approvals 3   Activity   Git 1                       14:02 
 ━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━
  SANDBOXES                                 │ api-refactor                          ● Running
 ▌● Running  api-refactor  Finn   ▂▃▅▇▅▃ 2h │ ──────────────────────────────────────────────
  ● Running  blub          Iris   ▁▁▂▁▁▁ 4d │ Finn · go@2 · 2 CPUs · 2 GiB · up 2h 14m
  ○ Stopped  docs-site     Iris             │
  ◐ Booting  demo          —      ░░░░░░    │ CPU  ▂▃▅▇▅▃▂▃▅▆▇▇▅▃▂   38 %   ┊ MEM ███████░░░ 1.4 GiB
                                            │
                                            │ ▌ Git push · waiting                     30s ago
                                            │ ▌ 2 commits to feature/login on acme/web
                                            │ ▌ +214 −37 in 9 files          enter review
                                            │
                                            │ TERMINALS   main · claude · 2
                                            │ PORTS       :3000 vite · :8080 api
                                            │ HISTORY     ○──○──●──◇─────◆ now
                                            │             created  auto  checkpoint
 ───────────────────────────────────────────┴──────────────────────────────────────────────
  enter open  n new  s start/stop │ t terminal  c chat │ a approvals  l activity  ? more
```

- The `▌` rule marks the focused row in the accent; the git card's rule is git violet.
- **Sparklines** (`▁▂▃▄▅▆▇█`) show the last minutes of CPU per sandbox, from the usage the
  workspace status line already reads (`runtime.Usage`), polled while Home is on show. The
  detail pane adds memory as a bar. A booting sandbox shows a moving `░▒▓` shimmer instead.
- **History** is the lineage in one line: forks `○`, checkpoints `●` (auto ones `·`), a
  restore `◇`, the current generation `◆`.
- At 160 columns and more a third column shows the sandbox's live Activity, so Home is a
  dashboard: list, detail, network.

## 4. Approvals as cards

Each approval is a card with a rounded border and a rule in its area's hue; the focused card's
border is the accent. Choices sit on the card as keycaps, so the decision reads at a glance.

```
 ╭──────────────────────────────────────────────────────────────────────────────╮
 ┃ ⇅ NETWORK   blub wants registry.npmjs.org:443           Default · 10m ago   │
 ┃                                                                             │
 ┃   pattern  *.npmjs.org ‹p›    scope  Iris's sandboxes ‹s›   ports  default ‹x›│
 ┃                                                                             │
 ┃   [y] Allow   [n] Deny   [o] Once   [1] 1 hour                  [i] Ignore    │
 ╰──────────────────────────────────────────────────────────────────────────────╯
```

`┃` is the area rule (network sky, git violet, browser orange, memory teal). Keycaps are bold
on a dim background; the one the card would choose by default is underlined. Group cards
stack their hosts as chips (`pypi.org` `files.pythonhosted.org` `+1`).

## 5. Git review: file list, side by side, and stats

The review keeps its pager, and gains:

- a **file list** on the left at 120 columns and more, with each file's `+/−` as a small bar
  (`+++++−−`), and `[`/`]` to jump between files;
- **side-by-side** hunks at 160 columns and more (`v` toggles), with line numbers in dim;
- **word-level highlights** inside changed lines (reverse video on the changed words), from
  a plain diff of the two lines, with no syntax highlighter;
- a summary strip at the top: `9 files  +214 −37  ████████████▒▒▒`.

```
 FILES            │ internal/auth/login.go                                      +42 −7
 ▌auth/login.go ▇▂│  40  func Login(w http.ResponseWriter, r *http.Request) {
  auth/token.go ▅ │  41 -	token := r.URL.Query().Get("token")
  web/login.tsx ▃▁│  41 +	token, err := readToken(r)
  …               │  42 +	if err != nil {
```

## 6. Chat with a shape

- The user's prompts get a chat-green rule and a little space above, the agent's text none,
  so turns separate without boxes.
- Tool calls fold to one line with a glyph per kind (`✎` edit, `⌕` search, `▶` run, `↯`
  fetch) and the state on the right as a status dot; a running call spins (`⠋⠙⠹⠸`).
- Code blocks get a dim left rule and the language as a label; diffs inside a tool call use
  the git review's colors.
- A plan shows as a checklist (`☐ ☑`) that fills in as the turn runs.

## 7. Activity as a live view

The log keeps its list and gets a strip on top: one row per busy host, a sparkline of its
connections over the last 10 minutes, and the verdict's color (allowed green, denied red,
waiting yellow).

```
 registry.npmjs.org   ▁▂▇█▅▂▁▁▁▁   allowed   *.npmjs.org
 api.openai.com       ▁▁▁▂▂▂▃▂▂▂   allowed   provider
 telemetry.foo.dev    ▂▂▂▂▂▂▂▂▂▂   denied    rule #12
```

## 8. Pictures where the terminal can show them

The teammate's browser and app previews are visual by nature. Where the terminal speaks a
graphics protocol (kitty, iTerm2, or sixel; detected), the sandbox detail shows the browser's
latest screencast frame as a thumbnail, refreshed every few seconds. Elsewhere, on truecolor
terminals, a half-block rendering (`▀` with foreground and background colors, two pixels per
cell) gives a rough 40×12 preview, enough to see that a page loaded or a dialog is open. On
16-color terminals there is no picture, only the page's title and URL.

This needs one addition to the API: the latest frame as a JPEG (`GET .../browser/frame`).
Studio relays the screencast's frames to the web today without keeping any
(`internal/browser/stream.go`), so it would keep the last one of each browser.

## 9. Motion and mouse

- **Mouse**: Bubble Tea v2 reports clicks and the wheel. Clicking a row selects it, a
  double click opens it, the wheel scrolls pagers and transcripts, and clicking a tab or a
  keycap acts as its key. Holding shift keeps the terminal's own selection.
- **Motion** only where it carries state: the spinner of a running turn or call, the booting
  shimmer, a new approval's card sliding in from the top over 150 ms. Nothing moves on an
  idle screen, so the TUI stays quiet over SSH.
- **Toasts**: decisions and errors show for 3 seconds at the top right instead of replacing
  the notice line.

## Phases

1. **Color and cards** (no layout change): the hue table, status dots, the tab header, card
   approvals, keycaps, chat rules and glyphs. Goldens at 80×24 stay; new colored goldens
   record ANSI output at 256 colors.
2. **Wide layouts and mouse**: the list and detail split at 120 columns, the git file list,
   side by side at 160, the history line. Goldens at 140×40 and 180×50.
3. **Live numbers**: the sparklines and bars from usage and connections, Activity's strip.
4. **Pictures**: the browser frame endpoint, terminal graphics detection, half-block fallback.

Each phase is useful alone, and none changes what a key does.

## Costs and open questions

- **Dependencies.** Phases 1–3 need nothing beyond Lip Gloss v2 (borders, color profiles,
  layout joins). Phase 4 needs a sixel/kitty encoder; a small one is a few hundred lines, or
  `github.com/BourgeoisBear/rasterm` (MIT).
- **Polling.** Sparklines on Home read usage for every running sandbox every 2 seconds. For
  many sandboxes, an environment-wide usage endpoint (one call) would be better.
- **Fonts.** Everything uses Unicode box drawing, blocks and a few symbols that common
  monospace fonts carry; no Nerd Font. Glyphs a font lacks show as tofu, so a test renders
  every glyph we use against the fonts of the major terminals' defaults, and `plain` drops
  them.
- **Windows Terminal** supports truecolor, mouse and (since 1.22) sixel; conhost supports
  none of them and gets the 16-color layout.
- **Decided:** the 160-column dashboard is Home, not its own screen. Its third column is
  only the selected sandbox's connections, so it stays quiet for people who use the TUI to
  answer approvals, and a narrower terminal never sees it.
