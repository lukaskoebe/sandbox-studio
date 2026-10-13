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
