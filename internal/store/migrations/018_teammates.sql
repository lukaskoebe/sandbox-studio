-- Personas are now teammates. Renames tables, columns, indexes and triggers, rewrites the
-- memory scopes 'persona:<id>' to 'teammate:<id>' and the persona keys of stored JSON.
-- Migrations run with foreign keys off (Store.migrate), so memory_pages can be rebuilt for
-- its kind CHECK without cascading to its timeline and chunks.

ALTER TABLE personas RENAME TO teammates;
DROP INDEX personas_provider;
CREATE INDEX teammates_provider ON teammates (provider_id);

-- Sandboxes.
DROP TRIGGER sandboxes_persona_owner_insert;
DROP TRIGGER sandboxes_persona_owner_update;
DROP INDEX sandboxes_persona;
ALTER TABLE sandboxes RENAME COLUMN persona_id TO teammate_id;
CREATE INDEX sandboxes_teammate ON sandboxes (teammate_id) WHERE teammate_id IS NOT NULL;

CREATE TRIGGER sandboxes_teammate_owner_insert
BEFORE INSERT ON sandboxes
WHEN NEW.teammate_id IS NOT NULL
 AND NOT EXISTS (SELECT 1 FROM teammates WHERE id = NEW.teammate_id AND environment_id = NEW.environment_id)
BEGIN
    SELECT RAISE(ABORT, 'sandbox teammate must belong to the same environment');
END;

-- Ownership is fixed at creation.
CREATE TRIGGER sandboxes_teammate_owner_update
BEFORE UPDATE OF teammate_id, environment_id ON sandboxes
WHEN NEW.teammate_id IS NOT OLD.teammate_id
 OR (NEW.teammate_id IS NOT NULL AND NEW.environment_id IS NOT OLD.environment_id)
BEGIN
    SELECT RAISE(ABORT, 'sandbox teammate is immutable');
END;

-- Rules.
DROP TRIGGER rules_persona_scope_insert;
DROP TRIGGER rules_persona_scope_update;
ALTER TABLE rules RENAME COLUMN persona_id TO teammate_id;

CREATE TRIGGER rules_teammate_scope_insert
BEFORE INSERT ON rules
WHEN NEW.teammate_id IS NOT NULL
 AND (NEW.sandbox_id IS NOT NULL
      OR NOT EXISTS (SELECT 1 FROM teammates WHERE id = NEW.teammate_id AND environment_id = NEW.environment_id))
BEGIN
    SELECT RAISE(ABORT, 'rule teammate must belong to the same environment, and a rule has one scope');
END;

CREATE TRIGGER rules_teammate_scope_update
BEFORE UPDATE OF teammate_id, sandbox_id, environment_id ON rules
WHEN NEW.teammate_id IS NOT NULL
 AND (NEW.sandbox_id IS NOT NULL
      OR NOT EXISTS (SELECT 1 FROM teammates WHERE id = NEW.teammate_id AND environment_id = NEW.environment_id))
BEGIN
    SELECT RAISE(ABORT, 'rule teammate must belong to the same environment, and a rule has one scope');
END;

-- Memory. memory_pages is rebuilt: its kind CHECK names 'persona-self'.
ALTER TABLE memory_sources RENAME COLUMN author_persona TO author_teammate;
ALTER TABLE memory_facts RENAME COLUMN author_persona TO author_teammate;
ALTER TABLE memory_timeline RENAME COLUMN author_persona TO author_teammate;

