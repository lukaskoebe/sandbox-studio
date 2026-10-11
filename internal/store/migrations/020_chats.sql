-- Chats (docs/design/chat-mode.md): a teammate's harness driven over ACP, shown as a
-- conversation. The agent keeps its own transcript in the guest; these tables hold only
-- what Studio shows.
--
-- rev is a per-chat change counter: every write to a chat, its turns or its items takes
-- the chat's next rev, so a stream reconnecting with the last rev it saw misses nothing.
CREATE TABLE chats (
    id             TEXT PRIMARY KEY,
    environment_id TEXT NOT NULL REFERENCES environments(id) ON DELETE CASCADE,
    sandbox_id     TEXT NOT NULL REFERENCES sandboxes(id) ON DELETE CASCADE,
    teammate_id    TEXT NOT NULL,
    harness        TEXT NOT NULL,
    acp_session_id TEXT NOT NULL DEFAULT '',
    title          TEXT NOT NULL DEFAULT '',
    state          TEXT NOT NULL DEFAULT 'idle' CHECK (state IN ('idle', 'running', 'terminal', 'orphaned')),
    rev            INTEGER NOT NULL DEFAULT 0,
    created_at     INTEGER NOT NULL,
    updated_at     INTEGER NOT NULL
);
CREATE INDEX chats_environment ON chats (environment_id, updated_at);
CREATE INDEX chats_sandbox ON chats (sandbox_id);

CREATE TABLE chat_turns (
    id          TEXT PRIMARY KEY,
    chat_id     TEXT NOT NULL REFERENCES chats(id) ON DELETE CASCADE,
    seq         INTEGER NOT NULL,
    snapshot    TEXT NOT NULL DEFAULT '',     -- turn snapshot taken before the prompt
    stop_reason TEXT NOT NULL DEFAULT '',     -- ACP stop reason, or interrupted
    usage       BLOB,                         -- JSON: tokens, cost
    changes     BLOB,                         -- JSON: files changed by the turn
    origin      TEXT NOT NULL DEFAULT 'chat' CHECK (origin IN ('chat', 'terminal')),
    undone_at   INTEGER,
    started_at  INTEGER NOT NULL,
    ended_at    INTEGER,
    rev         INTEGER NOT NULL DEFAULT 0,
    UNIQUE (chat_id, seq)
);
CREATE INDEX chat_turns_rev ON chat_turns (chat_id, rev);
CREATE INDEX chat_turns_open ON chat_turns (chat_id) WHERE ended_at IS NULL;

CREATE TABLE chat_items (
    id           TEXT PRIMARY KEY,
    chat_id      TEXT NOT NULL REFERENCES chats(id) ON DELETE CASCADE,
    turn_id      TEXT NOT NULL REFERENCES chat_turns(id) ON DELETE CASCADE,
    seq          INTEGER NOT NULL,           -- order within the chat
    kind         TEXT NOT NULL CHECK (kind IN ('user', 'agent', 'thought', 'tool', 'plan', 'permission', 'question', 'marker')),
    tool_call_id TEXT NOT NULL DEFAULT '',
    status       TEXT NOT NULL DEFAULT '',
    approval_id  TEXT NOT NULL DEFAULT '',
    body         BLOB NOT NULL,              -- JSON, kind-specific, at most 256 KiB
    updated_at   INTEGER NOT NULL,
    rev          INTEGER NOT NULL DEFAULT 0,
    UNIQUE (chat_id, seq)
);
CREATE INDEX chat_items_rev ON chat_items (chat_id, rev);
CREATE INDEX chat_items_turn ON chat_items (turn_id);
CREATE INDEX chat_items_approval ON chat_items (approval_id) WHERE approval_id != '';
