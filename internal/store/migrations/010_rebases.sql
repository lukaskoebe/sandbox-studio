-- Rebase moves a sandbox to another template, so the template pin can change. The
-- owner triggers still require a ready template in the sandbox's environment.
DROP TRIGGER sandboxes_template_pin_update;

-- A rebase operation stays present after the catalog adopts the new generation. It is
-- removed only after recovery or the normal path has removed the obsolete VM. It holds
-- the resources of the target template, which the catalog adopts with the generation.
CREATE TABLE rebases (
    sandbox_id      TEXT PRIMARY KEY REFERENCES sandboxes(id) ON DELETE CASCADE,
    environment_id  TEXT NOT NULL REFERENCES environments(id) ON DELETE CASCADE,
    template_id     TEXT NOT NULL REFERENCES templates(id) ON DELETE RESTRICT,
    from_generation INTEGER NOT NULL,
    to_generation   INTEGER NOT NULL CHECK (to_generation = from_generation + 1),
    cpus            INTEGER NOT NULL,
    memory_mib      INTEGER NOT NULL,
    max_memory_mib  INTEGER NOT NULL,
    workspace_mib   INTEGER NOT NULL,
    docker_mib      INTEGER NOT NULL
);

DROP TRIGGER templates_referenced_state_update;
CREATE TRIGGER templates_referenced_state_update
BEFORE UPDATE OF state ON templates
WHEN NEW.state = 'deleting'
 AND (EXISTS (SELECT 1 FROM sandboxes WHERE template_id = OLD.id)
      OR EXISTS (SELECT 1 FROM rebases WHERE template_id = OLD.id))
BEGIN
    SELECT RAISE(ABORT, 'referenced template cannot be marked deleting');
END;
