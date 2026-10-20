-- use_host_git_identity: the teammate's sessions commit as the user Studio runs as, with the
-- host's git config --global user.name and user.email, instead of git_name and git_email
-- (docs/teammates.md). Off by default: commits by agents say so.
ALTER TABLE teammates ADD COLUMN use_host_git_identity INTEGER NOT NULL DEFAULT 0;
