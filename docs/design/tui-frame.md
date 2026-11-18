# The TUI's frame: sidebar, thread and computer (design)

Status: built in part (2026-11-18): the frame, the places, the thread beside its computer, and
the computer's Terminal, Changes and Details tabs. [tui.md](../tui.md#the-frame) describes it as
it is.
It replaces Home as a sidebar and a pane ([tui-home-panes.md](tui-home-panes.md)) and the
sessions on Home ([tui-sessions.md](tui-sessions.md)). It is mock A of the TUI revamp, with
mock B's inbox as its Home. Still to come: the Files and Browser tabs, comments on lines of a
diff, Search Studio on `ctrl+k`, and `/` commands and `@` files in the composer.

Where the build differs from the text below:

- `+` in the sidebar starts a thread with the teammate under the cursor, and on any other row
  opens New computer. It doesn't ask "`t` thread · `c` computer · `m` teammate" yet.
- On a teammate's home `+` gives the teammate another computer, and `enter` writes to it.
- The sidebar's second row is `/ Filter` until Search Studio replaces the filter.
- The Changes tab took the Git screen and the git review, and `g` on Home is gone. A decided
  push is in the tab's PUSHED for a day, and in Activity under git after that.
- PUSHED is read from `GET .../approvals?status=approved` every third read of git (about 15
  seconds) while the tab shows, filtered to the computer and the last day. Its `#n` comes from
  the push's pull request URL: the TUI doesn't read the pull request's live state yet.
- `enter` on a push opens its computer on its own, unless the place on show is that computer's
  (a thread on it, or its teammate's home showing it); then the tab opens beside it.
- A push of a computer the environment on show doesn't have, or that went, opens a review
  place: the review alone, with the review's keys and `j/k` scrolling.
- A forge review or comment that waits shows as its card in the tab, decided with the card's
  keys.
- The rail is beside main from a pane of 70 cells, and a diff is side by side from a pane of
  160 (so after `alt+z` at 180 columns). A repository's header takes the cursor and shows its
  branch, upstream and changes.
- The first pick waits for git's status, so that a change, not a repository, gets the cursor.
- A change's diff starts at its first hunk: the head over it names the file.
- On a review the key bar leaves out `↑↓` and `[/]` for the decisions; `?` lists them.

## The frame keeps its five fixed rows

The frame is the crumb row, a rule, the body (`h-5` rows), a rule, the key bar and the notice
line. The mocks draw no notice line, so their bodies are one row taller than what shipped. The
notice line stays: the filter, the one-line prompts (`linePrompt`), confirmations and results
live there. At 80×24 the body is 19 rows.

## Widths

One function decides the columns, and nothing else reads `m.width` for layout:

```go
// layout is the widths of the frame's columns; 0 means the column is not drawn.
type layout struct{ sidebar, main, side int }

func (m *app) layoutFor(w int) layout
```

| Width | Sidebar | Main | Side (the computer) |
|---|---|---|---|
| `< 100` | drawer on `\` (30 cells, over the body) | `w` | none; `tab` flips main between the thread and the computer |
| `100–119` | 30, always | `w-31` | none; `tab` flips |
| `>= 120`, a thread or a teammate's home with a computer | 30 | `clamp((w-31)*42/100, 40, 60)` | the rest, less 1 for the split |
| `>= 120`, a thread that is an agent in a terminal | 30 | the wider share | the narrower share, on Details |
| `>= 120`, Home, your own computer, or a full screen (Activity, Settings…) | 30 | `w-31` | none |
| zoomed (`alt+z`) | 0 | `w` (the focused pane) | 0 |

At 140 this gives 30 / 45 / 63, which is the A4 mock. `paneWidth = 100`, `splitWidth = 120`
and `sideWidth = 160` (the side-by-side diff) keep their values, and the sidebar is the
constant 30. Layout depends on the width and the place only, never on the look (plain or
visual).

## Who has the keys

Three owners, always visible in the frame and the key bar:

1. **A shell** (a terminal has the keys). The Terminal tab's rule and the bottom rule turn into
   the accent's heavy `━`. Studio keeps only `ctrl+]` (keys back), `alt+1`…`alt+9` (another
   terminal), `alt+z` (full screen) and `alt+a` (answer the banner). Everything else, `ctrl+c`
   included, goes to the shell.
2. **The composer.** Text goes to the draft. `enter` sends, `ctrl+j` a new line, `esc` puts the
   draft aside.
3. **Studio.** One of three focus areas has the cursor:

```go
type focusArea int

const (
	focusMain    focusArea = iota // Home's list, a thread, a teammate's home, a computer on its own
	focusSidebar                  // the sidebar, or the drawer
	focusSide                     // the computer beside a thread
)
```

The split to the left of the focused column is the accent's `┃`; the others are a dim `│`.

**`alt+←/→` is not taken.** Mock A3 shows it for switching tabs while a shell has the keys.
Debian and Ubuntu's readline, and Claude Code's own prompt, use alt+arrows to move by word, so
taking them breaks typing in the agent. Tabs are switched with `1`–`5` once Studio has the keys
(`ctrl+]` first).

## The key map when Studio has the keys

Everywhere:

| Key | Does | Note |
|---|---|---|
| `\` | to the sidebar and back; below 100 columns it opens and closes the drawer | |
| `tab` | in a thread or a computer: flip to the other pane. On Home: the next waiting row | inside a thread, `esc` returns to Home with the cursor where it was |
| `alt+z` | the focused pane takes the whole screen, and back | |
| `esc` | back one place, last to Home | |
| `q` | quit from Home and the sidebar; back elsewhere | |
| `?` | keys | |
| `+` | new: a thread with the teammate in view, a shell on the Terminal tab, a computer on Home | **`n` no longer means new anywhere.** `n` was "new" on Home and "deny" on a card on the same screen, and "new chat" in a chat where `n` now rejects a permission. `n` always means no |
| `e` `,` `l` | environment, Settings, Activity: on Home and in the sidebar | `l` on Details is that computer's Activity |

Keys that went: `a` (the Approvals screen), `1`–`9` into a session from Home, `c`/`C` (chat,
new chat), `t`/`T`/`s`/`z`/`f`/`h`/`d` on Home's list (they moved to the thread, the computer's
tabs and the Computers screen), `n` new.

Two rules keep the keys from clashing:

- **Lifecycle keys (`s` `z` `f` `h` `d`) work on the Details tab and on a row of the Computers
  screen, nowhere else.** `s` is "scope" on a network row and must not also stop a computer on
  the same screen.
- **Digits belong to what is under the cursor**: a row's or card's choices on Home and in a
  thread (`1` allow for an hour, `1`–`9` an agent's options, `1`/`2` a memory conflict), the
  tabs `1`–`5` in the computer pane. They never enter sessions.

## Terminals are drawn by the emulator at every width

A terminal used to be drawn in the pane by `liveTerm` (`pane.go`, `charmbracelet/x/vt`) from 100
columns, and handed over raw with `tea.Exec` below. Mock A3 puts the tabs and the terminal chips
around the shell at 80×24, which a raw handover can't draw, so the TUI uses `liveTerm` at every
width. The raw `attachment` stays for `studio shell`. `alt+z` gives the shell the whole screen
less one status line.
