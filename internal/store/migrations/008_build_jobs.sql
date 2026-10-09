-- Build jobs own builder sandboxes independently from their status. A cancelled
-- job can keep ownership until the worker proves its sandbox is gone.
CREATE UNIQUE INDEX sandboxes_environment_id_id ON sandboxes (environment_id, id);

CREATE TABLE build_jobs (
    id                TEXT PRIMARY KEY,
    environment_id    TEXT NOT NULL REFERENCES environments(id) ON DELETE RESTRICT,
    request_key       TEXT NOT NULL CHECK (length(CAST(request_key AS BLOB)) BETWEEN 1 AND 255),
    source            TEXT NOT NULL CHECK (length(CAST(source AS BLOB)) BETWEEN 1 AND 65536),
    spec              TEXT NOT NULL CHECK (length(CAST(spec AS BLOB)) BETWEEN 1 AND 524288),
    base_ref          TEXT NOT NULL,
    target_platform   TEXT NOT NULL,
    exporter_version  TEXT NOT NULL,
    cache_key         TEXT NOT NULL DEFAULT '',
    base_digest       TEXT NOT NULL DEFAULT '',
    platform          TEXT NOT NULL DEFAULT '',
    template_id       TEXT REFERENCES templates(id) ON DELETE SET NULL,
    status            TEXT NOT NULL CHECK (status IN ('queued', 'preparing', 'setting_up', 'exporting', 'ready', 'failed', 'cancelled')),
    error             TEXT NOT NULL DEFAULT '' CHECK (length(CAST(error AS BLOB)) <= 1000),
    cleanup_error     TEXT NOT NULL DEFAULT '' CHECK (length(CAST(cleanup_error AS BLOB)) <= 1000),
    cleanup_pending   INTEGER NOT NULL DEFAULT 0 CHECK (cleanup_pending IN (0, 1)),
    prewarm_name      TEXT NOT NULL DEFAULT '',
    prewarm_token     TEXT NOT NULL DEFAULT '',
    sandbox_id        TEXT,
    created_at        INTEGER NOT NULL,
    updated_at        INTEGER NOT NULL,
    FOREIGN KEY (environment_id, sandbox_id) REFERENCES sandboxes(environment_id, id)
);

CREATE UNIQUE INDEX build_jobs_environment_id_id ON build_jobs (environment_id, id);
CREATE UNIQUE INDEX build_jobs_sandbox ON build_jobs (sandbox_id) WHERE sandbox_id IS NOT NULL;
CREATE UNIQUE INDEX build_jobs_active_request_key ON build_jobs (environment_id, request_key)
    WHERE status IN ('queued', 'preparing', 'setting_up', 'exporting');
CREATE INDEX build_jobs_environment_created ON build_jobs (environment_id, created_at DESC, id);
CREATE INDEX build_jobs_recovery ON build_jobs (status, cleanup_pending, created_at, id);
CREATE UNIQUE INDEX build_jobs_active_cache_key ON build_jobs (environment_id, cache_key)
    WHERE cache_key <> '' AND status IN ('queued', 'preparing', 'setting_up', 'exporting');

CREATE TABLE build_job_logs (
    environment_id TEXT NOT NULL,
    job_id         TEXT NOT NULL,
    content        TEXT NOT NULL DEFAULT '' CHECK (length(CAST(content AS BLOB)) <= 1048576),
    truncated      INTEGER NOT NULL DEFAULT 0 CHECK (truncated IN (0, 1)),
    PRIMARY KEY (environment_id, job_id),
    FOREIGN KEY (environment_id, job_id) REFERENCES build_jobs(environment_id, id) ON DELETE CASCADE
);

ALTER TABLE sandboxes ADD COLUMN build_job_id TEXT REFERENCES build_jobs(id);
CREATE UNIQUE INDEX sandboxes_build_job ON sandboxes (build_job_id) WHERE build_job_id IS NOT NULL;

-- Keep optional template links in the same environment and cache namespace.
-- The template ID FK uses ON DELETE SET NULL so old jobs never retain dangling
-- references after a cache entry is removed.
CREATE TRIGGER build_jobs_template_owner_insert
BEFORE INSERT ON build_jobs
WHEN NEW.template_id IS NOT NULL
 AND NOT EXISTS (SELECT 1 FROM templates WHERE id = NEW.template_id AND environment_id = NEW.environment_id AND cache_key = NEW.cache_key)
BEGIN
    SELECT RAISE(ABORT, 'build job template must match environment and cache key');
END;

CREATE TRIGGER build_jobs_template_owner_update
BEFORE UPDATE OF template_id, environment_id, cache_key ON build_jobs
WHEN NEW.template_id IS NOT NULL
 AND NOT EXISTS (SELECT 1 FROM templates WHERE id = NEW.template_id AND environment_id = NEW.environment_id AND cache_key = NEW.cache_key)
BEGIN
    SELECT RAISE(ABORT, 'build job template must match environment and cache key');
END;

CREATE TRIGGER sandboxes_build_job_owner_insert
BEFORE INSERT ON sandboxes
WHEN NEW.build_job_id IS NOT NULL
 AND NOT EXISTS (SELECT 1 FROM build_jobs WHERE id = NEW.build_job_id AND environment_id = NEW.environment_id)
BEGIN
    SELECT RAISE(ABORT, 'sandbox build job must belong to the same environment');
END;

CREATE TRIGGER sandboxes_build_job_owner_update
BEFORE UPDATE OF build_job_id, environment_id ON sandboxes
WHEN NEW.build_job_id IS NOT NULL
 AND NOT EXISTS (SELECT 1 FROM build_jobs WHERE id = NEW.build_job_id AND environment_id = NEW.environment_id)
BEGIN
    SELECT RAISE(ABORT, 'sandbox build job must belong to the same environment');
END;
