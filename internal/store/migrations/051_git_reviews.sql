-- A teammate's review of a pull request on a forge (docs/git-review.md#pull-request-reviews).
-- The draft itself is the git.review approval's payload; this row follows what became of it,
-- for git_pull_request_review_status and the agent's next prompt.
CREATE TABLE git_reviews (
    id             TEXT PRIMARY KEY,
    environment_id TEXT NOT NULL REFERENCES environments(id) ON DELETE CASCADE,
    sandbox_id     TEXT NOT NULL REFERENCES sandboxes(id) ON DELETE CASCADE,
    approval_id    TEXT NOT NULL,
    forge_id       TEXT NOT NULL,
    forge_name     TEXT NOT NULL,
    owner          TEXT NOT NULL,
    repo           TEXT NOT NULL,
    number         INTEGER NOT NULL,
    head_sha       TEXT NOT NULL,
    state          TEXT NOT NULL CHECK (state IN ('pending', 'posted', 'discarded', 'failed', 'superseded')),
    url            TEXT NOT NULL DEFAULT '',
    result         TEXT NOT NULL DEFAULT '',
    note           TEXT NOT NULL DEFAULT '',
    notified_at    INTEGER,
    created_at     INTEGER NOT NULL,
    updated_at     INTEGER NOT NULL
);
CREATE INDEX git_reviews_approval ON git_reviews (environment_id, approval_id);
CREATE INDEX git_reviews_pull ON git_reviews (sandbox_id, forge_id, owner, repo, number);

CREATE TRIGGER git_reviews_sandbox_owner
BEFORE INSERT ON git_reviews
WHEN NOT EXISTS (SELECT 1 FROM sandboxes WHERE id = NEW.sandbox_id AND environment_id = NEW.environment_id)
BEGIN
    SELECT RAISE(ABORT, 'review sandbox must belong to the same environment');
END;
