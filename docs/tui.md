# Terminal UI

`studio tui` is a keyboard client of the running Studio, in the same binary. It covers the
daily loop: see, create, fork and suspend sandboxes, save and restore their checkpoints, see
their open ports, answer approvals of every kind and review git pushes, chat with a sandbox's
teammate, follow the network Activity, and drop into a sandbox's terminal. Everything else
stays in the browser, and the TUI prints the URL where it applies. Plain `studio` is still
the server; `studio tui` only ever connects to one.

The user page is [Terminal UI](../site/src/content/docs/tui.md). A proposal for a richer look
on wide and capable terminals is in [design/tui-visual.md](design/tui-visual.md).

## Why a terminal client

Studio is a browser app on a loopback-only server. Using it from another machine means a
tunnel or the LAN Vite setup, people who live in a terminal still switched to a browser to
answer an approval, and over plain SSH (a dev box, a homelab server, a cloud VM) nothing
worked without forwarding a port. The TUI runs *on* the host, so nothing new listens on the
network.

It covers the daily loop rather than parity with the web: it is a client of the same HTTP
API and adds no second way to change state. Settings, providers, souls, the template editor,
lineage, memory, the rules editor and the browser stay in the web app; the TUI prints their
URL.

- **Library.** Charm's Bubble Tea v2 with Lip Gloss and Bubbles (`charm.land/*/v2`, MIT,
  works on Windows terminals). `tview` has heavier widgets and less control over the look; a
  hand-rolled renderer on `golang.org/x/term` needs no dependency but much more code. There
  is no markdown renderer: chat text gets light styling (bold headings, indented code)
  instead of a glamour dependency.
- **`studio tui`, not `studio`.** Plain `studio` stays the server even in an interactive
  terminal with a server already running, so scripts and autostart never get a UI.
- **One screen at a time**, with a header and a key bar and no panes, so it works at 80×24
  and over slow links.

## Starting it

```sh
studio            # the server, in one terminal, or at login (studio autostart enable)
studio tui        # in another terminal, on the same machine
```

`studio tui` needs an interactive terminal and a Studio on its loopback address,
`127.0.0.1:7878` by default. For another address, put the flag before the subcommand:
`studio -addr 127.0.0.1:9000 tui`. Without one it says so, prints the start command and exits with
status 1. It doesn't start Studio itself.

## Screens and keys

Keys are the same on every screen and cannot be changed. The key bar shows the ones that apply.

| Screen | Shows | Keys |
|---|---|---|
| **Home** | the sandboxes of the environment on show: status dot and word, name, teammate, template, uptime and the approvals waiting on each | `↑/↓` `j/k` move · `enter` open · `n` new · `t` terminal · `s` start, stop or resume · `z` suspend · `f` fork · `h` checkpoints · `d` delete (asks first) · `c` chat · `a` approvals · `l` Activity · `e` environment · `/` filter by name, teammate or template · `?` help · `q` quit |
| **Sandbox** (from `enter`) | status, teammate, template, resources, uptime, approvals, ID, and the open ports with their preview addresses | `t` terminal · `s` start, stop or resume · `z` suspend · `f` fork · `h` checkpoints · `d` delete · `c` chat · `l` its Activity · `a` approvals · `esc` back |
| **Checkpoints** (from `h`) | the sandbox's checkpoints, newest first: name, auto or manual, state, age | `n` save one · `r` restore the one under the cursor (asks first) · `↑/↓` move · `esc` back |
| **New sandbox** (from `n`) | a teammate, a template, a name; then the create's steps. A fork's progress shows here too | see below |
| **Chats** (from `c`) | the chats of the sandbox's teammate in that sandbox, newest activity first | `enter` open · `n` new chat · `esc` back |
| **Chat** | the transcript, streaming | see below |
| **Approvals** (from `a` anywhere) | every pending approval of every environment, grouped like the web stack | see below |
| **Git review** (from `enter` on a git card) | the push's facts, commits and diff, or the pull request | see below |
| **Activity** (from `l`) | connections and decisions of the environment's sandboxes, newest first | `enter` the entry and its rule · `/` filter by sandbox · `r` print the rules page's URL · `esc` back |
| **Environments** (from `e`) | the environments; `enter` switches | `↑/↓` · `enter` choose · `esc` cancel |

The header always shows the number of pending approvals across all environments. When a new
approval arrives, the terminal rings its bell once.

### Approvals

Requests from one sandbox that arrive within three seconds of each other form one card, and
a browser page's requests to one site form one card, as on the web. A card's choices start the
way the web card starts them: the pattern level and the scope its teammate last chose.

