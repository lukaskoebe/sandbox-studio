-- Consolidation, second round (docs/memory.md). A fact can hold in a context only (context):
-- it stays, and surfaces only in searches that name that context. The judge's verdicts are
-- cached with the contexts it named for each fact of the pair.
ALTER TABLE memory_facts ADD COLUMN context TEXT NOT NULL DEFAULT '';

-- The table is rebuilt to accept the new verdict; SQLite cannot change a CHECK in place.
CREATE TABLE memory_judgments_new (
    environment_id TEXT NOT NULL REFERENCES environments(id) ON DELETE CASCADE,
    pair_hash      TEXT NOT NULL,
    model          TEXT NOT NULL,
    prompt_version INTEGER NOT NULL,
    verdict        TEXT NOT NULL CHECK (verdict IN ('no_conflict', 'duplicate', 'supersedes', 'contradiction', 'context_dependent')),
    reason         TEXT NOT NULL DEFAULT '',
    context_a      TEXT NOT NULL DEFAULT '',
    context_b      TEXT NOT NULL DEFAULT '',
    created_at     INTEGER NOT NULL,
    PRIMARY KEY (environment_id, pair_hash, model, prompt_version)
);
INSERT INTO memory_judgments_new (environment_id, pair_hash, model, prompt_version, verdict, reason, created_at)
    SELECT environment_id, pair_hash, model, prompt_version, verdict, reason, created_at FROM memory_judgments;
DROP TABLE memory_judgments;
ALTER TABLE memory_judgments_new RENAME TO memory_judgments;

-- Suggestions the user accepts or dismisses, never applied by themselves: a teammate fact to
-- propose for shared memory (promote), or a fact whose page should become a core page
-- (core). A fact gets at most one suggestion of each kind, so a dismissed one stays dismissed.
CREATE TABLE memory_suggestions (
    id             TEXT PRIMARY KEY,
    environment_id TEXT NOT NULL REFERENCES environments(id) ON DELETE CASCADE,
    fact_id        TEXT NOT NULL REFERENCES memory_facts(id) ON DELETE CASCADE,
    kind           TEXT NOT NULL CHECK (kind IN ('promote', 'core')),
    reason         TEXT NOT NULL DEFAULT '',
    status         TEXT NOT NULL DEFAULT 'open' CHECK (status IN ('open', 'accepted', 'dismissed')),
    created_at     INTEGER NOT NULL,
    decided_at     INTEGER,
    UNIQUE (environment_id, fact_id, kind)
);
CREATE INDEX memory_suggestions_status ON memory_suggestions (environment_id, status, created_at);
