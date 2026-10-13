# Terminal UI

`studio tui` is a keyboard client of the running Studio, in the same binary. It covers the
daily loop: see the sandboxes, answer approvals, and drop into a sandbox's terminal. Everything
else stays in the browser, and the TUI prints the URL where it applies. Plain `studio` is still
the server; `studio tui` only ever connects to one.

The design is in [PLAN.md](../PLAN.md#c2--terminal-ui-concept). The user page is
[Terminal UI](../site/src/content/docs/tui.md).

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
| **Home** | the sandboxes of the environment on show: status dot and word, name, teammate, template, uptime and the approvals waiting on each | `↑/↓` `j/k` move · `enter` open · `t` terminal · `s` start, stop or resume · `d` delete (asks first) · `a` approvals · `l` activity (browser) · `n` new (browser) · `c` chat (browser) · `e` environment · `/` filter by name, teammate or template · `?` help · `q` quit |
| **Sandbox** (from `enter`) | status, teammate, template, resources, uptime, approvals, ID | `t` terminal · `s` start, stop or resume · `d` delete · `c` chat (browser) · `a` approvals · `esc` back |
| **Approvals** (from `a` on Home or a sandbox) | every pending approval of every environment, grouped like the web stack | see below |
| **Environments** (from `e`) | the environments; `enter` switches | `↑/↓` · `enter` choose · `esc` cancel |

The header always shows the number of pending approvals across all environments. When a new
approval arrives, the terminal rings its bell once.

### Approvals

Requests from one sandbox that arrive within three seconds of each other form one card, and
a browser page's requests to one site form one card, as on the web. A card's choices start the
way the web card starts them: the pattern level and the scope its teammate last chose.

| Card | Keys |
|---|---|
| Network request | `y` allow · `n` deny (each makes a rule) · `o` allow once · `O` deny once (no rule) · `1` allow for 1 hour · `i` ignore · `p` cycle the host pattern, then a browser request's method and path · `s` scope: environment, the teammate's sandboxes or this sandbox · `x` any port instead of the default ports · `w` allow the navigation's site for this browser session |
| Group | `A` allow all · `D` deny all · `r` review one by one · `s` scope |
| Git push or pull request, browser, memory, chat | `enter` prints the URL of the page that decides it |

The decisions go to the same endpoint as the web card (`POST
/api/environments/{env}/approvals/{id}/decide`), with the same bodies.

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
memory, the browser, lineage and checkpoints, secrets, the git page and its diffs, the new
sandbox form, and chats and Activity for now. Where a screen has a key, it prints
`open in browser: <url>` in the notice line. `?` lists the URLs of the rest.

## Look

The terminal's own foreground and background, one accent (ANSI cyan) for focus and status,
and dim text for secondary facts. Colors are ANSI, so the user's palette applies. Bubble Tea's
renderer drops them when `NO_COLOR` is set, and the TUI is laid out for 80×24.

## Code

The package is `internal/tui`. `cmd/studio/tui.go` is the subcommand.

- `client.go` is the typed client. It adds no endpoints; its results are the API's own Go
  types (`sandboxes.View`, `api.ApprovalView`, `store.Teammate`, `events.Event`).
  Changes arrive on `/api/events`; a burst of them refetches once, after 300 ms.
- `app.go`, `keys.go`, `view.go`, `home.go`, `detail.go` are the Bubble Tea v2 model: state,
  messages, key handling and the frame. The key bar is Bubbles' `help`, and the bindings are
  Bubbles' `key`, so the bar and the handlers come from one list.
- `approvals.go` ports the web's card grouping (`web/src/lib/approval-groups.ts`) and the
  decision choices of `web/src/components/approval-decision.tsx`.
- `terminal.go` is the attachment: a `tea.ExecCommand`, so Bubble Tea releases the terminal
  while it runs. It bridges the websocket and reads the keys itself, which is what lets Ctrl-]
  return without a goroutine left reading the terminal.

Tests:

- Model tests drive `Update` with key and result messages.
- Golden views (`testdata/*.golden`) are the screens at 80×24 with `NO_COLOR`. After an
  intended change, `go test ./internal/tui -run Golden -update` rewrites them; review the diff.
- The client and the commands run against an in-process Studio: the API behind the same guards
  and an in-memory catalog, with a fake VM runtime. The test Studio never touches the real
  one.
- A pseudo-terminal test runs the TUI as a child process: it shows Home and quits. Another runs
  the attachment on a real terminal and checks that unbuffered input reaches the sandbox and
  that Ctrl-] detaches. Both are skipped on Windows.