| Card | Keys |
|---|---|
| Network request | `y` allow · `n` deny (each makes a rule) · `o` allow once · `O` deny once (no rule) · `1` allow for 1 hour · `i` ignore · `p` cycle the host pattern, then a browser request's method and path · `s` scope: environment, the teammate's sandboxes or this sandbox; for a browser request, who: its browser, all browsers, or browsers and sandboxes · `x` any port instead of the default ports · `w` allow the navigation's site for this browser session |
| Group | `A` allow all · `D` deny all · `r` review one by one · `s` scope, or who for a browser page's requests |
| Git push or pull request | `enter` opens the git review; it is decided there, after the diff |
| Browser action | `y` allow · `n` deny · `i` ignore · `r` toggles whether the decision holds for every such action on the origin (the web's "always") |
| Browser credential | `y` fill · `n` deny · `i` ignore |
| Memory share | `y` share with every teammate · `n` decline · `i` ignore |
| Memory conflict | `1` keep fact A · `2` keep fact B · `i` later (it stays open on the memory page) · `enter` prints the memory page's URL, for keep both or an edit |
| Agent permission | `1`–`9` pick one of the agent's options, numbered as listed · `i` ignore |
| Any other kind | `enter` prints the URL of the page that decides it |

The decisions go to the same endpoint as the web card (`POST
/api/environments/{env}/approvals/{id}/decide`), with the same bodies:

- a browser action: `{action, scope: "teammate", remember}`, with `remember` left out for an
  ignore;
- a browser credential: `{action, scope: "teammate"}`;
- a memory share, and a memory conflict put off: `{action, scope: "environment"}`;
- an agent permission: `{action, scope: "environment", remember}`. Each option maps to an action
  and `remember` by its kind (allow once, allow always, reject once, reject always), and Studio
  maps them back to the option, as it does for the web's buttons.

Keeping one fact of a memory conflict resolves the conflict itself, as the web's conflict sheet
does: `POST .../memory/conflicts/{id}/resolve` with `{resolution: "keep_a" | "keep_b"}`. Studio
then closes the approval. Keeping both facts, or editing one, needs a text editor for two
facts, so it stays in the browser.

### Git review

`enter` on a git card opens the review the web's git sheet and page show (`web/src/components/git-review.tsx`),
from the approval's `git` detail: the repository, sandbox and teammate, the branch (and whether
it is new), the change with its file count and line totals, the commits, and the unified diff,
colored as on the web (`+` green, `-` red, hunks in the accent). A pull request shows its
branches, title and body instead. The review is a pager: `↑/↓` `j/k` scroll a line,
`pgup/pgdn` (or `b`/`space`) a page, `g`/`G` jump to the start or the end. In the visual look,
`[` and `]` jump to the previous or next file. From 120 columns a file list sits beside the
review, and from 160 columns the diff is side by side, which `v` toggles.

`y` approves and pushes (or opens the pull request), `n` rejects, `i` ignores. Each decides with
`{action, scope: "sandbox"}`, as the web does, and returns to the approvals. The web's note for
the agent is not offered; a rejection with a note is written in the browser.

### Chat

