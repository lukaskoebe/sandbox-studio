# Session lines on Home

Home draws each sandbox as a line over the last 30 minutes, hour, 2, 8 or 24 hours (the pick is
kept per browser). Every session the sandbox had in that window branches off its line when it
opens and rejoins it when it closes:

- **chats** above the line, a thin bar per turn. A turn is live, waiting on the user (the
  sandbox has an approval waiting), stopped (cancelled or interrupted), or done;
- **terminals** below, a thin bar per program that ran in them, named under the lane. A terminal
  the user opened, with no harness and no chat behind it, is drawn as the user's;
- **browser sessions** below, each stretch the user drove outlined;
- **previews** (ports that listened) as dotted branches below. Three or more previews whose
  spans overlap or follow within 10 minutes, such as a dev server's ports, share one branch
  named after the count and the ports, with a dot where each opened and a ring where one
  closed while the others stayed open (`groupPreviews`).

On the line itself, checkpoints are hollow squares, merges filled squares and forks diamonds;
restores and moves to a new template are dots. A fork starts its own line, under its source and
marked ↳, at the time it was forked. The line is solid while the sandbox runs, and dashed after
its last activity once it stopped. A sandbox with nothing open folds into a single line with a
dot where each session opened, and any sandbox can be folded or unfolded; sandboxes that are
stopped and had nothing in the window are only counted. A branch links to what it draws: a chat
to its thread, a terminal, the browser or a preview to that tab of the sandbox's screen, a
preview on its port (`?tab=preview&port=`) and a group of previews on the newest one still
open. While previews are off the Preview tab still opens from there, saying so, listing the
ports and offering to turn previews on.

## Pulse: how busy, and what is worth a word

A branch widens either side of its lane with how busy its session was (mock L4, "Pulse"):

| Branch | Counts | Errors (red ticks) |
|---|---|---|
| chat | tool calls (`chat_items` of kind `tool`) | failed tool calls |
| terminal | seconds the watcher saw it printing | — |
| browser | the teammate's browser actions (`browser_actions`) | failed or denied actions |
| preview | requests the preview proxy answered | answers of 500 and up |

Counts are per minute and summed into stretches of whole minutes at least 6 pixels wide, so the
30-minute window shows each minute and 24 hours a stretch of about ten. A stretch's width is the
square root of its share of the window's busiest minute of that kind, so a little work still
shows; each stretch's tooltip says what it counted. A folded sandbox draws its branches' widths
on its single line.

Flags mark what the user would want to know, each with a tick and its words where they fit
beside the labels (on the lane's label row, or the row above), the rest in its tooltip
(`SessionFlags`):

- **✗ failed**: a command a chat ran (`execute` tool call) failed, on the chat's branch;
- **⏸ asked you**: an approval of the sandbox, on its chat's branch when it is a chat's
  permission request and on the line otherwise, with its wait dashed until it was answered and
  the answer ("yes, 4m later"). Network requests count only while they wait;
- **⚠ blocked**: a network request was denied, with how often it was tried. Trackers and
  Chromium's own requests are left out;
- **▲ push**: a push went out (`git_pushes` pushed, failed or rejected), on the line, red when
  it failed.

The quiet style draws the lines in sans (`--lines-font`), with round line ends, rounded bars and
marks, and widths smoothed between stretches instead of pixel's steps.

The page is `web/src/components/session-lines.tsx`; turning the data into branches, marks,
flags and lanes is `web/src/lib/session-lines.ts`.

## Where the history comes from

`GET /api/environments/{env}/sessions?hours=` (or `?minutes=` for windows under two hours;
`internal/api/sessionlines.go`) returns the window's spans, chat turns, lineage events,
activity and flags. Chats and lineage were already stored: turns are
`chat_turns` joined with their chat (at most 2000, the newest kept), lineage is
`sandbox_events`. A chat's turns form one branch while they follow each other within 30
minutes; a chat whose last turn ended less than 30 minutes ago is still open.

Terminals, browser sessions and previews are only ever live, so Studio records them in
`session_spans` (migration 058) as it sees them:

| Kind | Key | Recorded by |
|---|---|---|
| `terminal` | `name@created` | the terminal watcher (`internal/sandboxes/sessionspans.go`) |
| `run` | `terminal-key@started`, parent the terminal | the terminal watcher, while the terminal is busy |
| `preview` | the port | the terminal watcher, from the ports listening |
| `browser` | the page's ID, or `profile:<session>` for the shared profile page | `browser.RecordSessions` |
| `drive` | `page-key@since`, parent the page | `browser.RecordSessions`, while the user drives |

Each pass hands the store what is live for one sandbox and kind (`SyncSessionSpans`): spans
that are no longer live close at that time, new ones open at the time the guest gives them
(a terminal's creation, a program's start), and a span that stays open takes its newest label.
Several callers sync the same sandbox (the watcher, a tab listing its terminals, the agent's
terminal tools), so syncs run one at a time and a session has at most one open span, which a
unique index keeps (migration 059, which also removed the copies that concurrent syncs had
recorded before).
Once every environment's sandboxes were listed, spans of sandboxes the pass no longer saw close
too (`CloseSessionSpansExcept`), so a deleted or stopped sandbox doesn't keep sessions open.
Closed spans are kept for 14 days. Sandbox IDs carry no foreign key, so the history outlives a
deleted sandbox until then.

Terminals' output and previews' requests are counted in memory (`internal/sandboxes/activity.go`):
each look at a sandbox's terminals adds, for each terminal whose latest output moved on since
the previous look, the seconds between the looks (at most a minute) at the minute of that
output; the preview proxy (`preview.Route`) tells `CountPreview` of each request with its
status. The watcher writes the counts at the end of each pass into `session_activity`
(migration 060, one row per environment, minute, sandbox, kind and key), kept for 14 days like
the spans. Chats' tool calls and browser actions need no counting: they are read from
`chat_items` and `browser_actions`. Flags are read from `approvals`, `git_pushes` and
`chat_items`, at most 300 of each source per window.

Changes publish the `sessions` topic, which Home's query listens to along with `chats` and
`sandboxes`.

## Limits

- The terminal watcher looks every 10 seconds while a Studio tab is open and every minute
  otherwise, the browser recorder every 10 seconds. A session shorter than that may not be
  recorded, and opening and closing times are as precise as the pass that saw them.
- A terminal's output is only as fine as the looks: a burst between two looks counts the same as
  printing throughout. While no tab is open the watcher looks once a minute, and a look covers
  at most a minute.
- When Studio wasn't running, nothing is recorded; spans left open close on the first pass
  after it starts again, at that time.
