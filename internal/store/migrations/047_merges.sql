-- Merging a fork back into the sandbox it was forked from (docs/design/fork-merge.md). The
-- merge is done in a scratch sandbox, a fresh fork of the source; accepting it replays the
-- scratch sandbox's changes into the source. The sandbox IDs have no foreign keys: a row
-- outlives the scratch sandbox it deletes, and the fork may be deleted.
--
-- state: preparing (the scratch sandbox is being made and the fork's changes applied), ready
-- (merged without conflicts), conflicts (some remain), failed (error says why), accepted or
-- aborted. report is forkmerge.Report as JSON. checkpoint_id is the source's checkpoint taken
-- before Accept replays into it.
CREATE TABLE merges (
    id             TEXT PRIMARY KEY,
    environment_id TEXT NOT NULL REFERENCES environments(id) ON DELETE CASCADE,
    source_id      TEXT NOT NULL,
    fork_id        TEXT NOT NULL,
    scratch_id     TEXT NOT NULL DEFAULT '',
    state          TEXT NOT NULL CHECK (state IN ('preparing', 'ready', 'conflicts', 'failed', 'accepted', 'aborted')),
    error          TEXT NOT NULL DEFAULT '',
    report         TEXT NOT NULL DEFAULT '',
    checkpoint_id  TEXT NOT NULL DEFAULT '',
    created_at     INTEGER NOT NULL,
    updated_at     INTEGER NOT NULL
);
CREATE INDEX merges_sandbox ON merges (environment_id, source_id, created_at);
-- One open merge into a sandbox at a time.
CREATE UNIQUE INDEX merges_open ON merges (environment_id, source_id) WHERE state IN ('preparing', 'ready', 'conflicts');

-- A merge event on the source's line names the fork. The kind check can only change by
-- rebuilding the table.
CREATE TABLE sandbox_events_new (
    id               INTEGER PRIMARY KEY AUTOINCREMENT,
    environment_id   TEXT NOT NULL REFERENCES environments(id) ON DELETE CASCADE,
    sandbox_id       TEXT NOT NULL,
    kind             TEXT NOT NULL CHECK (kind IN ('create', 'fork', 'rebase', 'restore', 'checkpoint', 'delete', 'merge')),
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
