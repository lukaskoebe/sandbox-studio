-- When each session of a sandbox was open, for Home's lines (docs/design/session-lines.md).
-- Studio sees terminals, browser sessions and previews only while they run, so its watchers
-- record a span when one appears and close it when it is gone. Chats need no span: their
-- turns are in chat_turns.
--
-- kind: terminal (a tmux session; key is its name and creation time), run (a program in a
-- terminal's foreground; parent is the terminal's key), browser (a session of a teammate's
-- browser driven from the sandbox; key is the session), drive (the user's takeover of a
-- browser session; parent is its key) or preview (a port listening; key is the port). Kinds
-- are checked in Go, so a new one needs no table rebuild.
--
-- label is what the session shows, such as "pnpm dev" or a URL. chat_id is the chat a
-- terminal continues or that opened a browser session; teammate_id is the teammate whose
-- browser it is; harness is the agent running in a terminal. closed_at is NULL while it is
-- open. The sandbox has no foreign key: the spans outlive it, like its lineage.
CREATE TABLE session_spans (
    id             TEXT PRIMARY KEY,
    environment_id TEXT NOT NULL REFERENCES environments(id) ON DELETE CASCADE,
    sandbox_id     TEXT NOT NULL,
    kind           TEXT NOT NULL,
    key            TEXT NOT NULL,
    parent         TEXT NOT NULL DEFAULT '',
    label          TEXT NOT NULL DEFAULT '',
    chat_id        TEXT NOT NULL DEFAULT '',
    teammate_id    TEXT NOT NULL DEFAULT '',
    harness        TEXT NOT NULL DEFAULT '',
    opened_at      INTEGER NOT NULL,
    closed_at      INTEGER
);
CREATE INDEX session_spans_time ON session_spans (environment_id, opened_at);
CREATE INDEX session_spans_open ON session_spans (environment_id, kind, sandbox_id) WHERE closed_at IS NULL;
