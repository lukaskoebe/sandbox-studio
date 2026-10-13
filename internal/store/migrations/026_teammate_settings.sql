-- Teammate settings: the avatar shown instead of the initials, the scope that the approval
-- cards for the teammate's sandboxes preselect for new rules, and the host pattern level the
-- teammate last approved or denied with (see policy.PatternLevel).
ALTER TABLE teammates ADD COLUMN avatar TEXT NOT NULL DEFAULT '';
ALTER TABLE teammates ADD COLUMN approval_scope TEXT NOT NULL DEFAULT 'environment'
    CHECK (approval_scope IN ('sandbox', 'teammate', 'environment'));
ALTER TABLE teammates ADD COLUMN approval_pattern TEXT NOT NULL DEFAULT ''
    CHECK (approval_pattern IN ('', 'exact', 'wildcard'));
