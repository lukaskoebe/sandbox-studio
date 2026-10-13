-- System sandboxes (docs/teammates.md, subscription logins): the VM of each login attempt and
-- the utility VM of a subscription provider. Their kinds join the CHECK on sandboxes.kind. SQLite
-- cannot change a CHECK in place, so the table is rebuilt the way SQLite's procedure describes:
-- a new table, the rows copied, the old table dropped and the new one renamed. Foreign keys are
-- off during the migration, so the references to sandboxes stay in place. Its indexes and
-- triggers are created again. SQLite checks every trigger when a table is renamed, so the
-- triggers of other tables that name sandboxes are dropped too, and created again at the end.
DROP TRIGGER templates_referenced_state_update;
DROP TRIGGER git_pushes_sandbox_owner;
DROP TRIGGER secrets_scope_insert;

CREATE TABLE sandboxes_new (
    id                     TEXT PRIMARY KEY,
    environment_id         TEXT NOT NULL REFERENCES environments(id) ON DELETE CASCADE,
    name                   TEXT NOT NULL,
    generation             INTEGER NOT NULL DEFAULT 1,
    cpus                   INTEGER NOT NULL,
    memory_mib             INTEGER NOT NULL,
    workspace_mib          INTEGER NOT NULL,
    docker_mib             INTEGER NOT NULL,
    created_at             INTEGER NOT NULL,
    dns_port               INTEGER NOT NULL DEFAULT 0,
    max_memory_mib         INTEGER NOT NULL DEFAULT 0,
    build_job_id           TEXT REFERENCES build_jobs(id),
    template_id            TEXT REFERENCES templates(id) ON DELETE RESTRICT,
    teammate_id            TEXT REFERENCES "teammates"(id),
    kind                   TEXT NOT NULL DEFAULT '' CHECK (kind IN ('', 'browser', 'claude-login', 'codex-login', 'claude-utility', 'codex-utility')),
    forked_from_sandbox_id TEXT,
    forked_from_generation INTEGER,
    forked_at              INTEGER,
    started_at             INTEGER,
    UNIQUE (environment_id, name)
);
INSERT INTO sandboxes_new (id, environment_id, name, generation, cpus, memory_mib, workspace_mib, docker_mib, created_at,
    dns_port, max_memory_mib, build_job_id, template_id, teammate_id, kind, forked_from_sandbox_id, forked_from_generation,
    forked_at, started_at)
    SELECT id, environment_id, name, generation, cpus, memory_mib, workspace_mib, docker_mib, created_at,
    dns_port, max_memory_mib, build_job_id, template_id, teammate_id, kind, forked_from_sandbox_id, forked_from_generation,
    forked_at, started_at FROM sandboxes;
DROP TABLE sandboxes;
ALTER TABLE sandboxes_new RENAME TO sandboxes;

CREATE UNIQUE INDEX sandboxes_environment_id_id ON sandboxes (environment_id, id);
CREATE UNIQUE INDEX sandboxes_build_job ON sandboxes (build_job_id) WHERE build_job_id IS NOT NULL;
CREATE UNIQUE INDEX sandboxes_browser ON sandboxes (teammate_id) WHERE kind = 'browser';
CREATE INDEX sandboxes_teammate ON sandboxes (teammate_id) WHERE teammate_id IS NOT NULL;
CREATE INDEX sandboxes_template_id ON sandboxes (template_id) WHERE template_id IS NOT NULL;

CREATE TRIGGER sandboxes_build_job_owner_insert
BEFORE INSERT ON sandboxes
WHEN NEW.build_job_id IS NOT NULL
 AND NOT EXISTS (SELECT 1 FROM build_jobs WHERE id = NEW.build_job_id AND environment_id = NEW.environment_id)
BEGIN
    SELECT RAISE(ABORT, 'sandbox build job must belong to the same environment');
END;

CREATE TRIGGER sandboxes_build_job_owner_update
BEFORE UPDATE OF build_job_id, environment_id ON sandboxes
WHEN NEW.build_job_id IS NOT NULL
 AND NOT EXISTS (SELECT 1 FROM build_jobs WHERE id = NEW.build_job_id AND environment_id = NEW.environment_id)
BEGIN
    SELECT RAISE(ABORT, 'sandbox build job must belong to the same environment');
END;

CREATE TRIGGER sandboxes_teammate_owner_insert
BEFORE INSERT ON sandboxes
WHEN NEW.teammate_id IS NOT NULL
 AND NOT EXISTS (SELECT 1 FROM teammates WHERE id = NEW.teammate_id AND environment_id = NEW.environment_id)
