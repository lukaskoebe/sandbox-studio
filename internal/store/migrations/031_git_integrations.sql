-- Git and browser integrations (docs/git-review.md, docs/browser.md). Numbered 031: 030 is
-- other work's.

-- A rejected or failed push is also told to the agent in its chat's next prompt, once.
-- notified_at is when a prompt carried the note; delivered_at is the sandbox's next fetch.
ALTER TABLE git_pushes ADD COLUMN notified_at INTEGER;
