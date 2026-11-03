# Session lines on Home

Home draws each sandbox as a line over the last 2, 8 or 24 hours. Every session the sandbox had
in that window branches off its line when it opens and rejoins it when it closes:

- **chats** above the line, one block per turn. A turn is live, waiting on the user (the
  sandbox has an approval waiting), stopped (cancelled or interrupted), or done;
- **terminals** below, one block per program that ran in them. A terminal the user opened, with
  no harness and no chat behind it, is drawn as the user's;
- **browser sessions** below, with a block for each stretch the user drove;
- **previews** (ports that listened) as dotted branches below.

On the line itself, checkpoints are hollow squares, merges filled squares and forks diamonds;
restores and moves to a new template are dots. A fork starts its own line, under its source and
marked ↳, at the time it was forked. The line is solid while the sandbox runs, and dashed after
its last activity once it stopped. A sandbox with nothing open folds into a single line with a
dot where each session opened, and any sandbox can be folded or unfolded; sandboxes that are
stopped and had nothing in the window are only counted. A branch links to what it draws: a chat
to its thread, a terminal, the browser or a preview to that tab of the sandbox's screen.

The page is `web/src/components/session-lines.tsx`; turning the data into branches, marks and
lanes is `web/src/lib/session-lines.ts`.

## Where the history comes from

`GET /api/environments/{env}/sessions?hours=` (`internal/api/sessionlines.go`) returns the
window's spans, chat turns and lineage events. Chats and lineage were already stored: turns are
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
Once every environment's sandboxes were listed, spans of sandboxes the pass no longer saw close
too (`CloseSessionSpansExcept`), so a deleted or stopped sandbox doesn't keep sessions open.
Closed spans are kept for 14 days. Sandbox IDs carry no foreign key, so the history outlives a
deleted sandbox until then.

Changes publish the `sessions` topic, which Home's query listens to along with `chats` and
`sandboxes`.

## Limits

- The terminal watcher looks every 10 seconds while a Studio tab is open and every minute
  otherwise, the browser recorder every 10 seconds. A session shorter than that may not be
  recorded, and opening and closing times are as precise as the pass that saw them.
- When Studio wasn't running, nothing is recorded; spans left open close on the first pass
  after it starts again, at that time.
