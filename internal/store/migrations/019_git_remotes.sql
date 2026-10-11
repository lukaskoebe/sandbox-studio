-- Git remotes over GitHub, any HTTPS host and SSH (docs/design/git-remotes.md). Numbered
-- 019: 018 belongs to work landing in parallel.

-- SQLite can't widen a CHECK, so forges is rebuilt. secret_id holds the token, or for
-- kind ssh the private key Studio generated. host_key is the SSH host key the user
-- confirmed, in authorized_keys format; host_key_offered is the fingerprint of a different
-- key the host presented since, until the user re-pins.
CREATE TABLE forges_new (
    id               TEXT PRIMARY KEY,
    environment_id   TEXT NOT NULL REFERENCES environments(id) ON DELETE CASCADE,
    name             TEXT NOT NULL,
    kind             TEXT NOT NULL CHECK (kind IN ('forgejo', 'github', 'https', 'ssh')),
    base_url         TEXT NOT NULL,
    username         TEXT NOT NULL DEFAULT '',
    secret_id        TEXT NOT NULL UNIQUE REFERENCES secrets(id),
    public_key       TEXT NOT NULL DEFAULT '',
    host_key         TEXT NOT NULL DEFAULT '',
    host_key_offered TEXT NOT NULL DEFAULT '',
    created_at       INTEGER NOT NULL,
    updated_at       INTEGER NOT NULL,
    UNIQUE (environment_id, name)
);
INSERT INTO forges_new (id, environment_id, name, kind, base_url, secret_id, created_at, updated_at)
    SELECT id, environment_id, name, kind, base_url, secret_id, created_at, updated_at FROM forges;
DROP TABLE forges;
ALTER TABLE forges_new RENAME TO forges;

CREATE TRIGGER forges_secret_owner_insert
BEFORE INSERT ON forges
WHEN NOT EXISTS (SELECT 1 FROM secrets WHERE id = NEW.secret_id AND environment_id = NEW.environment_id AND studio_only = 1)
BEGIN
    SELECT RAISE(ABORT, 'forge token must be a studio-only secret of the same environment');
END;

CREATE TRIGGER forges_secret_owner_update
BEFORE UPDATE OF secret_id, environment_id ON forges
WHEN NOT EXISTS (SELECT 1 FROM secrets WHERE id = NEW.secret_id AND environment_id = NEW.environment_id AND studio_only = 1)
BEGIN
    SELECT RAISE(ABORT, 'forge token must be a studio-only secret of the same environment');
END;
