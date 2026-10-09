-- Instances keep the template they were created from pinned for their lifetime.
ALTER TABLE sandboxes
    ADD COLUMN template_id TEXT REFERENCES templates(id) ON DELETE RESTRICT;

CREATE INDEX sandboxes_template_id ON sandboxes (template_id) WHERE template_id IS NOT NULL;

-- A sandbox may attach only to a ready template in its own environment.
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

-- Template lineage is immutable once attached; this slice has no unbind or rebind operation.
CREATE TRIGGER sandboxes_template_pin_update
BEFORE UPDATE OF template_id ON sandboxes
WHEN OLD.template_id IS NOT NULL AND NEW.template_id IS NOT OLD.template_id
BEGIN
    SELECT RAISE(ABORT, 'sandbox template reference is immutable');
END;

-- Store checks provide ErrConflict; this trigger also protects direct SQL and races.
CREATE TRIGGER templates_referenced_state_update
BEFORE UPDATE OF state ON templates
WHEN NEW.state = 'deleting'
 AND EXISTS (SELECT 1 FROM sandboxes WHERE template_id = OLD.id)
BEGIN
    SELECT RAISE(ABORT, 'referenced template cannot be marked deleting');
END;
