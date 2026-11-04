-- How busy each terminal and preview was, minute by minute, for Home's lines
-- (docs/design/session-lines.md). Chats and browser sessions need no row: their tool calls and
-- actions are in chat_items and browser_actions.
--
-- kind and key name the session as session_spans does: terminal (key: the terminal's name and
-- creation time) or preview (key: the port). minute is unix seconds / 60. count is how often
-- the terminal was seen to have printed since the last look, or how many requests the preview
-- answered; errors are the preview's answers of 500 and up. Like the spans, rows outlive their
-- sandbox and are kept for 14 days.
CREATE TABLE session_activity (
    environment_id TEXT NOT NULL REFERENCES environments(id) ON DELETE CASCADE,
    sandbox_id     TEXT NOT NULL,
    kind           TEXT NOT NULL,
    key            TEXT NOT NULL,
    minute         INTEGER NOT NULL,
    count          INTEGER NOT NULL DEFAULT 0,
    errors         INTEGER NOT NULL DEFAULT 0,
    PRIMARY KEY (environment_id, minute, sandbox_id, kind, key)
) WITHOUT ROWID;
