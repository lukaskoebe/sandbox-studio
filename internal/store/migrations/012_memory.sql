-- Memory (PLAN §6.7). Every row belongs to one environment and one scope: 'shared' or
-- 'persona:<id>'. Persona IDs are opaque here; they get a foreign key once personas land.
-- author_persona is '' when the author is the user or unknown.

-- Where information came from. Full transcripts are never stored, only a short evidence quote.
CREATE TABLE memory_sources (
    id             TEXT PRIMARY KEY,
    environment_id TEXT NOT NULL REFERENCES environments(id) ON DELETE CASCADE,
    scope          TEXT NOT NULL,
    kind           TEXT NOT NULL CHECK (kind IN ('user', 'verified', 'document', 'inferred', 'consolidation')),
    session_ref    TEXT NOT NULL DEFAULT '',
    author_persona TEXT NOT NULL DEFAULT '',
    evidence       TEXT NOT NULL DEFAULT '',
    created_at     INTEGER NOT NULL
);
CREATE INDEX memory_sources_scope ON memory_sources (environment_id, scope);

-- Atomic claims. A fact whose valid_until has passed counts as superseded at recall.
CREATE TABLE memory_facts (
    id             TEXT PRIMARY KEY,
    environment_id TEXT NOT NULL REFERENCES environments(id) ON DELETE CASCADE,
    scope          TEXT NOT NULL,
    kind           TEXT NOT NULL CHECK (kind IN ('preference', 'decision', 'fact', 'procedure', 'event')),
    entity_ids     TEXT NOT NULL DEFAULT '[]',   -- JSON array of strings
    attribute      TEXT NOT NULL DEFAULT '',     -- normalized key, e.g. deploy.command
    text           TEXT NOT NULL,
    observed_at    INTEGER NOT NULL,
    valid_from     INTEGER,
    valid_until    INTEGER,
    confidence     REAL NOT NULL DEFAULT 1 CHECK (confidence >= 0 AND confidence <= 1),
    tier           TEXT NOT NULL CHECK (tier IN ('user', 'verified', 'document', 'inferred')),
    support_count  INTEGER NOT NULL DEFAULT 1,
    status         TEXT NOT NULL DEFAULT 'active' CHECK (status IN ('active', 'superseded', 'disputed', 'retracted')),
    supersedes     TEXT REFERENCES memory_facts(id) ON DELETE SET NULL,
    author_persona TEXT NOT NULL DEFAULT '',
    created_at     INTEGER NOT NULL,
    updated_at     INTEGER NOT NULL
);
CREATE INDEX memory_facts_scope ON memory_facts (environment_id, scope, status);

CREATE TABLE memory_fact_sources (
    fact_id   TEXT NOT NULL REFERENCES memory_facts(id) ON DELETE CASCADE,
    source_id TEXT NOT NULL REFERENCES memory_sources(id) ON DELETE CASCADE,
    PRIMARY KEY (fact_id, source_id)
);

-- Entity and topic pages: compiled truth above the line, an append-only timeline below.
CREATE TABLE memory_pages (
    id             TEXT PRIMARY KEY,
    environment_id TEXT NOT NULL REFERENCES environments(id) ON DELETE CASCADE,
    scope          TEXT NOT NULL,
    slug           TEXT NOT NULL,
    title          TEXT NOT NULL,
    kind           TEXT NOT NULL CHECK (kind IN ('person', 'project', 'topic', 'procedure', 'persona-self')),
    compiled       TEXT NOT NULL DEFAULT '',
    always_load    INTEGER NOT NULL DEFAULT 0,
    tier           TEXT NOT NULL CHECK (tier IN ('user', 'verified', 'document', 'inferred')),
    author_persona TEXT NOT NULL DEFAULT '',
    created_at     INTEGER NOT NULL,
    updated_at     INTEGER NOT NULL,
    UNIQUE (environment_id, scope, slug)
);

