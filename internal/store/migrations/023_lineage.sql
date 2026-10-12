-- Lineage: where a sandbox came from, and what happened to it, in order.
--
-- A fork refers to its source by ID, generation and time. There is no foreign key: the
-- source may be deleted, and its delete event keeps its name. Forks made before this
-- migration have no reference, because their source wasn't recorded.
ALTER TABLE sandboxes ADD COLUMN forked_from_sandbox_id TEXT;
ALTER TABLE sandboxes ADD COLUMN forked_from_generation INTEGER;
ALTER TABLE sandboxes ADD COLUMN forked_at INTEGER;

-- One row per successful step. sandbox_id has no foreign key, so a deleted sandbox keeps its
-- history: its delete event is the tombstone, with the name, template and fork source it had.
-- Failed operations write nothing.
CREATE TABLE sandbox_events (
    id               INTEGER PRIMARY KEY AUTOINCREMENT,
    environment_id   TEXT NOT NULL REFERENCES environments(id) ON DELETE CASCADE,
    sandbox_id       TEXT NOT NULL,
    kind             TEXT NOT NULL CHECK (kind IN ('create', 'fork', 'rebase', 'restore', 'checkpoint', 'delete')),
    generation       INTEGER NOT NULL,
    name             TEXT NOT NULL,            -- the sandbox's name at the time
    template_id      TEXT NOT NULL DEFAULT '', -- the template of the generation; empty for none
    from_template_id TEXT NOT NULL DEFAULT '', -- rebase: the template it moved from
    checkpoint_id    TEXT NOT NULL DEFAULT '', -- restore and checkpoint
    checkpoint_name  TEXT NOT NULL DEFAULT '',
    -- fork: on the source's line the child, on the child's line the source; delete: the
    -- source of a fork
    other_sandbox_id TEXT NOT NULL DEFAULT '',
    created_at       INTEGER NOT NULL
);
CREATE INDEX sandbox_events_sandbox ON sandbox_events (environment_id, sandbox_id, created_at, id);
CREATE INDEX sandbox_events_kind ON sandbox_events (environment_id, kind);

-- The history that already exists is the best guess there is: each sandbox made by a user
-- gets a create event at its creation time, and each ready checkpoint a checkpoint event.
-- Build and browser VMs are not part of the history. Only generation 1 is known to have run
-- the current template.
INSERT INTO sandbox_events (environment_id, sandbox_id, kind, generation, name, template_id, created_at)
SELECT environment_id, id, 'create', 1, name,
       CASE WHEN generation = 1 THEN IFNULL(template_id, '') ELSE '' END, created_at
FROM sandboxes
WHERE build_job_id IS NULL AND kind = '';

INSERT INTO sandbox_events (environment_id, sandbox_id, kind, generation, name, checkpoint_id, checkpoint_name, created_at)
SELECT c.environment_id, c.sandbox_id, 'checkpoint', c.generation, s.name, c.id, c.name, c.created_at
FROM checkpoints c
JOIN sandboxes s ON s.environment_id = c.environment_id AND s.id = c.sandbox_id
WHERE c.state = 'ready';