`c` lists the chats of the sandbox's teammate in that sandbox (`GET
.../sandboxes/{id}/chats`). A sandbox without a teammate has no chats, and `c` says so.

A chat loads its log (`GET .../chats/{chat}`), then follows its stream (`GET
.../chats/{chat}/stream?after=<rev>`), the SSE the web follows. Each event carries a chat, turn
or item row at a new rev; the TUI keeps the newest version of each row, so an event that
arrives late changes nothing. A lost stream resumes from the last rev after 2 seconds, and a
chat that is gone (404) says so.

The transcript:

- Prompts are bold, after `›`. The agent's text gets light styling, with no markdown renderer:
  headings are bold, code blocks are indented four spaces with their fences dropped, list items
  wrap under their text.
- Thoughts and tool calls are folded to one line: `▸`, the title, and the call's state on the
  right. `↑/↓` moves a cursor between them, `enter` (or `space`) expands one: a tool call's
  locations, its diffs (`-`/`+` lines) and text output, at most 40 lines. With no cursor the
  transcript follows the end; `↓` past the last fold returns there.
- Plans, permission requests and questions show as one line each. The first one that waits
  is answered in place, with its choices listed under it:
  - a permission request: `1`–`9` pick an option, `i` ignores it. It is decided as its card
    on the approvals screen is.
  - a question: `i` skips it (`decline`). One with no field takes `1` for OK; a choice, or yes
    or no, takes `1`–`9`; text or a number takes `r`, which opens a line for the answer in the
    notice line. The answer goes to `POST .../chats/{chat}/items/{item}/answer` with
    `{action: "accept", content: {field: value}}`, as the web's form sends it. A question with
    several fields, or a list, is a form; its keys print the chat's URL instead.
- `pgup/pgdn` (or `b`/`space`) scroll a page, `g`/`G` jump to the start or the end.

`p` (or `enter` with no fold selected) opens the composer in the notice line: `enter` sends,
`ctrl+j` (or `shift+enter` where the terminal reports it) starts a new line, `esc` puts the
draft aside. A prompt is sent with `POST .../chats/{chat}/prompt`; while a turn runs Studio
answers 409, and the draft stays. `x` stops the running turn (`POST .../chats/{chat}/cancel`).
`n` starts a new chat: the composer opens, and the first prompt creates it (`POST
.../sandboxes/{id}/chats`). A chat continued in the terminal, or one whose sandbox was reset,
takes no prompt here.

### Activity

`l` on Home shows the environment's log; on a sandbox it starts filtered to that sandbox. The
log merges, as the web's Activity does:

- each sandbox's latest connections (`GET .../sandboxes/{id}/connections`), which Studio keeps
  in memory: host and port, the verdict (allowed, denied, failed, waiting, unanswered), and the
  rule that decided it;
- the decisions on its network requests (`GET .../approvals?status=all`, decided ones only),
  worded as on the web: Allowed, Denied, Allowed once, Ignored, Allowed · reviewer, the curated
  list or the level Open.

Studio announces no change to the connection logs, so the screen reads them again every two
seconds while it is on show, as the web's connection panel does. `/` filters by sandbox name;
`esc` clears the filter before it leaves. `enter` opens an entry with the rule that decided it
(`GET .../rules`): its host pattern, action, ports, what it covers (the environment, a
teammate's sandboxes or one sandbox), the requests it narrows to, its origin (an approval, the
curated list, the AI reviewer, by hand), hits, expiry, age and note. Without a rule it says why:
the curated list, the level, a once decision, an ignore. Rules are edited in the browser; `r`
prints where.

### New sandbox

`n` on Home asks, one step at a time, what the web's create dialogs ask:

1. whose sandbox it is: a teammate of the environment, or none (skipped when there are none);
2. what it starts from: the base image or one of the environment's templates (`GET
   .../templates`);
3. its name, which is also its hostname: lowercase letters, digits and dashes, checked
   against the API's pattern before the create.

`enter` goes on, `esc` goes back a step. The create is `POST .../sandboxes` (or `POST
.../templates/{id}/sandboxes` with a template), which answers at once with the sandbox in state
`creating`. The progress screen then follows the sandbox through the `sandboxes` topic of the
event stream, as the web's progress does: the steps done, the one that runs (with the image
pull's bytes or layers), and those to come. Once the create is over it opens the sandbox. A
failed create shows the step that failed and why, and `d` deletes the sandbox. `esc` leaves
the create running on its own.

### Fork, suspend and checkpoints

`f` on Home or a sandbox asks for the fork's name in the notice line, starting from the web's
suggestion (the name, cut to 35 characters, with `-fork`). The name is checked against the
API's pattern before the fork. `POST .../sandboxes/{id}/fork` answers at once with the new
sandbox in state `creating`, and the progress screen follows it as it does a create.

`z` suspends a running sandbox (`POST .../sandboxes/{id}/suspend`); `s` resumes it.

`h` opens the sandbox's checkpoints (`GET .../sandboxes/{id}/checkpoints`). They are copies of
its disks, not of its running processes. `n` asks for a name in the notice line; an empty one
takes the web's suggestion from the time (`Checkpoint 2026-10-17 09:30:00 UTC`, with ` (2)` and
on when taken). It saves while the sandbox runs or is stopped. `r` restores the checkpoint under
the cursor after a `y`: only a stopped sandbox goes back, on a runtime that can restore
(`checkpointRestoreSupported`), and what changed since is lost. Deleting and renaming checkpoints,
and the schedule, stay in the browser.

### Ports

The detail of a running sandbox lists its open ports (`GET .../sandboxes/{id}/ports`) with each
one's preview address, `http://{port}-{id}.localhost:{studio port}/`. Where the terminal shows
links, the address is one (OSC 8). Studio announces no change to the ports, so the screen
reads them again every three seconds while it is on show, as the web's Ports region does. It
lists six and counts the rest.

