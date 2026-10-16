-- Kind local: a folder of repositories on this computer (docs/git-review.md). It has no
-- credential, so auth is 'none' and secret_id NULL. SQLite can't widen a CHECK, so forges is
-- rebuilt as in 031, with every column and both triggers.
CREATE TABLE forges_new (
    id                   TEXT PRIMARY KEY,
    environment_id       TEXT NOT NULL REFERENCES environments(id) ON DELETE CASCADE,
    name                 TEXT NOT NULL,
    kind                 TEXT NOT NULL CHECK (kind IN ('forgejo', 'github', 'gitlab', 'https', 'ssh', 'local')),
    base_url             TEXT NOT NULL,
    username             TEXT NOT NULL DEFAULT '',
    auth                 TEXT NOT NULL DEFAULT '',
    secret_id            TEXT UNIQUE REFERENCES secrets(id),
    public_key           TEXT NOT NULL DEFAULT '',
    host_key             TEXT NOT NULL DEFAULT '',
    host_key_offered     TEXT NOT NULL DEFAULT '',
    agent_slow_at        INTEGER,
    agent_hint_dismissed INTEGER NOT NULL DEFAULT 0,
    created_at           INTEGER NOT NULL,
    updated_at           INTEGER NOT NULL,
    UNIQUE (environment_id, name),
    CHECK (CASE kind WHEN 'ssh' THEN auth IN ('agent', 'key') WHEN 'local' THEN auth = 'none' ELSE auth = '' END),
    CHECK ((auth IN ('agent', 'none')) = (secret_id IS NULL))
);
INSERT INTO forges_new (id, environment_id, name, kind, base_url, username, auth, secret_id, public_key, host_key,
                        host_key_offered, agent_slow_at, agent_hint_dismissed, created_at, updated_at)
    SELECT id, environment_id, name, kind, base_url, username, auth, secret_id, public_key, host_key,
           host_key_offered, agent_slow_at, agent_hint_dismissed, created_at, updated_at FROM forges;
DROP TABLE forges;
ALTER TABLE forges_new RENAME TO forges;

CREATE TRIGGER forges_secret_owner_insert
BEFORE INSERT ON forges
WHEN NEW.secret_id IS NOT NULL
 AND NOT EXISTS (SELECT 1 FROM secrets WHERE id = NEW.secret_id AND environment_id = NEW.environment_id AND studio_only = 1)
BEGIN
    SELECT RAISE(ABORT, 'forge token must be a studio-only secret of the same environment');
END;

CREATE TRIGGER forges_secret_owner_update
BEFORE UPDATE OF secret_id, environment_id ON forges
WHEN NEW.secret_id IS NOT NULL
 AND NOT EXISTS (SELECT 1 FROM secrets WHERE id = NEW.secret_id AND environment_id = NEW.environment_id AND studio_only = 1)
BEGIN
    SELECT RAISE(ABORT, 'forge token must be a studio-only secret of the same environment');
END;
