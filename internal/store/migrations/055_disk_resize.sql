-- A disk resize (docs/rebase.md#disk-resize) is a rebase onto the sandbox's own template
-- or base with other disk sizes, which copies Docker's data too. Its record says which it
-- is, and its history event is 'disks'. The kind check can only change by rebuilding the
-- table.
ALTER TABLE rebases ADD COLUMN kind TEXT NOT NULL DEFAULT 'rebase' CHECK (kind IN ('rebase', 'disks'));

CREATE TABLE sandbox_events_new (
    id               INTEGER PRIMARY KEY AUTOINCREMENT,
    environment_id   TEXT NOT NULL REFERENCES environments(id) ON DELETE CASCADE,
    sandbox_id       TEXT NOT NULL,
    kind             TEXT NOT NULL CHECK (kind IN ('create', 'fork', 'rebase', 'restore', 'checkpoint', 'delete', 'merge', 'disks')),
    generation       INTEGER NOT NULL,
    name             TEXT NOT NULL,
    template_id      TEXT NOT NULL DEFAULT '',
    from_template_id TEXT NOT NULL DEFAULT '',
    checkpoint_id    TEXT NOT NULL DEFAULT '',
    checkpoint_name  TEXT NOT NULL DEFAULT '',
    -- fork: on the source's line the child, on the child's line the source; delete: the
    -- source of a fork; merge: the fork merged
    other_sandbox_id TEXT NOT NULL DEFAULT '',
    created_at       INTEGER NOT NULL
);
INSERT INTO sandbox_events_new SELECT id, environment_id, sandbox_id, kind, generation, name, template_id, from_template_id,
    checkpoint_id, checkpoint_name, other_sandbox_id, created_at FROM sandbox_events;
DROP TABLE sandbox_events;
ALTER TABLE sandbox_events_new RENAME TO sandbox_events;
CREATE INDEX sandbox_events_sandbox ON sandbox_events (environment_id, sandbox_id, created_at, id);
CREATE INDEX sandbox_events_kind ON sandbox_events (environment_id, kind);