BEGIN
    SELECT RAISE(ABORT, 'sandbox teammate must belong to the same environment');
END;

CREATE TRIGGER sandboxes_teammate_owner_update
BEFORE UPDATE OF teammate_id, environment_id ON sandboxes
WHEN NEW.teammate_id IS NOT OLD.teammate_id
 OR (NEW.teammate_id IS NOT NULL AND NEW.environment_id IS NOT OLD.environment_id)
BEGIN
    SELECT RAISE(ABORT, 'sandbox teammate is immutable');
END;

CREATE TRIGGER sandboxes_template_owner_insert
BEFORE INSERT ON sandboxes
WHEN NEW.template_id IS NOT NULL
 AND NOT EXISTS (
    SELECT 1 FROM templates
    WHERE id = NEW.template_id
      AND environment_id = NEW.environment_id
      AND state = 'ready'
 )
BEGIN
    SELECT RAISE(ABORT, 'sandbox template must be ready and belong to the same environment');
END;

CREATE TRIGGER sandboxes_template_owner_update
BEFORE UPDATE OF template_id, environment_id ON sandboxes
WHEN NEW.template_id IS NOT NULL
 AND NOT EXISTS (
    SELECT 1 FROM templates
    WHERE id = NEW.template_id
      AND environment_id = NEW.environment_id
      AND state = 'ready'
 )
BEGIN
    SELECT RAISE(ABORT, 'sandbox template must be ready and belong to the same environment');
END;

CREATE TRIGGER templates_referenced_state_update
BEFORE UPDATE OF state ON templates
WHEN NEW.state = 'deleting'
 AND (EXISTS (SELECT 1 FROM sandboxes WHERE template_id = OLD.id)
      OR EXISTS (SELECT 1 FROM rebases WHERE template_id = OLD.id))
BEGIN
    SELECT RAISE(ABORT, 'referenced template cannot be marked deleting');
END;

CREATE TRIGGER git_pushes_sandbox_owner
BEFORE INSERT ON git_pushes
WHEN NOT EXISTS (SELECT 1 FROM sandboxes WHERE id = NEW.sandbox_id AND environment_id = NEW.environment_id)
BEGIN
    SELECT RAISE(ABORT, 'push sandbox must belong to the same environment');
END;

CREATE TRIGGER secrets_scope_insert
BEFORE INSERT ON secrets
WHEN (NEW.teammate_id IS NOT NULL
      AND NOT EXISTS (SELECT 1 FROM teammates WHERE id = NEW.teammate_id AND environment_id = NEW.environment_id))
  OR (NEW.sandbox_id IS NOT NULL
      AND NOT EXISTS (SELECT 1 FROM sandboxes WHERE id = NEW.sandbox_id AND environment_id = NEW.environment_id))
BEGIN
    SELECT RAISE(ABORT, 'secret teammate or sandbox must belong to the same environment');
END;

-- A subscription provider holds a secret too: a login keeps its Claude Code token, or its
-- ChatGPT access token's signature, there (docs/teammates.md, subscription logins). Migration 026
-- refused that with a CHECK, so providers is rebuilt without it, the same way as sandboxes above.
-- Its owner triggers are created again; teammates' composite key on providers stays valid.
CREATE TABLE providers_new (
    id             TEXT PRIMARY KEY,
    environment_id TEXT NOT NULL REFERENCES environments(id) ON DELETE CASCADE,
    name           TEXT NOT NULL,
    kind           TEXT NOT NULL CHECK (kind IN ('anthropic_api', 'openai_api', 'openai_compatible', 'claude_subscription', 'chatgpt_subscription')),
    base_url       TEXT NOT NULL DEFAULT '',   -- openai_compatible only
    model          TEXT NOT NULL DEFAULT '',   -- openai_compatible only: its single model
    secret_id      TEXT UNIQUE REFERENCES secrets(id),
    created_at     INTEGER NOT NULL,
    updated_at     INTEGER NOT NULL,
    UNIQUE (environment_id, name),
    UNIQUE (environment_id, id),
    CHECK ((kind = 'openai_compatible') = (base_url <> '' AND model <> ''))
);
INSERT INTO providers_new (id, environment_id, name, kind, base_url, model, secret_id, created_at, updated_at)
    SELECT id, environment_id, name, kind, base_url, model, secret_id, created_at, updated_at FROM providers;
DROP TABLE providers;
ALTER TABLE providers_new RENAME TO providers;

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
