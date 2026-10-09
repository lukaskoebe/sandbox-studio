CREATE TABLE checkpoints (
    id             TEXT PRIMARY KEY,
    environment_id TEXT NOT NULL REFERENCES environments(id) ON DELETE CASCADE,
    sandbox_id     TEXT NOT NULL REFERENCES sandboxes(id) ON DELETE CASCADE,
    name           TEXT NOT NULL,
    state          TEXT NOT NULL CHECK (state IN ('creating', 'ready', 'deleting')),
    generation     INTEGER NOT NULL CHECK (generation > 0),
    created_at     INTEGER NOT NULL,
    UNIQUE (environment_id, sandbox_id, name)
);
CREATE INDEX checkpoints_sandbox ON checkpoints (environment_id, sandbox_id, created_at, id);

-- A restore operation stays present after the catalog adopts the new generation. It is
-- removed only after startup recovery or the normal path has removed the obsolete VM.
CREATE TABLE restores (
    sandbox_id     TEXT PRIMARY KEY REFERENCES sandboxes(id) ON DELETE CASCADE,
    environment_id TEXT NOT NULL REFERENCES environments(id) ON DELETE CASCADE,
    checkpoint_id  TEXT NOT NULL,
    from_generation INTEGER NOT NULL,
    to_generation   INTEGER NOT NULL CHECK (to_generation = from_generation + 1)
);
CREATE INDEX restores_environment ON restores (environment_id, sandbox_id);
