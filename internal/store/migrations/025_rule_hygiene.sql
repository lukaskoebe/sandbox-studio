-- Network rule hygiene and once-decisions (C1).
--
-- origin says where a rule came from. Rules written before this migration were made by hand
-- or through the API, so they are manual. The other origins are set by the approvals that
-- create rules and, later, by the curated list and the AI reviewer.
ALTER TABLE rules ADD COLUMN origin TEXT NOT NULL DEFAULT 'manual'
    CHECK (origin IN ('manual', 'approval', 'curated', 'reviewer'));

-- expires_at: the rule stops matching at this time, and a sweep deletes it. NULL never expires.
ALTER TABLE rules ADD COLUMN expires_at INTEGER;
CREATE INDEX rules_expires ON rules (expires_at) WHERE expires_at IS NOT NULL;

-- hits and last_hit_at: how often and when the policy matched the rule. The policy batches
-- them and writes at most once a minute, so they can lag behind the connections they count.
ALTER TABLE rules ADD COLUMN hits INTEGER NOT NULL DEFAULT 0;
ALTER TABLE rules ADD COLUMN last_hit_at INTEGER;

-- once: a network request decided for its one connection, without a rule. Activity shows it.
ALTER TABLE approvals ADD COLUMN once INTEGER NOT NULL DEFAULT 0;