The previews need a preview login. The web opens a preview through a one-time link
(`POST .../previews/{port}/open`), which sets a preview cookie for eight hours. The TUI doesn't
print those links: each is a one-time token, too long for an 80-column line, and only good
in a browser anyway. A preview opened once from the web's Ports region opens from the TUI's
address after that.

## Sign-in

`studio tui` signs in as the local owner, the way `studio login-url` does:

1. It reads the install key from the catalog in the data directory and derives the control
   credential from it (`webauth.ControlToken`). Only a process that can read the catalog has it,
   so only the same OS user on the same machine can use the TUI. Over SSH, run it on the host.
2. It presents the control credential to `/api/auth/launch` and gets a one-time login ticket.
3. It exchanges the ticket at `/api/auth/exchange`, as the web login does, and keeps the
   resulting session cookie in memory. That session is the TUI's own; a browser never sees it.
4. On exit it calls `/api/auth/logout`, which clears the cookie.

No endpoint was added for this. Nothing listens on the network: the TUI reaches Studio on its
loopback address only, and refuses any other address.

Studio's sessions are signed cookies with no server-side list, as in the browser. Logout clears
the cookie in the client, but a copy of the cookie stays valid until its 30-day lifetime. A
revocable session table would be a change to the web sessions too; it is not part of the TUI.

## Terminal

`t` attaches to the sandbox's tmux session `main`, which the web workspace opens first; the
session is created when it doesn't exist. Only a running sandbox has a terminal.

The attachment uses the same websocket as the web terminal (`.../terminals/{name}/attach`):

- The screen, the keyboard and the terminal's size are handed over in raw mode, so the
  sandbox's full-screen programs work as they do in a terminal of their own. The size is sent
  whenever it changes, checked every 250 ms.
- `Ctrl-]` detaches and returns to the TUI. It is not sent to the sandbox.
- If the sandbox ends the terminal, Studio shows that and the next key returns to the TUI. That
  key is not sent to the sandbox either.

## Stays in the browser

Settings, providers, teammates, templates and the template editor, rules, the network page,
memory, the browser, lineage, deleting or renaming checkpoints and their schedule, secrets, and
the git settings. Some answers stay there too, because they need more than a key:

- keeping both facts of a memory conflict, or editing one: two texts to edit;
- a chat question that is a form of several fields, or a list: a form to fill;
- a preview's first login: a one-time link for a browser.

Where a screen has a key for one, it prints `open in browser: <url>` in the notice line. `?`
gives the browser's address for the rest.

## Look

There are two looks. The visual look is the default on a terminal with colors. The plain look
is used under `STUDIO_TUI=plain`, under `NO_COLOR`, or on a terminal without colors.

The plain look uses:

- the terminal's own foreground and background;
- one accent (ANSI cyan) for focus and status;
- dim text for secondary facts;
- green and red in diffs.

Every screen is laid out for 80×24.

The visual look carries the web's visual language ([design/ui-style.md](design/ui-style.md)
and [design/tui-visual.md](design/tui-visual.md)):

- **Colors.** One hue per area: network, git, browser, memory and chat. The status set is
  running, waiting, failed and stopped. The status dot is colored and the word never is;
  stopped is a hollow `○`. Truecolor terminals get the web's values, with light and dark
  variants chosen from the terminal's background. 256-color terminals get the nearest xterm
  colors. 16-color terminals get ANSI colors, so the user's palette still applies there.
  Focus stays the one cyan accent.
- **Header and key bar.** From 81 columns the header is a tab bar: `◆ Studio`, the
  environment, Sandboxes, Approvals, Activity and Git, with counts in the waiting color, and
  the clock. Below 100 columns the tabs shorten to their initials. At 80 columns the header
  is the plain one. The key bar groups keys with a dim `│` between the groups and calls `?`
  "more". When the separators don't fit, it drops them.
- **Cards.** Approvals are rounded cards with their area's rule, a kind chip, and the
  choices as keycaps on the card under the cursor.
- **Chat.** Prompts have a green rule and tool calls a glyph for their kind. A running call or
  turn spins. Code blocks have a dim rule and their language, and a plan is a checklist.
- **Wide layouts.** From 120 columns, Home is the list beside the selected sandbox's pane. The
  pane shows its facts, CPU and memory, the first approval waiting as a card, terminals, ports,
  and a history line from its lineage. The git review gains a file list with +/− bars, where
  `[` and `]` jump between files. From 160 columns the diff is side by side, and `v` toggles
  it. Inside a changed line, the words it changes are in reverse video. They come from a
  diff of the words of each removed line and the added line paired with it (`worddiff.go`),
  and lines that share no word, or have more than 300, keep their line's color only.
