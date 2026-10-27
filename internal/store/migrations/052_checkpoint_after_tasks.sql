-- A checkpoint schedule can take one after every task instead of every few hours
-- (docs/sandboxes.md#scheduled-checkpoints): after_tasks, with every_hours 0. SQLite cannot
-- change a CHECK in place, so the table is rebuilt as in 050.
CREATE TABLE checkpoint_schedules_new (
    sandbox_id     TEXT PRIMARY KEY REFERENCES sandboxes(id) ON DELETE CASCADE,
    environment_id TEXT NOT NULL REFERENCES environments(id) ON DELETE CASCADE,
    every_hours    INTEGER NOT NULL CHECK (every_hours >= 0),
    after_tasks    INTEGER NOT NULL DEFAULT 0 CHECK (after_tasks IN (0, 1)),
    note           TEXT NOT NULL DEFAULT '',
    CHECK ((every_hours > 0) <> (after_tasks = 1))
);
INSERT INTO checkpoint_schedules_new (sandbox_id, environment_id, every_hours, note)
    SELECT sandbox_id, environment_id, every_hours, note FROM checkpoint_schedules;
DROP TABLE checkpoint_schedules;
ALTER TABLE checkpoint_schedules_new RENAME TO checkpoint_schedules;
