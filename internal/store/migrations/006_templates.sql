-- OCI image templates are durable registry cache entries. Runtime artifacts are
-- represented by digests and media types only; no filesystem paths are stored.
CREATE TABLE templates (
    id               TEXT PRIMARY KEY,
    environment_id   TEXT NOT NULL REFERENCES environments(id) ON DELETE CASCADE,
    cache_key        TEXT NOT NULL,
    spec             TEXT NOT NULL,
    base_ref         TEXT NOT NULL,
    base_digest      TEXT NOT NULL,
    platform         TEXT NOT NULL,
    exporter_version TEXT NOT NULL,
    state            TEXT NOT NULL CHECK (state IN ('creating', 'ready', 'deleting')),
    created_at       INTEGER NOT NULL,
    UNIQUE (environment_id, cache_key)
);
CREATE INDEX templates_recovery ON templates (state, created_at, id);
CREATE INDEX templates_environment ON templates (environment_id, created_at, id);

CREATE TABLE template_artifacts (
    template_id TEXT NOT NULL REFERENCES templates(id) ON DELETE CASCADE,
    role        TEXT NOT NULL CHECK (role IN ('manifest', 'config', 'layer')),
    digest      TEXT NOT NULL,
    media_type  TEXT NOT NULL,
    size        INTEGER NOT NULL CHECK (size > 0),
    PRIMARY KEY (template_id, role)
);
