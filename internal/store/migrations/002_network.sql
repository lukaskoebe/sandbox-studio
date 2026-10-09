-- Each sandbox gets its own loopback DNS port, so lookups can be told apart per sandbox.
ALTER TABLE sandboxes ADD COLUMN dns_port INTEGER NOT NULL DEFAULT 0;

CREATE TABLE settings (
    key   TEXT PRIMARY KEY,
    value BLOB NOT NULL
);

-- Network rules. sandbox_id NULL means the rule covers the whole environment.
CREATE TABLE rules (
    id             TEXT PRIMARY KEY,
    environment_id TEXT NOT NULL REFERENCES environments(id) ON DELETE CASCADE,
    sandbox_id     TEXT REFERENCES sandboxes(id) ON DELETE CASCADE,
    host           TEXT NOT NULL,
    ports          TEXT NOT NULL,          -- comma-separated; empty means any port
    action         TEXT NOT NULL CHECK (action IN ('allow', 'proxy', 'caddy', 'deny')),
    config         TEXT NOT NULL DEFAULT '{}',
    note           TEXT NOT NULL DEFAULT '',
    created_at     INTEGER NOT NULL
);
CREATE INDEX rules_environment ON rules (environment_id);

-- Requests waiting for (or decided by) the user. subject deduplicates repeated attempts.
CREATE TABLE approvals (
    id             TEXT PRIMARY KEY,
    environment_id TEXT NOT NULL REFERENCES environments(id) ON DELETE CASCADE,
    sandbox_id     TEXT REFERENCES sandboxes(id) ON DELETE CASCADE,
    kind           TEXT NOT NULL,
    subject        TEXT NOT NULL,
    payload        TEXT NOT NULL,
    attempts       INTEGER NOT NULL DEFAULT 1,
    status         TEXT NOT NULL DEFAULT 'pending' CHECK (status IN ('pending', 'approved', 'denied', 'dismissed')),
    rule_id        TEXT,
    created_at     INTEGER NOT NULL,
    updated_at     INTEGER NOT NULL,
    decided_at     INTEGER
);
CREATE INDEX approvals_status ON approvals (environment_id, status, updated_at);
CREATE UNIQUE INDEX approvals_pending_subject ON approvals (environment_id, IFNULL(sandbox_id, ''), kind, subject) WHERE status = 'pending';