CREATE TABLE memory_pages_new (
    id              TEXT PRIMARY KEY,
    environment_id  TEXT NOT NULL REFERENCES environments(id) ON DELETE CASCADE,
    scope           TEXT NOT NULL,
    slug            TEXT NOT NULL,
    title           TEXT NOT NULL,
    kind            TEXT NOT NULL CHECK (kind IN ('person', 'project', 'topic', 'procedure', 'teammate-self')),
    compiled        TEXT NOT NULL DEFAULT '',
    always_load     INTEGER NOT NULL DEFAULT 0,
    tier            TEXT NOT NULL CHECK (tier IN ('user', 'verified', 'document', 'inferred')),
    author_teammate TEXT NOT NULL DEFAULT '',
    created_at      INTEGER NOT NULL,
    updated_at      INTEGER NOT NULL,
    UNIQUE (environment_id, scope, slug)
);
INSERT INTO memory_pages_new (id, environment_id, scope, slug, title, kind, compiled, always_load, tier, author_teammate, created_at, updated_at)
SELECT id, environment_id, scope, slug, title, CASE kind WHEN 'persona-self' THEN 'teammate-self' ELSE kind END,
       compiled, always_load, tier, author_persona, created_at, updated_at
FROM memory_pages;
DROP TABLE memory_pages;
ALTER TABLE memory_pages_new RENAME TO memory_pages;

UPDATE memory_sources SET scope = 'teammate:' || substr(scope, 9) WHERE scope LIKE 'persona:%';
UPDATE memory_facts SET scope = 'teammate:' || substr(scope, 9) WHERE scope LIKE 'persona:%';
UPDATE memory_pages SET scope = 'teammate:' || substr(scope, 9) WHERE scope LIKE 'persona:%';
UPDATE memory_chunks SET scope = 'teammate:' || substr(scope, 9) WHERE scope LIKE 'persona:%';
UPDATE memory_dream_runs SET scope = 'teammate:' || substr(scope, 9) WHERE scope LIKE 'persona:%';
UPDATE memory_dream_runs SET stats = json_remove(json_set(stats, '$.teammate', json_extract(stats, '$.persona')), '$.persona')
WHERE json_valid(stats) AND json_type(stats, '$.persona') IS NOT NULL;

DROP INDEX memory_agent_sessions_persona;
ALTER TABLE memory_agent_sessions RENAME COLUMN persona_id TO teammate_id;
CREATE INDEX memory_agent_sessions_teammate ON memory_agent_sessions (environment_id, teammate_id, started_at);
ALTER TABLE memory_session_log RENAME COLUMN persona_id TO teammate_id;

-- Git pushes and the browser.
ALTER TABLE git_pushes RENAME COLUMN persona_name TO teammate_name;

DROP INDEX browser_patterns_persona;
ALTER TABLE browser_patterns RENAME COLUMN persona_id TO teammate_id;
CREATE INDEX browser_patterns_teammate ON browser_patterns (environment_id, teammate_id);
DROP INDEX browser_actions_persona;
ALTER TABLE browser_actions RENAME COLUMN persona_id TO teammate_id;
CREATE INDEX browser_actions_teammate ON browser_actions (environment_id, teammate_id, id);

-- Approval payloads: personaId/personaName of browser.action, browser.credential and
-- memory.share, persona of git.push, and the fact scopes of memory.conflict.
UPDATE approvals SET payload = json_remove(json_set(payload,
        '$.teammateId', json_extract(payload, '$.personaId'),
        '$.teammateName', json_extract(payload, '$.personaName')),
    '$.personaId', '$.personaName')
WHERE kind IN ('browser.action', 'browser.credential', 'memory.share') AND json_valid(payload)
  AND json_type(payload, '$.personaId') IS NOT NULL;
UPDATE approvals SET payload = json_remove(json_set(payload, '$.teammate', json_extract(payload, '$.persona')), '$.persona')
WHERE kind = 'git.push' AND json_valid(payload) AND json_type(payload, '$.persona') IS NOT NULL;
UPDATE approvals SET payload = json_set(payload, '$.factA.scope', 'teammate:' || substr(json_extract(payload, '$.factA.scope'), 9))
WHERE kind = 'memory.conflict' AND json_valid(payload) AND json_extract(payload, '$.factA.scope') LIKE 'persona:%';
UPDATE approvals SET payload = json_set(payload, '$.factB.scope', 'teammate:' || substr(json_extract(payload, '$.factB.scope'), 9))
WHERE kind = 'memory.conflict' AND json_valid(payload) AND json_extract(payload, '$.factB.scope') LIKE 'persona:%';
