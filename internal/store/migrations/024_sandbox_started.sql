-- When the sandbox's VM last started: a create, fork, template create, import, transfer,
-- rebase, start or resume that Studio completed. It stays after a stop, so it describes
-- the VM only while the sandbox runs. NULL for sandboxes started before this migration.
ALTER TABLE sandboxes ADD COLUMN started_at INTEGER;