- **Live numbers.** From 100 columns, Home's list has a CPU sparkline per running sandbox.
  The pane and the detail screen show CPU and a memory bar. Usage is read every 2 seconds,
  and only while Home or the detail is on show, for running sandboxes. A runtime that
  reports no usage stops the reads. Activity has a strip of the busiest hosts, with 10-minute
  connection sparklines.
- **Mouse.** Bubble Tea's cell-motion mode is on, and holding shift keeps the terminal's own
  selection. A click selects a row: a sandbox, card, log entry, chat, checkpoint, environment,
  fold or file. A double click opens it as `enter` does. The wheel scrolls the review and the
  transcript, and moves the cursor in lists. A click on a tab acts as its key, and a click on
  the environment opens the picker. A click on a card's keycap or a key of the key bar presses
  that key; the bar's arrows and keys with a modifier take no clicks.

No key changed between the looks. Only `[`, `]` and `v` in the git review are new.

## Code

The package is `internal/tui`. `cmd/studio/tui.go` is the subcommand.

- `client.go` is the typed client. It adds no endpoints; its results are the API's own Go
  types (`sandboxes.View`, `api.ApprovalView`, `store.Chat`, `chat.Log`, `gateway.Conn`,
  `store.Rule`, `events.Event`). Changes arrive on `/api/events`; a burst of them refetches
  once, after 300 ms. `ChatStream` reads a chat's SSE stream into `ChatEvent`s.
- `app.go`, `keys.go`, `view.go`, `home.go`, `detail.go` are the Bubble Tea v2 model: state,
  messages, key handling and the frame. The key bar is Bubbles' `help`, and the bindings are
  Bubbles' `key`, so the bar and the handlers come from one list.
- `approvals.go` ports the web's card grouping (`web/src/lib/approval-groups.ts`) and the
  decision choices of `web/src/components/approval-decision.tsx`. `kinds.go` decides the other
  kinds (browser, memory, agent permissions) and reads a question's schema into the one field
  the TUI answers. `pager.go` is the git review.
- `chat.go` is the chat screens: the stream, the transcript's layout and folds, the composer.
  `input.go` is the line editor the composer and the new sandbox's name use, and `linePrompt`,
  the one-line question of the notice line (a fork's name, a checkpoint's name, an answer).
- `activity.go` is the Activity log and an entry's rule. `newsandbox.go` is the new sandbox's
  form, the fork, and the progress of both. `checkpoints.go` is the checkpoints screen, and
  `ports.go` the detail's ports.
- `style.go` holds the styles: the look, the palette of each color profile, and the fitting of
  styled text. `draw.go` has the visual look's pieces: dots, keycaps, card frames,
  sparklines, bars, the spinner, the tab header and the grouped key bar. `cards.go` is the
  approvals' cards, and `live.go` the usage, lineage and spinner reads. `mouse.go` maps
  clicks and the wheel to what each row and key of the frame leads to. Each body records
  those rows and keycaps as it draws them, and the frame the key bar's keys.
- `terminal.go` is the attachment: a `tea.ExecCommand`, so Bubble Tea releases the terminal
  while it runs. It bridges the websocket and reads the keys itself, which is what lets Ctrl-]
  return without a goroutine left reading the terminal.

The background work of the chat and Activity screens carries a generation, so a result, a
stream event or a tick of a screen the user has left is dropped.

Tests:

- Model tests drive `Update` with key and result messages.
- Golden views (`testdata/*.golden`) are the screens at 80×24 with `NO_COLOR`, which is the
  plain look. The `visual_*` goldens hold the visual look's 256-color ANSI at 80×24. The
  `wide_*` goldens hold its text at 96×24, 140×40 and 180×50. All of them run with a fixed
  clock, profile and spinner frame. After an intended change,
  `go test ./internal/tui -run Golden -update` rewrites them; review the diff.
- The client and the commands run against an in-process Studio: the API behind the same guards
  and an in-memory catalog, with a fake VM runtime, the chat service and a connection log. The
  test Studio never touches the real one. Where that Studio can't answer (an agent that is
  connected, a runtime that restores), a server that records the calls checks the paths and
  bodies the keys send.
- A pseudo-terminal test runs the TUI as a child process: it shows Home, opens Activity, goes
  back, opens the approvals, allows a waiting host with `y` (the server then has its rule) and
  quits. A second one runs at 140×40, where Home splits, and opens Activity by clicking its
  tab. Another runs the attachment on a real terminal and checks that unbuffered
  input reaches the sandbox and that Ctrl-] detaches. Both are skipped on Windows.
