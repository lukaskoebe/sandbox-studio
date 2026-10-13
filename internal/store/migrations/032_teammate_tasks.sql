-- Teammate tasks and heartbeats (docs/teammates.md). A teammate has a task list and an
-- optional check-in schedule: every heartbeat_every hours while the local hour is in
-- [heartbeat_from, heartbeat_to). heartbeat_every is 0 while the schedule is off, which
-- keeps the window for the next time. heartbeat_at is the last check-in of any outcome: the
-- schedule counts from it.
ALTER TABLE teammates ADD COLUMN heartbeat_every INTEGER NOT NULL DEFAULT 0 CHECK (heartbeat_every BETWEEN 0 AND 24);
ALTER TABLE teammates ADD COLUMN heartbeat_from INTEGER NOT NULL DEFAULT 9 CHECK (heartbeat_from BETWEEN 0 AND 23);
ALTER TABLE teammates ADD COLUMN heartbeat_to INTEGER NOT NULL DEFAULT 18 CHECK (heartbeat_to BETWEEN 1 AND 24);
ALTER TABLE teammates ADD COLUMN heartbeat_at INTEGER NOT NULL DEFAULT 0;

-- A chat a heartbeat opened is marked, so the chat list can say so.
ALTER TABLE chats ADD COLUMN heartbeat INTEGER NOT NULL DEFAULT 0 CHECK (heartbeat IN (0, 1));

-- The tables below reference a teammate with its environment, so a row can't name a teammate
-- of another environment.
CREATE UNIQUE INDEX teammates_environment_id ON teammates (environment_id, id);

-- open, in_progress or done. created_by is the user, or the teammate through its task_create
-- tool. due is a day, YYYY-MM-DD, or empty.
CREATE TABLE teammate_tasks (
    id             TEXT PRIMARY KEY,
    environment_id TEXT NOT NULL REFERENCES environments(id) ON DELETE CASCADE,
    teammate_id    TEXT NOT NULL,
    title          TEXT NOT NULL,
    details        TEXT NOT NULL DEFAULT '',   -- markdown, at most 8 KiB
    status         TEXT NOT NULL DEFAULT 'open' CHECK (status IN ('open', 'in_progress', 'done')),
    due            TEXT NOT NULL DEFAULT '',
    created_by     TEXT NOT NULL DEFAULT 'user' CHECK (created_by IN ('user', 'teammate')),
    created_at     INTEGER NOT NULL,
    updated_at     INTEGER NOT NULL,
    FOREIGN KEY (environment_id, teammate_id) REFERENCES teammates (environment_id, id) ON DELETE CASCADE
);
CREATE INDEX teammate_tasks_teammate ON teammate_tasks (teammate_id, status);

-- The check-ins that started a heartbeat or were skipped, with why. A check-in with nothing
-- to do is not logged.
CREATE TABLE teammate_heartbeats (
    id             TEXT PRIMARY KEY,
    environment_id TEXT NOT NULL REFERENCES environments(id) ON DELETE CASCADE,
    teammate_id    TEXT NOT NULL,
    at             INTEGER NOT NULL,
    outcome        TEXT NOT NULL CHECK (outcome IN ('started', 'skipped')),
    reason         TEXT NOT NULL DEFAULT '',
    chat_id        TEXT REFERENCES chats(id) ON DELETE SET NULL,
    FOREIGN KEY (environment_id, teammate_id) REFERENCES teammates (environment_id, id) ON DELETE CASCADE
);
CREATE INDEX teammate_heartbeats_teammate ON teammate_heartbeats (teammate_id, at);
