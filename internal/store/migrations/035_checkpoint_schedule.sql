-- Scheduled checkpoints (docs/sandboxes.md#scheduled-checkpoints). A sandbox with a row here
-- gets a checkpoint every every_hours; note says why the last attempt didn't take one.
-- Checkpoints it takes are marked auto, and only those does it delete again.
CREATE TABLE checkpoint_schedules (
    sandbox_id     TEXT PRIMARY KEY REFERENCES sandboxes(id) ON DELETE CASCADE,
    environment_id TEXT NOT NULL REFERENCES environments(id) ON DELETE CASCADE,
    every_hours    INTEGER NOT NULL CHECK (every_hours > 0),
    note           TEXT NOT NULL DEFAULT ''
);
ALTER TABLE checkpoints ADD COLUMN auto INTEGER NOT NULL DEFAULT 0;
