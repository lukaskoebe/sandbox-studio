-- Consolidation ("dream", PLAN §6.7). A conflict remembers the judge's reason and the
-- memory.conflict approval it opened; a fact pair has at most one conflict.
ALTER TABLE memory_conflicts ADD COLUMN reason TEXT NOT NULL DEFAULT '';
ALTER TABLE memory_conflicts ADD COLUMN approval_id TEXT;
CREATE UNIQUE INDEX memory_conflicts_pair ON memory_conflicts (environment_id, fact_a, fact_b);

-- One row per dream of a scope. A run covers the facts created from window_start to
-- window_end (both included); only a finished ('done') run moves the next run's window. A
-- 'running' row left by an interrupted run is resumed with the same window.
CREATE TABLE memory_dream_runs (
    id             TEXT PRIMARY KEY,
    environment_id TEXT NOT NULL REFERENCES environments(id) ON DELETE CASCADE,
    scope          TEXT NOT NULL,
    trigger        TEXT NOT NULL CHECK (trigger IN ('nightly', 'facts', 'manual')),
    status         TEXT NOT NULL CHECK (status IN ('running', 'done', 'stopped', 'failed')),
    window_start   INTEGER NOT NULL,
    window_end     INTEGER NOT NULL,
    started_at     INTEGER NOT NULL,
    finished_at    INTEGER,
    resumes        INTEGER NOT NULL DEFAULT 0,
    stats          TEXT NOT NULL DEFAULT '{}',   -- JSON
    note           TEXT NOT NULL DEFAULT ''
);
CREATE UNIQUE INDEX memory_dream_running ON memory_dream_runs (environment_id, scope) WHERE status = 'running';
CREATE INDEX memory_dream_runs_scope ON memory_dream_runs (environment_id, scope, started_at);

-- The judge's verdicts, cached by the pair's content, model and prompt version.
CREATE TABLE memory_judgments (
    environment_id TEXT NOT NULL REFERENCES environments(id) ON DELETE CASCADE,
    pair_hash      TEXT NOT NULL,
    model          TEXT NOT NULL,
    prompt_version INTEGER NOT NULL,
    verdict        TEXT NOT NULL CHECK (verdict IN ('no_conflict', 'duplicate', 'supersedes', 'contradiction')),
    reason         TEXT NOT NULL DEFAULT '',
    created_at     INTEGER NOT NULL,
    PRIMARY KEY (environment_id, pair_hash, model, prompt_version)
);
