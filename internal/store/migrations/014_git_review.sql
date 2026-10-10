-- Git review (M8). Numbered 015 to leave 014 to work landing in parallel.

-- Studio-only secrets never reach sandboxes: no placeholder in their environment and no
-- substitution by the gateway. Studio itself uses them, e.g. a forge's token.
ALTER TABLE secrets ADD COLUMN studio_only INTEGER NOT NULL DEFAULT 0;

-- Forges an environment's sandboxes push to through the virtual git remote. The name is
-- the URL segment: https://git.studio.internal/<name>/<owner>/<repo>.git
CREATE TABLE forges (
    id             TEXT PRIMARY KEY,
    environment_id TEXT NOT NULL REFERENCES environments(id) ON DELETE CASCADE,
    name           TEXT NOT NULL,
    kind           TEXT NOT NULL CHECK (kind IN ('forgejo', 'github')),
    base_url       TEXT NOT NULL,
    secret_id      TEXT NOT NULL UNIQUE REFERENCES secrets(id),
    created_at     INTEGER NOT NULL,
    updated_at     INTEGER NOT NULL,
    UNIQUE (environment_id, name)
);

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

-- A push a sandbox made to the virtual remote, staged on the host until the user decides.
-- Persona and forge are copied so the record stays readable after either is deleted.
CREATE TABLE git_pushes (
    id             TEXT PRIMARY KEY,
    environment_id TEXT NOT NULL REFERENCES environments(id) ON DELETE CASCADE,
    sandbox_id     TEXT NOT NULL REFERENCES sandboxes(id) ON DELETE CASCADE,
    persona_name   TEXT NOT NULL DEFAULT '',
    forge_id       TEXT NOT NULL,
    forge_name     TEXT NOT NULL,
    owner          TEXT NOT NULL,
    repo           TEXT NOT NULL,
    ref            TEXT NOT NULL,           -- refs/heads/...
    old_sha        TEXT NOT NULL,           -- zeros for a new branch
    new_sha        TEXT NOT NULL,
    default_branch TEXT NOT NULL DEFAULT '',
    approval_id    TEXT NOT NULL,
    state          TEXT NOT NULL CHECK (state IN ('pending', 'pushed', 'failed', 'rejected', 'superseded')),
    result         TEXT NOT NULL DEFAULT '',
    note           TEXT NOT NULL DEFAULT '',
    delivered_at   INTEGER,                 -- when the sandbox was told it was rejected or failed
    pr_approval_id TEXT NOT NULL DEFAULT '',
    pr_url         TEXT NOT NULL DEFAULT '',
    pr_result      TEXT NOT NULL DEFAULT '',
    created_at     INTEGER NOT NULL,
    updated_at     INTEGER NOT NULL
);
CREATE INDEX git_pushes_approval ON git_pushes (environment_id, approval_id);
CREATE INDEX git_pushes_pr_approval ON git_pushes (environment_id, pr_approval_id) WHERE pr_approval_id <> '';
CREATE INDEX git_pushes_repo ON git_pushes (sandbox_id, forge_id, owner, repo);

CREATE TRIGGER git_pushes_sandbox_owner
BEFORE INSERT ON git_pushes
WHEN NOT EXISTS (SELECT 1 FROM sandboxes WHERE id = NEW.sandbox_id AND environment_id = NEW.environment_id)
BEGIN
    SELECT RAISE(ABORT, 'push sandbox must belong to the same environment');
END;
