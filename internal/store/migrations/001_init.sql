CREATE TABLE environments (
    id         TEXT PRIMARY KEY,
    name       TEXT NOT NULL UNIQUE,
    created_at INTEGER NOT NULL
);

CREATE TABLE sandboxes (
    id             TEXT PRIMARY KEY,
    environment_id TEXT NOT NULL REFERENCES environments(id) ON DELETE CASCADE,
    name           TEXT NOT NULL,
    generation     INTEGER NOT NULL DEFAULT 1,
    cpus           INTEGER NOT NULL,
    memory_mib     INTEGER NOT NULL,
    workspace_mib  INTEGER NOT NULL,
    docker_mib     INTEGER NOT NULL,
    created_at     INTEGER NOT NULL,
    UNIQUE (environment_id, name)
);
