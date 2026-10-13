-- Teammate settings: the avatar shown instead of the initials, and the scope that the
-- approval cards for the teammate's sandboxes preselect for new rules.
ALTER TABLE teammates ADD COLUMN avatar TEXT NOT NULL DEFAULT '';
ALTER TABLE teammates ADD COLUMN approval_scope TEXT NOT NULL DEFAULT 'environment'
    CHECK (approval_scope IN ('sandbox', 'teammate', 'environment'));
