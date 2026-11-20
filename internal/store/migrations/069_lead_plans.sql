-- Lead's plans (docs/lead.md): messages to teammates that Lead drafts on the user's request and
-- sends only once the user approves them, and the plan's thread with the user.
CREATE TABLE lead_plans (
    id             TEXT PRIMARY KEY,
    environment_id TEXT NOT NULL REFERENCES environments(id) ON DELETE CASCADE,
    title          TEXT NOT NULL DEFAULT '',
    state          TEXT NOT NULL DEFAULT 'drafting', -- drafting, asking, proposed, running, done, cancelled or failed
    steps          TEXT NOT NULL DEFAULT '[]',       -- JSON: the steps proposed or carried out, each with its state
    approval_id    TEXT NOT NULL DEFAULT '',         -- the lead.plan approval it waits on
    approved_at    INTEGER NOT NULL DEFAULT 0,
    created_at     INTEGER NOT NULL,
    updated_at     INTEGER NOT NULL
);
CREATE INDEX lead_plans_env ON lead_plans (environment_id, updated_at);

CREATE TABLE lead_plan_items (
    id      TEXT PRIMARY KEY,
    plan_id TEXT NOT NULL REFERENCES lead_plans(id) ON DELETE CASCADE,
    kind    TEXT NOT NULL,            -- you, lead, report or note
    text    TEXT NOT NULL DEFAULT '',
    data    TEXT NOT NULL DEFAULT '', -- JSON: a proposal's steps, a question's options, a report's threads
    model   TEXT NOT NULL DEFAULT '',
    cost    INTEGER NOT NULL DEFAULT 0,
    priced  INTEGER NOT NULL DEFAULT 0,
    at      INTEGER NOT NULL
);
CREATE INDEX lead_plan_items_plan ON lead_plan_items (plan_id, at);
