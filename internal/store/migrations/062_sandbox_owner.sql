-- A computer's teammate can change: a base serves several teammates, and a fork or the user can
-- give a computer another owner, or none (docs/sandboxes.md#teammates). The teammate still has to
-- belong to the computer's environment; Studio itself only changes it while the computer is
-- stopped, since the teammate's rules, secrets and harness files follow the owner.
DROP TRIGGER sandboxes_teammate_owner_update;

CREATE TRIGGER sandboxes_teammate_owner_update
BEFORE UPDATE OF teammate_id, environment_id ON sandboxes
WHEN NEW.teammate_id IS NOT NULL
 AND (NEW.teammate_id IS NOT OLD.teammate_id OR NEW.environment_id IS NOT OLD.environment_id)
 AND NOT EXISTS (SELECT 1 FROM teammates WHERE id = NEW.teammate_id AND environment_id = NEW.environment_id)
BEGIN
    SELECT RAISE(ABORT, 'sandbox teammate must belong to the same environment');
END;
