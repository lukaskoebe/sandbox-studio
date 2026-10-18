# Home as a sidebar and a pane (design)

Status: built (2026-10-18); [tui.md](../tui.md#the-pane) describes it as it is. It follows
[tui-sessions.md](tui-sessions.md), which put every session one key away, and is mock A2 of the
layout proposals: a sidebar of sandboxes with their sessions, and beside it the selected
session as it runs, as herdr shows its agents.

## 1. Why

With sessions on Home, an agent was one key away, but entering it still took the whole
screen. Seeing what three agents do meant entering each in turn. The detail pane beside the
list only showed the sandbox's facts.

## 2. Layout

From 100 columns (`paneWidth`), Home is two columns:

- **The sidebar**, a third of the width, between 32 and 44 columns. Each sandbox is a group
  header, `blub · Iris ───── ▁▂▄ ● 2h`: the name, the teammate, a CPU sparkline when there is
  room, the approvals waiting (`▲ 2`) and the status as a dot and a short uptime. Its sessions
  follow, numbered, each with its state and its kind on the right: `agent`, `chat`, `term`,
  `sched` (a scheduled task's run) or `check` (a check-in's). A blank line separates the groups.
- **The pane** shows what the cursor is on:
  - a sandbox's header: its dashboard (section 4);
  - an agent's or another terminal: its screen, live;
  - a chat: its transcript, streaming, with the composer;
  - the `+ n older` row: a hint that enter lists them.

Below 100 columns Home is the list alone, at full width with the template in each header, and
a session still opens full screen, as before.

The two columns are split by a dim `│`. While the pane has the keys, the split is the accent
`┃` and the rule under the pane's title is the accent `━`, so you can tell where the keys go.

## 3. The pane's keys

The sidebar has the keys until you hand them over:

| Key | In the sidebar | In the pane |
|---|---|---|
| `enter`, `1`–`9`, a click on the pane | give the pane the keys, on that session | |
| `Ctrl-]` | | gives the keys back; `esc` too in a chat without a composer |
| `alt+1`–`alt+9` | | switch to that session, keeping the keys in the pane |
| `alt+z` | puts the pane in full screen, and back | the same |
| everything else | the sidebar's keys | the terminal's, `ctrl+c` and `q` included |

A chat in the pane takes the chat screen's keys. An idle chat opens its composer when the pane
gets the keys; `esc` puts the prompt aside and a second `esc` gives the keys back.

Ctrl-], alt+digits and alt+z are what the pane keeps for itself. Ctrl-] is the attachment's
detach key, so it means the same everywhere. Alt keys depend on the terminal sending them as
`ESC` and the key, which most do; macOS terminals need Option set to act as Meta.

### The terminal

The pane is a terminal emulator (`charmbracelet/x/vt`) fed by the attachment's websocket, the
one the web terminal and `t` use. The emulator's screen is drawn cell by cell into the pane,
with the real cursor placed in it while the pane has the keys. Keys go through the emulator,
which encodes them as the terminal would, so arrows, function keys and bracketed paste work.

- The cursor has to rest on a session for 150 ms before the pane connects, so moving over
  the list doesn't open every terminal on the way.
- Moving the cursor off the session closes its attachment. The tmux session goes on.
- A redraw waits 16 ms after output, so a burst of output is one frame.
- The pane's size is sent on connect and whenever the pane changes size. tmux sizes a session
  to its latest client, so the pane and the web terminal resize each other when both show it.
- When the terminal ends, the pane says so, and enter opens it again.

### The approval banner

A blocked agent's screen gets a box over its top rows in the waiting color: the approval that
waits in its sandbox, `api-refactor wants proxy.golang.org:443`. When it is a single network
request, `y`, `o` and `n` in the sidebar allow it, allow it once or deny it, without leaving
Home. Any other kind says `a approvals`. While the pane has the keys, those keys belong to the
agent, so the banner says `Ctrl-] then y allow …`. A chat's permission request is in its
transcript and gets no banner.

## 4. The dashboard

A sandbox's header shows its dashboard in the pane:

- the name and status with uptime, then its teammate, template, CPUs and memory;
- **CPU** as an area chart of the last 2 minutes, 60 samples, each one or two columns wide.
  Each column fills in eighths of a cell, 3 or 4 rows high, with a `100┤` / `0┤` axis.
  Samples not read yet are a dim dotted baseline. The top quarter is in the waiting color.
  The header gives the current share of the sandbox's CPUs, the mean and the peak;
- **memory** as a gauge bar with its share, and a one-row history under it;
- the first approval waiting, as a card;
- its terminals, ports, history line and ID;
- its newest connections, as many as fit up to 8, then its teammate's browser.

A pane under 20 rows shows the old one-line usage instead of the charts. The plain look has
no charts.

## 5. What it replaced

The detail pane beside Home's list at 120 columns, and the network column at 160, are gone.
The dashboard holds both. The full-screen sandbox detail (`enter` below 100 columns) stays.

## 6. Later

- An approval banner for kinds other than network, deciding in place.
- Keeping attachments open in the background, so switching back shows the screen at once,
  and so herdr's blocked can be read from the screen, as in tui-sessions.md section 6.
- A split of the pane itself, to watch two agents side by side.
