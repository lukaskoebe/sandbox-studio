-- Lead's standing orders (docs/lead.md): plans Lead drafts on its own at a set time, which wait
-- for the user's approval as any plan does; and follow-ups that may go without asking.
ALTER TABLE lead_settings ADD COLUMN follow_ups INTEGER NOT NULL DEFAULT 0; -- a follow-up in a thread Lead started goes without asking
ALTER TABLE lead_plans ADD COLUMN order_id TEXT NOT NULL DEFAULT '';        -- the standing order it was drafted for

CREATE TABLE lead_orders (
    id             TEXT PRIMARY KEY,
    environment_id TEXT NOT NULL REFERENCES environments(id) ON DELETE CASCADE,
    text           TEXT NOT NULL,                -- what the user wants each time, as a plan's first message
    at             INTEGER NOT NULL DEFAULT 540, -- minutes after local midnight: 09:00
    days           INTEGER NOT NULL DEFAULT 31,  -- bit 0 Monday … bit 6 Sunday: weekdays
    enabled        INTEGER NOT NULL DEFAULT 1,
    last_day       TEXT NOT NULL DEFAULT '',     -- the local date it last came due, YYYY-MM-DD
    last_plan_id   TEXT NOT NULL DEFAULT '',     -- the plan it drafted last
    note           TEXT NOT NULL DEFAULT '',     -- why it drafted none the last time it came due
    created_at     INTEGER NOT NULL,
    updated_at     INTEGER NOT NULL
);
CREATE INDEX lead_orders_env ON lead_orders (environment_id, created_at);
