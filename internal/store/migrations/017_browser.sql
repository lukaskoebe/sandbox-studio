-- The browser broker (PLAN §6.8). A persona's browser VM is a sandbox row of kind
-- 'browser': one per persona, created lazily, hidden from the sandbox API.
ALTER TABLE sandboxes ADD COLUMN kind TEXT NOT NULL DEFAULT '' CHECK (kind IN ('', 'browser'));
CREATE UNIQUE INDEX sandboxes_browser ON sandboxes (persona_id) WHERE kind = 'browser';

-- What a persona's agents may do in its browser, beyond the defaults. allow and deny decide
-- actions; sensitive marks fields whose values snapshots blank. origin is
-- scheme://host[:port], the host may start with *.; role and label empty match any
-- element, label is a case-insensitive glob.
CREATE TABLE browser_patterns (
    id             TEXT PRIMARY KEY,
    environment_id TEXT NOT NULL REFERENCES environments(id) ON DELETE CASCADE,
    persona_id     TEXT NOT NULL REFERENCES personas(id) ON DELETE CASCADE,
    action         TEXT NOT NULL,
    origin         TEXT NOT NULL,
    role           TEXT NOT NULL DEFAULT '',
    label          TEXT NOT NULL DEFAULT '',
    verdict        TEXT NOT NULL CHECK (verdict IN ('allow', 'deny', 'sensitive')),
    created_at     INTEGER NOT NULL,
    UNIQUE (persona_id, action, origin, role, label, verdict)
);
CREATE INDEX browser_patterns_persona ON browser_patterns (environment_id, persona_id);

-- The action log of a persona's browser. session_id groups the actions of one run of the
-- browser VM; screenshot names a JPEG under <data>/browser/. Never holds credential values.
CREATE TABLE browser_actions (
    id             INTEGER PRIMARY KEY,
    environment_id TEXT NOT NULL REFERENCES environments(id) ON DELETE CASCADE,
    persona_id     TEXT NOT NULL,
    session_id     TEXT NOT NULL,
    sandbox_id     TEXT NOT NULL DEFAULT '',      -- the driving agent sandbox; empty for the user
    actor          TEXT NOT NULL CHECK (actor IN ('agent', 'user')),
    action         TEXT NOT NULL,
    target         TEXT NOT NULL DEFAULT '',
    url            TEXT NOT NULL DEFAULT '',
    outcome        TEXT NOT NULL CHECK (outcome IN ('ok', 'denied', 'pending', 'failed')),
    detail         TEXT NOT NULL DEFAULT '',
    screenshot     TEXT NOT NULL DEFAULT '',
    at             INTEGER NOT NULL
);
CREATE INDEX browser_actions_persona ON browser_actions (environment_id, persona_id, id);
CREATE INDEX browser_actions_session ON browser_actions (session_id, id);
