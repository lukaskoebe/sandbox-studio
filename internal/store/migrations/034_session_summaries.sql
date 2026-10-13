-- Session summaries and chat extraction (docs/memory.md). Each extraction run updates a short
-- running summary of its session, which the next sessions' context shows. Chats are extracted by
-- Studio itself once they are idle; memory_seq is the last chat item seq extracted.
ALTER TABLE memory_agent_sessions ADD COLUMN summary TEXT NOT NULL DEFAULT '';
ALTER TABLE memory_agent_sessions ADD COLUMN summary_at INTEGER;
ALTER TABLE chats ADD COLUMN memory_seq INTEGER NOT NULL DEFAULT 0;
