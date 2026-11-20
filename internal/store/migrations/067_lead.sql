-- Lead, the teammate without a computer that writes up the environment's day (docs/lead.md):
-- its settings, the days it wrote, the short note it keeps of each teammate's day, and what its
-- model calls cost each day. Days are local dates (YYYY-MM-DD), since the day is the user's.
CREATE TABLE lead_settings (
    environment_id TEXT PRIMARY KEY REFERENCES environments(id) ON DELETE CASCADE,
    name           TEXT NOT NULL DEFAULT 'Lead',
    write_at       INTEGER NOT NULL DEFAULT 1080,    -- minutes after local midnight: 18:00
    days           INTEGER NOT NULL DEFAULT 31,      -- bit 0 Monday … bit 6 Sunday: weekdays
    skip_quiet     INTEGER NOT NULL DEFAULT 1,       -- no day for a day without turns
    own_computers  INTEGER NOT NULL DEFAULT 0,       -- the user's own computers are part of the day
    show_costs     INTEGER NOT NULL DEFAULT 1,
    heading        INTEGER NOT NULL DEFAULT 1,       -- the day's one line of tone
    daily_limit    INTEGER NOT NULL DEFAULT 1000000, -- USD × 10^6
    notify         TEXT NOT NULL DEFAULT 'waiting',  -- waiting, always or never
    updated_at     INTEGER NOT NULL DEFAULT 0
);

CREATE TABLE lead_days (
    environment_id TEXT NOT NULL REFERENCES environments(id) ON DELETE CASCADE,
    day            TEXT NOT NULL,
    written_at     INTEGER NOT NULL,
    trigger        TEXT NOT NULL,             -- schedule or now
    model          TEXT NOT NULL DEFAULT '',  -- the model that wrote the sentences
    cost           INTEGER NOT NULL DEFAULT 0,
    priced         INTEGER NOT NULL DEFAULT 0,
    facts          TEXT NOT NULL,             -- JSON: the day as Studio's records have it
    prose          TEXT NOT NULL DEFAULT '',  -- JSON: the model's sentences, their ids checked
    note           TEXT NOT NULL DEFAULT '',  -- why the day has no sentences, when it has none
    digest         TEXT NOT NULL DEFAULT '',  -- of what the sentences were written from
    PRIMARY KEY (environment_id, day)
);

-- teammate_id is empty for the user's own computers. It has no foreign key, so a day keeps its
-- notes of a teammate deleted since.
CREATE TABLE lead_notes (
    environment_id TEXT NOT NULL REFERENCES environments(id) ON DELETE CASCADE,
    day            TEXT NOT NULL,
    teammate_id    TEXT NOT NULL,
    note           TEXT NOT NULL,
    through        INTEGER NOT NULL,          -- the newest summary or turn the note has taken in
    updated_at     INTEGER NOT NULL,
    PRIMARY KEY (environment_id, day, teammate_id)
);

CREATE TABLE lead_usage (
    environment_id TEXT NOT NULL REFERENCES environments(id) ON DELETE CASCADE,
    day            TEXT NOT NULL,
    calls          INTEGER NOT NULL DEFAULT 0,
    input_tokens   INTEGER NOT NULL DEFAULT 0,
    output_tokens  INTEGER NOT NULL DEFAULT 0,
    cost           INTEGER NOT NULL DEFAULT 0,
    skipped        INTEGER NOT NULL DEFAULT 0,
    PRIMARY KEY (environment_id, day)
);
