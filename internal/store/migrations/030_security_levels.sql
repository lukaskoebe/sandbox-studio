-- Network security levels and the curated list (C1 part 3, docs/network.md).
--
-- security_level is the environment's level, which decides what happens when no rule matches:
-- locked asks for everything, as Studio did before levels, so existing environments are locked.
-- New environments start at guarded.
ALTER TABLE environments ADD COLUMN security_level TEXT NOT NULL DEFAULT 'guarded'
    CHECK (security_level IN ('locked', 'guarded', 'assisted', 'open'));
UPDATE environments SET security_level = 'locked';

-- curated_off: the IDs of the curated list entries the environment turned off, as a JSON array.
ALTER TABLE environments ADD COLUMN curated_off TEXT NOT NULL DEFAULT '[]';

-- A teammate's level overrides its environment's; '' means the environment's.
ALTER TABLE teammates ADD COLUMN security_level TEXT NOT NULL DEFAULT ''
    CHECK (security_level IN ('', 'locked', 'guarded', 'assisted', 'open'));

-- reverted: the user undid an AI reviewer decision, which deleted its rule (policy.Revert).
ALTER TABLE approvals ADD COLUMN reverted INTEGER NOT NULL DEFAULT 0;
