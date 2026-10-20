-- Budgets (docs/teammates.md#budgets). The gateway records every model call it meters, with
-- what it cost at list prices in millionths of a USD; priced is 0 for a model without a known
-- price, or a response without usage. A call names its chat, turn and task only when it was
-- the one turn running in its sandbox. Calls outlive their sandboxes and chats, so a day's
-- spend stays what it was.
CREATE TABLE model_calls (
    id                 INTEGER PRIMARY KEY,
    environment_id     TEXT NOT NULL REFERENCES environments(id) ON DELETE CASCADE,
    sandbox_id         TEXT NOT NULL,
    teammate_id        TEXT NOT NULL DEFAULT '',
    chat_id            TEXT NOT NULL DEFAULT '',
    turn_id            TEXT NOT NULL DEFAULT '',
    task_id            TEXT NOT NULL DEFAULT '',
    host               TEXT NOT NULL,
    model              TEXT NOT NULL,
    input_tokens       INTEGER NOT NULL DEFAULT 0,
    output_tokens      INTEGER NOT NULL DEFAULT 0,
    cache_read_tokens  INTEGER NOT NULL DEFAULT 0,
    cache_write_tokens INTEGER NOT NULL DEFAULT 0,
    cost               INTEGER NOT NULL DEFAULT 0,
    priced             INTEGER NOT NULL DEFAULT 1,
    at                 INTEGER NOT NULL
);
CREATE INDEX model_calls_environment ON model_calls (environment_id, at);
CREATE INDEX model_calls_teammate ON model_calls (teammate_id, at) WHERE teammate_id != '';
CREATE INDEX model_calls_chat ON model_calls (chat_id) WHERE chat_id != '';

-- A teammate's limits: USD micros per local day, and minutes per chat turn. A task's: USD
-- micros and minutes per turn of its runs. 0 is no limit.
ALTER TABLE teammates ADD COLUMN daily_spend_limit INTEGER NOT NULL DEFAULT 0;
ALTER TABLE teammates ADD COLUMN turn_minutes INTEGER NOT NULL DEFAULT 0;
ALTER TABLE teammate_tasks ADD COLUMN run_spend_limit INTEGER NOT NULL DEFAULT 0;
ALTER TABLE teammate_tasks ADD COLUMN run_minutes INTEGER NOT NULL DEFAULT 0;
