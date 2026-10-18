-- Scheduled tasks (docs/design/scheduled-tasks.md). A task may have a schedule, JSON or NULL,
-- and a sandbox to run in, NULL for the teammate's latest. next_run_at is the next time a run
-- is due: NULL while the schedule is off, paused or has no time left.
ALTER TABLE teammate_tasks ADD COLUMN schedule TEXT;
ALTER TABLE teammate_tasks ADD COLUMN sandbox_id TEXT REFERENCES sandboxes(id) ON DELETE SET NULL;
ALTER TABLE teammate_tasks ADD COLUMN next_run_at INTEGER;
CREATE INDEX teammate_tasks_next_run ON teammate_tasks (next_run_at) WHERE next_run_at IS NOT NULL;

-- The runs of scheduled tasks: started (and done once the teammate marks it so), skipped with
-- why, or missed while Studio was not running. due_at is the scheduled time, 0 for Run now.
CREATE TABLE task_runs (
    id             TEXT PRIMARY KEY,
    environment_id TEXT NOT NULL REFERENCES environments(id) ON DELETE CASCADE,
    teammate_id    TEXT NOT NULL,
    task_id        TEXT NOT NULL REFERENCES teammate_tasks(id) ON DELETE CASCADE,
    due_at         INTEGER NOT NULL DEFAULT 0,
    at             INTEGER NOT NULL,
    outcome        TEXT NOT NULL CHECK (outcome IN ('started', 'done', 'skipped', 'missed')),
    reason         TEXT NOT NULL DEFAULT '',
    chat_id        TEXT REFERENCES chats(id) ON DELETE SET NULL,
    FOREIGN KEY (environment_id, teammate_id) REFERENCES teammates (environment_id, id) ON DELETE CASCADE
);
CREATE INDEX task_runs_task ON task_runs (task_id, at);
CREATE INDEX task_runs_teammate ON task_runs (teammate_id, at);

-- A chat a scheduled run opened names its task, as heartbeat marks a check-in's chat.
ALTER TABLE chats ADD COLUMN task_id TEXT NOT NULL DEFAULT '';