CREATE TABLE memory_timeline (
    id             TEXT PRIMARY KEY,
    environment_id TEXT NOT NULL REFERENCES environments(id) ON DELETE CASCADE,
    page_id        TEXT NOT NULL REFERENCES memory_pages(id) ON DELETE CASCADE,
    at             INTEGER NOT NULL,
    text           TEXT NOT NULL,
    fact_id        TEXT REFERENCES memory_facts(id) ON DELETE SET NULL,
    source_id      TEXT REFERENCES memory_sources(id) ON DELETE SET NULL,
    author_persona TEXT NOT NULL DEFAULT '',
    created_at     INTEGER NOT NULL
);
CREATE INDEX memory_timeline_page ON memory_timeline (page_id, at);

-- Entries are append-only; only links to deleted facts and sources may be cleared.
CREATE TRIGGER memory_timeline_append_only BEFORE UPDATE OF environment_id, page_id, at, text, author_persona, created_at ON memory_timeline
BEGIN
    SELECT RAISE(ABORT, 'memory timeline entries are append-only');
END;

CREATE TABLE memory_conflicts (
    id             TEXT PRIMARY KEY,
    environment_id TEXT NOT NULL REFERENCES environments(id) ON DELETE CASCADE,
    fact_a         TEXT NOT NULL REFERENCES memory_facts(id) ON DELETE CASCADE,
    fact_b         TEXT NOT NULL REFERENCES memory_facts(id) ON DELETE CASCADE,
    verdict        TEXT NOT NULL CHECK (verdict IN ('contradiction', 'temporal_supersession', 'context_dependent', 'duplicate')),
    status         TEXT NOT NULL DEFAULT 'open' CHECK (status IN ('open', 'resolved')),
    resolution     TEXT NOT NULL DEFAULT '',
    note           TEXT NOT NULL DEFAULT '',
    created_at     INTEGER NOT NULL,
    resolved_at    INTEGER
);
CREATE INDEX memory_conflicts_env ON memory_conflicts (environment_id, status);

-- Retrieval units over facts, page compiled truth and timeline entries. Exactly one owner
-- column is set. environment_id and scope are copied from the owner for filtering.
CREATE TABLE memory_chunks (
    id             INTEGER PRIMARY KEY,
    environment_id TEXT NOT NULL REFERENCES environments(id) ON DELETE CASCADE,
    scope          TEXT NOT NULL,
    fact_id        TEXT REFERENCES memory_facts(id) ON DELETE CASCADE,
    page_id        TEXT REFERENCES memory_pages(id) ON DELETE CASCADE,
    timeline_id    TEXT REFERENCES memory_timeline(id) ON DELETE CASCADE,
    title          TEXT NOT NULL DEFAULT '',
    text           TEXT NOT NULL,
    CHECK ((fact_id IS NOT NULL) + (page_id IS NOT NULL) + (timeline_id IS NOT NULL) = 1)
);
CREATE INDEX memory_chunks_scope ON memory_chunks (environment_id, scope);
CREATE INDEX memory_chunks_fact ON memory_chunks (fact_id);
CREATE INDEX memory_chunks_page ON memory_chunks (page_id);
CREATE INDEX memory_chunks_timeline ON memory_chunks (timeline_id);

CREATE VIRTUAL TABLE memory_fts USING fts5 (title, text, content = 'memory_chunks', content_rowid = 'id', tokenize = 'porter unicode61');
CREATE TRIGGER memory_chunks_ai AFTER INSERT ON memory_chunks BEGIN
    INSERT INTO memory_fts (rowid, title, text) VALUES (new.id, new.title, new.text);
END;
CREATE TRIGGER memory_chunks_ad AFTER DELETE ON memory_chunks BEGIN
    INSERT INTO memory_fts (memory_fts, rowid, title, text) VALUES ('delete', old.id, old.title, old.text);
END;

-- One int8 vector per chunk: value[i] = vec[i] * scale. model names the embedder and its
-- version, so vectors from another model are ignored and re-embedded in the background.
CREATE TABLE memory_embeddings (
    chunk_id   INTEGER PRIMARY KEY REFERENCES memory_chunks(id) ON DELETE CASCADE,
    model      TEXT NOT NULL,
    dims       INTEGER NOT NULL,
    scale      REAL NOT NULL,
    vec        BLOB NOT NULL,
    created_at INTEGER NOT NULL
);
