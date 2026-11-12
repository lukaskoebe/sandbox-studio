-- What a teammate's memory does on its own (docs/memory.md): memory_learn extracts facts from
-- its chats and sessions, memory_dream consolidates its memory every night and after many new
-- facts. Both start on, as before; Dream now and facts it saves itself work either way.
ALTER TABLE teammates ADD COLUMN memory_learn INTEGER NOT NULL DEFAULT 1;
ALTER TABLE teammates ADD COLUMN memory_dream INTEGER NOT NULL DEFAULT 1;
