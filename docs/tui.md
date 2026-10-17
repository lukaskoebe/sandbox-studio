# Terminal UI

`studio tui` is a keyboard client of the running Studio, in the same binary. It covers the
daily loop: see and create sandboxes, answer approvals and review git pushes, chat with a
sandbox's teammate, follow the network Activity, and drop into a sandbox's terminal. Everything
else stays in the browser, and the TUI prints the URL where it applies. Plain `studio` is still
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
| **Home** | the sandboxes of the environment on show: status dot and word, name, teammate, template, uptime and the approvals waiting on each | `↑/↓` `j/k` move · `enter` open · `n` new · `t` terminal · `s` start, stop or resume · `d` delete (asks first) · `c` chat · `a` approvals · `l` Activity · `e` environment · `/` filter by name, teammate or template · `?` help · `q` quit |
| **Sandbox** (from `enter`) | status, teammate, template, resources, uptime, approvals, ID | `t` terminal · `s` start, stop or resume · `d` delete · `c` chat · `l` its Activity · `a` approvals · `esc` back |
| **New sandbox** (from `n`) | a teammate, a template, a name; then the create's steps | see below |
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
| Browser, memory, chat | `enter` prints the URL of the page that decides it |

The decisions go to the same endpoint as the web card (`POST
/api/environments/{env}/approvals/{id}/decide`), with the same bodies.

### Git review

`enter` on a git card opens the review the web's git sheet and page show (`web/src/components/git-review.tsx`),
from the approval's `git` detail: the repository, sandbox and teammate, the branch (and whether
it is new), the change with its file count and line totals, the commits, and the unified diff,
colored as on the web (`+` green, `-` red, hunks in the accent). A pull request shows its
branches, title and body instead. The review is a pager: `↑/↓` `j/k` scroll a line,
`pgup/pgdn` (or `b`/`space`) a page, `g`/`G` jump to the start or the end.

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
- Plans, permission requests and questions show as one line each. Permissions and questions are
  answered in the browser.
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
memory, the browser, lineage and checkpoints, secrets, and the git settings. Chat permissions
and questions, browser actions and memory approvals are answered there too. Where a screen has
a key for one, it prints `open in browser: <url>` in the notice line. `?` gives the browser's
address for the rest.

## Look

The terminal's own foreground and background, one accent (ANSI cyan) for focus and status,
and dim text for secondary facts; diffs add green and red. Colors are ANSI, so the user's
palette applies. Bubble Tea's renderer drops them when `NO_COLOR` is set, and the TUI is laid
out for 80×24.

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
  decision choices of `web/src/components/approval-decision.tsx`. `pager.go` is the git review.
- `chat.go` is the chat screens: the stream, the transcript's layout and folds, the composer.
  `input.go` is the line editor the composer and the new sandbox's name use.
- `activity.go` is the Activity log and an entry's rule. `newsandbox.go` is the new sandbox's
  form and the create's progress.
- `terminal.go` is the attachment: a `tea.ExecCommand`, so Bubble Tea releases the terminal
  while it runs. It bridges the websocket and reads the keys itself, which is what lets Ctrl-]
  return without a goroutine left reading the terminal.

The background work of the chat and Activity screens carries a generation, so a result, a
stream event or a tick of a screen the user has left is dropped.

Tests:

- Model tests drive `Update` with key and result messages.
- Golden views (`testdata/*.golden`) are the screens at 80×24 with `NO_COLOR`. After an
  intended change, `go test ./internal/tui -run Golden -update` rewrites them; review the diff.
- The client and the commands run against an in-process Studio: the API behind the same guards
  and an in-memory catalog, with a fake VM runtime, the chat service and a connection log. The
  test Studio never touches the real one.
- A pseudo-terminal test runs the TUI as a child process: it shows Home, opens Activity, goes
  back and quits. Another runs the attachment on a real terminal and checks that unbuffered
  input reaches the sandbox and that Ctrl-] detaches. Both are skipped on Windows.
