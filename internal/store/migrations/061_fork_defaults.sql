-- A base: a prepared sandbox new ones start from. What its forks take by default, as JSON;
-- NULL for an ordinary sandbox.
ALTER TABLE sandboxes ADD COLUMN fork_defaults TEXT;
