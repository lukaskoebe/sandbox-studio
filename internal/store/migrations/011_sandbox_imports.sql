-- An import of a sandbox export file. The workspace archive is staged opaquely under the
-- data directory until the import ends. sandbox_id is set in the same transaction that
-- creates the sandbox, so recovery can remove a half-made one.
CREATE TABLE sandbox_imports (
    id             TEXT PRIMARY KEY,
    environment_id TEXT NOT NULL REFERENCES environments(id) ON DELETE CASCADE,
    name           TEXT NOT NULL,
    state          TEXT NOT NULL CHECK (state IN ('uploading', 'building', 'creating', 'importing', 'ready', 'failed')),
    error          TEXT NOT NULL DEFAULT '',
    build_job_id   TEXT NOT NULL DEFAULT '',
    sandbox_id     TEXT REFERENCES sandboxes(id) ON DELETE SET NULL,
    created_at     INTEGER NOT NULL,
    updated_at     INTEGER NOT NULL
);
CREATE INDEX sandbox_imports_active ON sandbox_imports(state) WHERE state NOT IN ('ready', 'failed');
