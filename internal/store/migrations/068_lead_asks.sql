-- Ask Lead (docs/lead.md): the user's questions about a day and Lead's answers, and when the
-- user last looked at the day, for Home's "since you looked".
CREATE TABLE lead_asks (
    id             TEXT PRIMARY KEY,
    environment_id TEXT NOT NULL REFERENCES environments(id) ON DELETE CASCADE,
    day            TEXT NOT NULL,                     -- the day asked about, YYYY-MM-DD
    question       TEXT NOT NULL,
    answer         TEXT NOT NULL DEFAULT '',          -- the model's words, refs such as [push:2] checked
    refs           TEXT NOT NULL DEFAULT '',          -- JSON: the facts the answer's refs point at
    state          TEXT NOT NULL DEFAULT 'answering', -- answering, answered or failed
    note           TEXT NOT NULL DEFAULT '',          -- why there is no answer, when there is none
    model          TEXT NOT NULL DEFAULT '',
    cost           INTEGER NOT NULL DEFAULT 0,
    priced         INTEGER NOT NULL DEFAULT 0,
    asked_at       INTEGER NOT NULL,
    answered_at    INTEGER NOT NULL DEFAULT 0
);
CREATE INDEX lead_asks_day ON lead_asks (environment_id, day, asked_at);

CREATE TABLE lead_seen (
    environment_id TEXT PRIMARY KEY REFERENCES environments(id) ON DELETE CASCADE,
    seen_at        INTEGER NOT NULL
);
