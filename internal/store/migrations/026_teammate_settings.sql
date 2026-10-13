-- Teammate settings: the avatar shown instead of the initials, the scope that the approval
-- cards for the teammate's sandboxes preselect for new rules, and the host pattern level the
-- teammate last approved or denied with (see policy.PatternLevel).
ALTER TABLE teammates ADD COLUMN avatar TEXT NOT NULL DEFAULT '';
ALTER TABLE teammates ADD COLUMN approval_scope TEXT NOT NULL DEFAULT 'environment'
    CHECK (approval_scope IN ('sandbox', 'teammate', 'environment'));
ALTER TABLE teammates ADD COLUMN approval_pattern TEXT NOT NULL DEFAULT ''
    CHECK (approval_pattern IN ('', 'exact', 'wildcard'));

-- A teammate copied to an environment without a provider of its kind has none yet, so
-- provider_id may be NULL. The table is rebuilt rather than altered, and copied out and back
-- rather than renamed: the triggers of sandboxes, rules and secrets name teammates.
CREATE TABLE teammates_old AS SELECT * FROM teammates;
DROP TABLE teammates;
CREATE TABLE teammates (
    id               TEXT PRIMARY KEY,
    environment_id   TEXT NOT NULL REFERENCES environments(id) ON DELETE CASCADE,
    name             TEXT NOT NULL,
    role             TEXT NOT NULL DEFAULT '',
    soul             TEXT NOT NULL DEFAULT '',
    harness          TEXT NOT NULL CHECK (harness IN ('opencode', 'claude', 'codex')),
    provider_id      TEXT,
    model            TEXT NOT NULL DEFAULT '',
    git_name         TEXT NOT NULL,
    git_email        TEXT NOT NULL,
    created_at       INTEGER NOT NULL,
    updated_at       INTEGER NOT NULL,
    avatar           TEXT NOT NULL DEFAULT '',
    approval_scope   TEXT NOT NULL DEFAULT 'environment' CHECK (approval_scope IN ('sandbox', 'teammate', 'environment')),
    approval_pattern TEXT NOT NULL DEFAULT '' CHECK (approval_pattern IN ('', 'exact', 'wildcard')),
    UNIQUE (environment_id, name),
    FOREIGN KEY (environment_id, provider_id) REFERENCES providers(environment_id, id)
);
INSERT INTO teammates (id, environment_id, name, role, soul, harness, provider_id, model, git_name, git_email,
                       created_at, updated_at, avatar, approval_scope, approval_pattern)
SELECT id, environment_id, name, role, soul, harness, provider_id, model, git_name, git_email,
       created_at, updated_at, avatar, approval_scope, approval_pattern
FROM teammates_old;
DROP TABLE teammates_old;
CREATE INDEX teammates_provider ON teammates (provider_id);

-- An API-key provider may exist without its key, until one is added: importing an environment
-- creates providers from the file, which has no keys. Subscription providers never have one.
-- Rebuilt as above; the triggers that check its key's scope are created again.
CREATE TABLE providers_old AS SELECT * FROM providers;
DROP TABLE providers;
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
    CHECK (secret_id IS NULL OR kind NOT IN ('claude_subscription', 'chatgpt_subscription')),
    CHECK ((kind = 'openai_compatible') = (base_url <> '' AND model <> ''))
);
INSERT INTO providers (id, environment_id, name, kind, base_url, model, secret_id, created_at, updated_at)
SELECT id, environment_id, name, kind, base_url, model, secret_id, created_at, updated_at FROM providers_old;
DROP TABLE providers_old;

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
