-- Secrets get the scopes rules have: the environment, a teammate (every sandbox it owns) or
-- one sandbox. A name is unique per scope; for a sandbox the most specific one wins.
-- The table is rebuilt to replace UNIQUE (environment_id, name). It is copied out and back
-- rather than renamed, so the triggers of providers and forges that name it stay valid.

CREATE TABLE secrets_old AS SELECT * FROM secrets;
DROP TABLE secrets;

CREATE TABLE secrets (
    id             TEXT PRIMARY KEY,
    environment_id TEXT NOT NULL REFERENCES environments(id) ON DELETE CASCADE,
    teammate_id    TEXT REFERENCES teammates(id) ON DELETE CASCADE,
    sandbox_id     TEXT REFERENCES sandboxes(id) ON DELETE CASCADE,
    name           TEXT NOT NULL,          -- also the env var name sandboxes see
    value          BLOB NOT NULL,          -- sealed by the vault (nonce || AES-GCM ciphertext)
    hosts          TEXT NOT NULL,          -- comma-separated host patterns the value may be sent to
    placeholder    TEXT NOT NULL UNIQUE,   -- what sandboxes see instead of the value
    note           TEXT NOT NULL DEFAULT '',
    created_at     INTEGER NOT NULL,
    updated_at     INTEGER NOT NULL,
    studio_only    INTEGER NOT NULL DEFAULT 0,
    CHECK (teammate_id IS NULL OR sandbox_id IS NULL),
    -- Studio's own secrets (forge credentials) belong to the environment.
    CHECK (studio_only = 0 OR (teammate_id IS NULL AND sandbox_id IS NULL))
);
INSERT INTO secrets (id, environment_id, name, value, hosts, placeholder, note, created_at, updated_at, studio_only)
SELECT id, environment_id, name, value, hosts, placeholder, note, created_at, updated_at, studio_only FROM secrets_old;
DROP TABLE secrets_old;

CREATE UNIQUE INDEX secrets_environment_name ON secrets (environment_id, name) WHERE teammate_id IS NULL AND sandbox_id IS NULL;
CREATE UNIQUE INDEX secrets_teammate_name ON secrets (teammate_id, name) WHERE teammate_id IS NOT NULL;
CREATE UNIQUE INDEX secrets_sandbox_name ON secrets (sandbox_id, name) WHERE sandbox_id IS NOT NULL;

CREATE TRIGGER secrets_scope_insert
BEFORE INSERT ON secrets
WHEN (NEW.teammate_id IS NOT NULL
      AND NOT EXISTS (SELECT 1 FROM teammates WHERE id = NEW.teammate_id AND environment_id = NEW.environment_id))
  OR (NEW.sandbox_id IS NOT NULL
      AND NOT EXISTS (SELECT 1 FROM sandboxes WHERE id = NEW.sandbox_id AND environment_id = NEW.environment_id))
BEGIN
    SELECT RAISE(ABORT, 'secret teammate or sandbox must belong to the same environment');
END;

-- The scope is fixed at creation.
CREATE TRIGGER secrets_scope_update
BEFORE UPDATE OF environment_id, teammate_id, sandbox_id ON secrets
WHEN NEW.environment_id IS NOT OLD.environment_id
  OR NEW.teammate_id IS NOT OLD.teammate_id
  OR NEW.sandbox_id IS NOT OLD.sandbox_id
BEGIN
    SELECT RAISE(ABORT, 'secret scope is immutable');
END;

-- A provider's key belongs to its environment as a whole.
DROP TRIGGER providers_secret_owner_insert;
DROP TRIGGER providers_secret_owner_update;

CREATE TRIGGER providers_secret_owner_insert
BEFORE INSERT ON providers
WHEN NEW.secret_id IS NOT NULL
 AND NOT EXISTS (SELECT 1 FROM secrets WHERE id = NEW.secret_id AND environment_id = NEW.environment_id
                 AND teammate_id IS NULL AND sandbox_id IS NULL)
BEGIN
    SELECT RAISE(ABORT, 'provider secret must be an environment secret of the same environment');
END;

CREATE TRIGGER providers_secret_owner_update
BEFORE UPDATE OF secret_id, environment_id ON providers
WHEN NEW.secret_id IS NOT NULL
 AND NOT EXISTS (SELECT 1 FROM secrets WHERE id = NEW.secret_id AND environment_id = NEW.environment_id
                 AND teammate_id IS NULL AND sandbox_id IS NULL)
BEGIN
    SELECT RAISE(ABORT, 'provider secret must be an environment secret of the same environment');
END;
