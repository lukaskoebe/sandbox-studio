# Terminal UI

`studio tui` is a keyboard client of the running Studio, in the same binary. It covers the
daily loop: see, create, fork and suspend sandboxes, save and restore their checkpoints, see
their open ports, answer approvals of every kind and review git pushes, chat with a sandbox's
teammate, follow the network Activity, and drop into any of a sandbox's terminals, an agent's
among them, with one key. Everything else
stays in the browser, and the TUI prints the URL where it applies. Plain `studio` is still
the server; `studio tui` only ever connects to one.

The user page is [Terminal UI](../site/src/content/docs/tui.md). The design of the visual look
is in [design/tui-visual.md](design/tui-visual.md), that of Home's sessions in
[design/tui-sessions.md](design/tui-sessions.md), and that of Home's sidebar and pane in
[design/tui-home-panes.md](design/tui-home-panes.md).

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
  is no glamour dependency: `markdown.go` renders chat text itself, reading markdown as the
  web's renderer does.
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
| **Home** | the sandboxes of the environment on show: status, name, teammate, template, uptime and the approvals waiting on each; under each, its [sessions](#sessions). From 100 columns, a sidebar of them beside [the pane](#the-pane) | `↑/↓` `j/k` move over sandboxes and sessions · `1`–`9` go into that session · `enter` go into the session, or open the sandbox · `alt+z` the pane in full screen · `y` `o` `n` decide the [banner](#the-approval-banner)'s network request, `a` reviews it on its card, `←/→` and `space` press its buttons · `tab` the next session that waits · `n` new · `t` terminal · `T` start the teammate's agent · `s` start, stop or resume · `z` suspend · `f` fork · `h` checkpoints · `d` delete (asks first) · `c` the newest chat · `C` a new chat · `a` approvals · `l` Activity · `g` Git · `e` environment · `/` filter by name, teammate or template · `?` help · `q` quit |
| **Sandbox** (from `enter`) | status, teammate, template, resources, uptime, approvals, ID, and the open ports with their preview addresses | `t` terminal · `T` agent · `s` start, stop or resume · `z` suspend · `f` fork · `h` checkpoints · `d` delete · `c` chat · `C` new chat · `l` its Activity · `a` approvals · `g` Git · `esc` back |
| **Checkpoints** (from `h`) | the sandbox's checkpoints, newest first: name, auto or manual, state, age | `n` save one · `r` restore the one under the cursor (asks first) · `↑/↓` move · `esc` back |
| **New sandbox** (from `n`) | a teammate, a template, a name; then the create's steps. A fork's progress shows here too | see below |
| **Chat** (from a chat's session, `c` or `C`) | the transcript, streaming | see below |
| **Approvals** (from `a` anywhere) | every pending approval of every environment, grouped like the web stack | see below |
| **Git** (from `g`, or its tab) | the environment's pushes and pull requests: the ones waiting for a review, then the last 100 decided, with their sandbox and outcome | `↑/↓` move · `enter` the review · `a` approvals · `l` Activity · `esc` back |
| **Git review** (from `enter` on a git card or a push on the Git screen) | the push's facts, commits and diff, or the pull request | see below |
| **Activity** (from `l`) | connections and decisions of the environment's sandboxes, newest first | `enter` the entry and its rule · `/` filter by sandbox · `r` print the rules page's URL · `esc` back |
| **Environments** (from `e`) | the environments; `enter` switches | `↑/↓` · `enter` choose · `esc` cancel |

The header always shows the number of pending approvals across all environments, after the
environment's working and blocked sessions. When a new approval arrives, the terminal rings
its bell once.

A confirmation (delete a sandbox, restore a checkpoint) shows its choices in the notice line,
`y Delete  [n Keep]`, with Keep focused: `enter` keeps, `y` or `←` then `enter` goes ahead.

### Sessions

Under each sandbox, Home lists what can be entered in it, indented:

- its agent terminals: tmux sessions that run a harness, such as `claude` or `codex-2`;
- the chats of its teammate in it, the newest three, then a `+ n older` row that shows the
  rest. A check-in's chat says so, and so does a scheduled task's run. A chat continued in a
  terminal is listed once, as its terminal;
- its other terminals, `main` among them.

The first nine sessions get the numbers `1` to `9`, top to bottom. On Home, a number goes
straight into its session, and so does `enter` on a session's row. From 100 columns the
session opens in [the pane](#the-pane) beside the list. Narrower, it takes the screen: a
terminal is attached (any of them, by its name), and `Ctrl-]` returns to Home with the cursor
on it. A chat opens, an idle one with the composer ready; `esc` closes the composer and a
second `esc` returns. Either way, switching to another session takes two keys: `Ctrl-]` (or
`esc`), then its number; in the pane, `alt+1` to `alt+9` take one.

Each agent terminal and chat shows its agent's state, as herdr words them:

| Mark | State | A chat | An agent terminal |
|---|---|---|---|
| `●` | working | a turn runs | the terminal printed within 5 seconds of the last read |
| `▲` | blocked | one of its permission requests waits | an approval of another kind waits in its sandbox; approvals don't say which process asked |
| `✓` | done | it worked, and hasn't been entered since | the same |
| `○` | idle | none of these | none of these |
| `·` | unknown | | the guest doesn't report the terminal's output |

A session that turns done says so, in the notice line or as a toast. `tab` moves the cursor
to the next blocked session, then to the next done one. Plain terminals show no state and say
when they are attached elsewhere, such as in the web.

Home reads the terminals of each running sandbox (`GET .../sandboxes/{id}/terminals`) and the
chats of each sandbox with a teammate (`GET .../sandboxes/{id}/chats`) every 3 seconds while
it shows. A terminal's `activity` is when its window last printed, which the guest reads from
tmux's `window_activity`; a sandbox started before Studio had it reports none until it starts
again. Done lives in the TUI's memory only.

`T` starts the teammate's harness in a new terminal (`POST .../sandboxes/{id}/sessions`, the
web's Start agent) and attaches it. `c` opens the sandbox's newest chat, or a new one when it
has none, and `C` always starts a new one.

### The pane

From 100 columns, Home is a sidebar and a pane. The sidebar lists each sandbox as a group
header, `blub · Iris ───── ▁▂▄ ● 2h` (name, teammate, CPU sparkline, approvals waiting as `▲ n`,
status and uptime), with its numbered sessions under it, each with its state and its kind on
the right: `agent`, `chat`, `term`, `sched` for a scheduled task's run, or `check` for a
check-in's. The pane shows what the cursor is on:

- **A sandbox's header**: its dashboard. It has the facts, CPU over the last 2 minutes as an
  area chart with its mean and peak, memory as a gauge and a one-row history, and the first
  approval waiting as a card. Then come its terminals, ports, history, ID, newest connections
  and its teammate's browser. A pane under 20 rows shows CPU and memory on one line each, and
  the plain look has no charts.
- **A terminal or an agent's terminal**: its screen, live. The pane attaches it once the
  cursor has rested on it for 150 ms, and lets it go when the cursor moves on; the tmux
  session keeps running.
- **A chat**: its transcript, streaming.

`enter`, the session's number or a click on the pane gives the pane the keys. The split
between the columns turns into the accent `┃`, and every key goes to the terminal, `q` and
`ctrl+c` included, except:

- `Ctrl-]` gives the keys back to the sidebar (in a chat, `esc` does too, once the composer is
  closed);
- `alt+1` to `alt+9` switch to that session and keep the keys in the pane;
- `alt+z` puts the pane in full screen, and back. It works from the sidebar too.

Alt keys need a terminal that sends them as `ESC` and the key; on macOS, set Option to act as
Meta. The pane sends its size when it attaches and when it changes. tmux sizes a session to
its latest client, so a terminal open in the web too follows whichever was resized last.

### The approval banner

When an approval waits in a sandbox, every session of it in the pane, terminal or chat, gets
a box over its top rows that says what waits, such as `api-refactor wants
proxy.golang.org:443 · 1 more`. A chat's own permission requests are in its transcript and get
no banner. The box carries its choices as buttons: `o Allow once`, `y Allow`, `n Deny` and
`a More…` for a single network request, `a Review it` for any other kind or a group.

- **From the sidebar**, the letters decide it, and `←/→` move over the buttons and `space` or
  `enter` presses one. `enter` keeps opening the session until an arrow is pressed.
- **While the pane has the keys**, they go to the agent, so the box says `alt+a answers it`.
  `alt+a` moves the keys to the banner's buttons. After a decision, or `esc`, the keys go back
  to the agent, so answering takes `alt+a`, then `y`, and typing goes on.
- **`a`** (More…, Review it) opens the approvals with the cursor on that card, where the
  pattern, the scope and the other choices are. Once it is decided there, the TUI goes back to
  Home, with the pane's keys where they were. `esc` there stays on the approvals.

A terminal attached full screen (below 100 columns, or `t`) can't draw a box over the
sandbox's screen. Instead, while it is attached, the TUI checks the sandbox's pending
approvals every 2 seconds. A new one sets the terminal's title to `▲ api-refactor wants
proxy.golang.org:443 · Ctrl-] answers it` and rings the bell once. The title is written only
after 150 ms without output, so it doesn't land inside an escape sequence the sandbox is
writing, and the old title is pushed (`CSI 22;0 t`) and popped (`CSI 23;0 t`) around it, in
terminals that keep a title stack. `Ctrl-]` then opens the approvals on that card instead of
Home, and once it is decided, the terminal is attached again.

`studio shell` watches the same way, and having no TUI to return to, its `Ctrl-]` asks about
the approval in place (`Shell`, `askApproval` in `internal/tui/shell.go`): it clears the screen
and shows the request with `o` allow once, `y` allow (the teammate's default pattern and scope,
as the card would start) and `n` deny, or for another kind says to answer it in the TUI or the
browser. `enter` goes back to the terminal, and `q`, `Ctrl-]` or `Ctrl-C` detach. After a
decision the terminal is attached again, which redraws it. With nothing waiting, `Ctrl-]`
detaches as before.

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

A chat is opened from its session on Home, or with `c` and `C`. A sandbox without a teammate
has no chats, and `c` says so.

A chat loads its log (`GET .../chats/{chat}`), then follows its stream (`GET
.../chats/{chat}/stream?after=<rev>`), the SSE the web follows. Each event carries a chat, turn
or item row at a new rev; the TUI keeps the newest version of each row, so an event that
arrives late changes nothing. A lost stream resumes from the last rev after 2 seconds, and a
chat that is gone (404) says so.

The transcript:

- Prompts are bold, after `›`. The agent's text is rendered as markdown (`markdown.go`, the
  same blocks and inline marks as `web/src/components/chat/markdown.tsx`): headings and
  `**bold**` are bold, `*italics*` italic, and a link shows its text with its address dim
  after it. Bullets become `•` (`◦` nested) and items wrap under their text; quotes get a
  `│` rule (`>` in the plain look). Code blocks drop their fences and get a dim `│` rule
  with their language (indented four spaces in the plain look); inline code takes the accent
  color (keeps its backticks in the plain look). Tables are boxed, each column as wide as its
  cells up to a fair share of the width, with cells wrapping inside; a table too narrow for
  its columns becomes `heading: cell` lines per row.
- Thoughts and tool calls are folded to one line: `▸`, the title, and the call's state on the
  right. `↑/↓` moves a cursor between them, `enter` (or `space`) expands one: a tool call's
  locations, its diffs (`-`/`+` lines) and text output, at most 40 lines. With no cursor the
  transcript follows the end; `↓` past the last fold returns there.
- Plans, permission requests and questions show as one line each. The first one that waits
  is answered in place, with its choices listed under it:
  - a permission request: `1`–`9` pick an option, `i` ignores it. It is decided as its card
    on the approvals screen is. `←/→` move over its choices as buttons, and `space` or `enter`
    presses one.
  - a question: `i` skips it (`decline`). One with no field takes `1` for OK; a choice, or yes
    or no, takes `1`–`9`; text or a number takes `r`, which opens a line for the answer in the
    notice line. The answer goes to `POST .../chats/{chat}/items/{item}/answer` with
    `{action: "accept", content: {field: value}}`, as the web's form sends it. A question with
    several fields, or a list, is a form; its keys print the chat's URL instead.
- `pgup/pgdn` (or `b`/`space`) scroll a page, `g`/`G` jump to the start or the end.

An approval waiting in the chat's sandbox, such as a network request of a tool the agent
runs, is a [banner](#the-approval-banner) under the title. `alt+a` opens the approvals on its
card and comes back to the chat once it is decided.

The composer is a box at the bottom of the chat, in the chat's color, with the teammate's name
in its frame and its keys on the right; it grows with the prompt to six lines. While no prompt
is written it is one line on the keycaps' background: `› Write to Iris…`, `Sending…` or the
draft put aside. `p` (or `enter` with no fold selected) opens it: `enter` sends,
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
session is created when it doesn't exist. A session row on Home attaches its own terminal, in
the pane from 100 columns, and `studio shell <sandbox> [terminal]` does the same from a shell.
Only a running sandbox has terminals.

In the pane, the terminal is drawn by an emulator (`charmbracelet/x/vt`) fed by the same
websocket, which encodes the keys as a terminal would. Full screen, as below, the TUI hands the
terminal over.

The attachment uses the same websocket as the web terminal (`.../terminals/{name}/attach`):

- The screen, the keyboard and the terminal's size are handed over in raw mode, so the
  sandbox's full-screen programs work as they do in a terminal of their own. The size is sent
  whenever it changes, checked every 250 ms.
- `Ctrl-]` detaches and returns to the TUI. It is not sent to the sandbox. If an approval
  came while it was attached, `Ctrl-]` opens it, as [the banner](#the-approval-banner) says.
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
  choices as keycaps on the card under the cursor. `←/→` move over the card's toggles (pattern,
  scope, ports) and then its choices; the one focused is reversed, and `space` or `enter`
  steps a toggle or presses a choice. Nothing is focused until an arrow is pressed. In the
  plain look the focused one is in brackets.
- **Chat.** Prompts have a green rule and tool calls a glyph for their kind. A running call or
  turn spins. Code blocks have a dim rule and their language, and a plan is a checklist.
- **Wide layouts.** From 100 columns, Home is the sidebar and [the pane](#the-pane), in both
  looks. A sandbox's dashboard in the pane has the history line from its lineage and its
  network: its connections newest first, with their verdict's dot, age, host and verdict,
  read from `GET .../sandboxes/{id}/connections` every 2 seconds while it shows (`dash.go`).
  From 120 columns the git review gains a file list with +/− bars, where `[` and `]` jump
  between files. When the sandbox's teammate's browser runs, the dashboard ends with its page's
  address and, on terminals with 256 colors or more, a picture of the page about 40×9 cells,
  drawn in half blocks (`▀`, two pixels a cell) from `GET .../browser/frame` every 3 seconds
  (`picture.go`). A picture that doesn't fit the pane's height is left out. From 160 columns the diff is side by side, and `v` toggles
  it. Inside a changed line, the words it changes are in reverse video. They come from a
  diff of the words of each removed line and the added line paired with it (`worddiff.go`),
  and lines that share no word, or have more than 300, keep their line's color only.
- **Live numbers.** From 100 columns, the sidebar has a CPU sparkline per running sandbox, and
  the dashboard charts CPU and memory over 2 minutes (`charts.go`). The detail screen shows
  CPU and a memory bar. Usage is read every 2 seconds, and only while Home or the detail is on
  show, for running sandboxes. A runtime that
  reports no usage stops the reads. Activity has a strip of the busiest hosts, with 10-minute
  connection sparklines.
- **Mouse.** Bubble Tea's cell-motion mode is on, and holding shift keeps the terminal's own
  selection. A click selects a row: a sandbox, session, card, log entry, checkpoint,
  environment, fold or file. A double click opens it as `enter` does. The wheel scrolls the review and the
  transcript, and moves the cursor in lists. A click on a tab acts as its key, and a click on
  the environment opens the picker. A click on a card's keycap or a key of the key bar presses
  that key; the bar's arrows and keys with a modifier take no clicks.
- **Motion.** The result of an action (a start, a stop, a decision, a detach) and the errors
  of background loads show as a toast at the top right for 3 seconds, in the running or the
  failed color, and clear the notice line's "Stopping…". The plain look keeps them on the
  notice line. A new approval's card slides in from the top over about 150 ms while the
  approvals show. Nothing else moves but the spinner and the booting shimmer.

No key changed between the looks. Only `[`, `]` and `v` in the git review are new.

## Code

The package is `internal/tui`. `cmd/studio/tui.go` is the subcommand.

- `client.go` is the typed client. It adds no endpoints; its results are the API's own Go
  types (`sandboxes.View`, `api.ApprovalView`, `store.Chat`, `chat.Log`, `gateway.Conn`,
  `store.Rule`, `browser.BrowserStatus`, `events.Event`). Changes arrive on `/api/events`; a
  burst of them refetches once, after 300 ms. `ChatStream` reads a chat's SSE stream into
  `ChatEvent`s.
- `app.go`, `keys.go`, `view.go`, `home.go`, `detail.go` are the Bubble Tea v2 model: state,
  messages, key handling and the frame. The key bar is Bubbles' `help`, and the bindings are
  Bubbles' `key`, so the bar and the handlers come from one list.
- `approvals.go` ports the web's card grouping (`web/src/lib/approval-groups.ts`) and the
  decision choices of `web/src/components/approval-decision.tsx`. `kinds.go` decides the other
  kinds (browser, memory, agent permissions) and reads a question's schema into the one field
  the TUI answers. `pager.go` is the git review, and `git.go` the Git screen.
- `chat.go` is the chat screen: the stream, the transcript's layout and folds. `composer.go`
  is the composer's box.
- `sessions.go` is Home's sessions: their reads, states, rows, numbers and the ways in.
  `homepane.go` is Home's split, the pane's state, keys, title and approval banner; `buttons.go`
  the focus of the arrows over buttons, the confirmations, and the return to a session after a
  decision made elsewhere; `pane.go`
  the live terminal in it, an emulator between the attachment's websocket and the frame; and
  `charts.go` the dashboard and its charts.
  `input.go` is the line editor the composer and the new sandbox's name use, and `linePrompt`,
  the one-line question of the notice line (a fork's name, a checkpoint's name, an answer).
- `activity.go` is the Activity log and an entry's rule. `newsandbox.go` is the new sandbox's
  form, the fork, and the progress of both. `checkpoints.go` is the checkpoints screen, and
  `ports.go` the detail's ports.
- `style.go` holds the styles: the look, the palette of each color profile, and the fitting of
  styled text. `draw.go` has the visual look's pieces: dots, keycaps, card frames,
  sparklines, bars, the spinner, the tab header and the grouped key bar. `cards.go` is the
  approvals' cards, `live.go` the usage, lineage and spinner reads, `motion.go` the
  toasts and the cards' slide-in, `dash.go` the network column, and `picture.go` the browser's
  page and its picture in half blocks. `mouse.go` maps
  clicks and the wheel to what each row and key of the frame leads to. Each body records
  those rows and keycaps as it draws them, and the frame the key bar's keys.
- `terminal.go` is the attachment: a `tea.ExecCommand`, so Bubble Tea releases the terminal
  while it runs. It bridges the websocket and reads the keys itself, which is what lets Ctrl-]
  return without a goroutine left reading the terminal. While a terminal of Home is attached,
  it watches the sandbox's approvals and sets the title.

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
  tab. A third runs the attachment on a real terminal and checks that unbuffered input
  reaches the sandbox and that Ctrl-] detaches. They are skipped on Windows.
- Model tests run the pane against a fake attachment: its screen, the keys it sends, Ctrl-],
  alt+digits and full screen, and the banner's keys.
- Model tests press the arrows over a card's toggles, the banner, a chat's picks and a
  confirmation, answer the banner with `alt+a` and check the keys go back, and check that a
  decision on the approvals returns to the pane or attaches the terminal again.
