-- Memory extraction of a chat that failed, or waits for the daily budget, is tried again later
-- instead of being dropped: memory_retry_at is when the sweep may read the chat again (unix, 0 for
-- now) and memory_attempts counts the failed calls since its cursor last moved.
ALTER TABLE chats ADD COLUMN memory_retry_at INTEGER NOT NULL DEFAULT 0;
ALTER TABLE chats ADD COLUMN memory_attempts INTEGER NOT NULL DEFAULT 0;
