-- The base image each sandbox runs (docs/sandboxes.md#base-images): the OCI reference its VM
-- was made from and the Studio release that brought it, so a sandbox behind the current base
-- can say so. A template sandbox runs its template's base. Sandboxes made before this column
-- learn their image from msb's catalog at startup; their release stays unknown.
ALTER TABLE sandboxes ADD COLUMN base_image TEXT NOT NULL DEFAULT '';
ALTER TABLE sandboxes ADD COLUMN base_version TEXT NOT NULL DEFAULT '';
UPDATE sandboxes SET base_image = (SELECT base_ref FROM templates WHERE templates.id = sandboxes.template_id)
    WHERE template_id IS NOT NULL;

-- A rebase can go onto the default image, which has no template: template_id becomes nullable,
-- and the operation carries the base it moves to. SQLite cannot drop a NOT NULL in place, so
-- the table is rebuilt as in 033, with the trigger that names it dropped and created again.
DROP TRIGGER templates_referenced_state_update;

CREATE TABLE rebases_new (
    sandbox_id      TEXT PRIMARY KEY REFERENCES sandboxes(id) ON DELETE CASCADE,
    environment_id  TEXT NOT NULL REFERENCES environments(id) ON DELETE CASCADE,
    template_id     TEXT REFERENCES templates(id) ON DELETE RESTRICT,
    from_generation INTEGER NOT NULL,
    to_generation   INTEGER NOT NULL CHECK (to_generation = from_generation + 1),
    cpus            INTEGER NOT NULL,
    memory_mib      INTEGER NOT NULL,
    max_memory_mib  INTEGER NOT NULL,
    workspace_mib   INTEGER NOT NULL,
    docker_mib      INTEGER NOT NULL,
    base_image      TEXT NOT NULL DEFAULT '',
    base_version    TEXT NOT NULL DEFAULT ''
);
INSERT INTO rebases_new (sandbox_id, environment_id, template_id, from_generation, to_generation, cpus, memory_mib,
    max_memory_mib, workspace_mib, docker_mib, base_image)
    SELECT r.sandbox_id, r.environment_id, r.template_id, r.from_generation, r.to_generation, r.cpus, r.memory_mib,
    r.max_memory_mib, r.workspace_mib, r.docker_mib, IFNULL((SELECT base_ref FROM templates WHERE id = r.template_id), '')
    FROM rebases r;
DROP TABLE rebases;
ALTER TABLE rebases_new RENAME TO rebases;

CREATE TRIGGER templates_referenced_state_update
BEFORE UPDATE OF state ON templates
WHEN NEW.state = 'deleting'
 AND (EXISTS (SELECT 1 FROM sandboxes WHERE template_id = OLD.id)
      OR EXISTS (SELECT 1 FROM rebases WHERE template_id = OLD.id))
BEGIN
    SELECT RAISE(ABORT, 'referenced template cannot be marked deleting');
END;
