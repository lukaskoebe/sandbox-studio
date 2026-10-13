-- Teammate settings: the avatar shown instead of the initials.
ALTER TABLE teammates ADD COLUMN avatar TEXT NOT NULL DEFAULT '';
