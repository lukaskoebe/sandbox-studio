-- LLM access of an environment. API-key providers own one vault secret holding the key;
-- subscription providers have none until their login flow exists.
CREATE TABLE providers (
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
    CHECK ((secret_id IS NULL) = (kind IN ('claude_subscription', 'chatgpt_subscription'))),
    CHECK ((kind = 'openai_compatible') = (base_url <> '' AND model <> ''))
);

-- A provider's key lives in its own environment.
CREATE TRIGGER providers_secret_owner_insert
BEFORE INSERT ON providers
WHEN NEW.secret_id IS NOT NULL
 AND NOT EXISTS (SELECT 1 FROM secrets WHERE id = NEW.secret_id AND environment_id = NEW.environment_id)
BEGIN
    SELECT RAISE(ABORT, 'provider secret must belong to the same environment');
END;

CREATE TRIGGER providers_secret_owner_update
BEFORE UPDATE OF secret_id, environment_id ON providers
WHEN NEW.secret_id IS NOT NULL
 AND NOT EXISTS (SELECT 1 FROM secrets WHERE id = NEW.secret_id AND environment_id = NEW.environment_id)
BEGIN
    SELECT RAISE(ABORT, 'provider secret must belong to the same environment');
END;

CREATE TABLE personas (
    id             TEXT PRIMARY KEY,
    environment_id TEXT NOT NULL REFERENCES environments(id) ON DELETE CASCADE,
    name           TEXT NOT NULL,
    role           TEXT NOT NULL DEFAULT '',
    soul           TEXT NOT NULL DEFAULT '',
    harness        TEXT NOT NULL CHECK (harness IN ('opencode', 'claude', 'codex')),
    provider_id    TEXT NOT NULL,
    model          TEXT NOT NULL DEFAULT '',
    git_name       TEXT NOT NULL,
    git_email      TEXT NOT NULL,
    created_at     INTEGER NOT NULL,
    updated_at     INTEGER NOT NULL,
    UNIQUE (environment_id, name),
    FOREIGN KEY (environment_id, provider_id) REFERENCES providers(environment_id, id)
);
CREATE INDEX personas_provider ON personas (provider_id);

-- Sandboxes may belong to a persona of their environment; existing ones stay unowned.
ALTER TABLE sandboxes ADD COLUMN persona_id TEXT REFERENCES personas(id);
CREATE INDEX sandboxes_persona ON sandboxes (persona_id) WHERE persona_id IS NOT NULL;

CREATE TRIGGER sandboxes_persona_owner_insert
BEFORE INSERT ON sandboxes
WHEN NEW.persona_id IS NOT NULL
 AND NOT EXISTS (SELECT 1 FROM personas WHERE id = NEW.persona_id AND environment_id = NEW.environment_id)
BEGIN
    SELECT RAISE(ABORT, 'sandbox persona must belong to the same environment');
END;

-- Ownership is fixed at creation.
CREATE TRIGGER sandboxes_persona_owner_update
BEFORE UPDATE OF persona_id, environment_id ON sandboxes
WHEN NEW.persona_id IS NOT OLD.persona_id
 OR (NEW.persona_id IS NOT NULL AND NEW.environment_id IS NOT OLD.environment_id)
BEGIN
    SELECT RAISE(ABORT, 'sandbox persona is immutable');
END;

-- Rules can be scoped to a persona: they apply to every sandbox it owns. A rule has at
-- most one of the two scopes.
ALTER TABLE rules ADD COLUMN persona_id TEXT REFERENCES personas(id) ON DELETE CASCADE;

CREATE TRIGGER rules_persona_scope_insert
BEFORE INSERT ON rules
WHEN NEW.persona_id IS NOT NULL
 AND (NEW.sandbox_id IS NOT NULL
      OR NOT EXISTS (SELECT 1 FROM personas WHERE id = NEW.persona_id AND environment_id = NEW.environment_id))
BEGIN
    SELECT RAISE(ABORT, 'rule persona must belong to the same environment, and a rule has one scope');
END;

CREATE TRIGGER rules_persona_scope_update
BEFORE UPDATE OF persona_id, sandbox_id, environment_id ON rules
WHEN NEW.persona_id IS NOT NULL
 AND (NEW.sandbox_id IS NOT NULL
      OR NOT EXISTS (SELECT 1 FROM personas WHERE id = NEW.persona_id AND environment_id = NEW.environment_id))
BEGIN
    SELECT RAISE(ABORT, 'rule persona must belong to the same environment, and a rule has one scope');
END;
