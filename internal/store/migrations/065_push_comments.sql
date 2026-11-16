-- A reviewer's comments on a push now reach the agent's next prompt while the push waits and
-- once it is approved, not only once it is rejected or failed (docs/git-review.md). Notes of
-- pushes from before were told some other way or not at all: they stay untold.
UPDATE git_pushes SET notified_at = updated_at
    WHERE notified_at IS NULL AND note != '' AND state IN ('pending', 'pushed', 'superseded');
