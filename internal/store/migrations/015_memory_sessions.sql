-- Memory in agent sessions (PLAN §6.7). A harness session as Studio saw it through hooks;
-- sandbox_id has no foreign key so the log outlives the sandbox.
CREATE TABLE memory_agent_sessions (
    id             TEXT PRIMARY KEY,
    environment_id TEXT NOT NULL REFERENCES environments(id) ON DELETE CASCADE,
    persona_id     TEXT NOT NULL,
    sandbox_id     TEXT NOT NULL,
    sandbox_name   TEXT NOT NULL DEFAULT '',
    harness        TEXT NOT NULL,
    session_ref    TEXT NOT NULL,                 -- the harness's own session ID
    git_remote     TEXT NOT NULL DEFAULT '',
    started_at     INTEGER NOT NULL,
    last_event_at  INTEGER NOT NULL,
    UNIQUE (environment_id, sandbox_id, harness, session_ref)
);
CREATE INDEX memory_agent_sessions_persona ON memory_agent_sessions (environment_id, persona_id, started_at);

-- What memory did in a session: context packs and per-prompt recalls with why, writes,
-- extraction runs and tool calls. session_id is NULL for tool calls outside a known session.
CREATE TABLE memory_session_log (
    id             INTEGER PRIMARY KEY,
    environment_id TEXT NOT NULL REFERENCES environments(id) ON DELETE CASCADE,
    session_id     TEXT REFERENCES memory_agent_sessions(id) ON DELETE CASCADE,
    persona_id     TEXT NOT NULL,
    sandbox_id     TEXT NOT NULL,
    at             INTEGER NOT NULL,
    kind           TEXT NOT NULL CHECK (kind IN ('context', 'recall', 'write', 'extraction', 'tool')),
    item_type      TEXT NOT NULL DEFAULT '',      -- fact, page, conflict, approval
    item_id        TEXT NOT NULL DEFAULT '',
    score          REAL,
    why            TEXT NOT NULL DEFAULT '',      -- JSON: memory.Why for recalls
    summary        TEXT NOT NULL
);
CREATE INDEX memory_session_log_session ON memory_session_log (session_id, id);
CREATE INDEX memory_session_log_env ON memory_session_log (environment_id, persona_id, id);

-- Calls to the utility model for extraction, per environment and UTC day.
CREATE TABLE memory_utility_usage (
    environment_id TEXT NOT NULL REFERENCES environments(id) ON DELETE CASCADE,
    day            TEXT NOT NULL,                 -- YYYY-MM-DD
    calls          INTEGER NOT NULL DEFAULT 0,
    input_tokens   INTEGER NOT NULL DEFAULT 0,
    output_tokens  INTEGER NOT NULL DEFAULT 0,
    cost_micros    INTEGER NOT NULL DEFAULT 0,    -- estimated USD × 10^6
    skipped        INTEGER NOT NULL DEFAULT 0,    -- runs refused by the budget
    PRIMARY KEY (environment_id, day)
);
