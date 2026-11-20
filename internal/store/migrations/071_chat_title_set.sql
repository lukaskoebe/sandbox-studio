-- A chat the user renamed keeps that name: the agent's titles no longer replace it.
ALTER TABLE chats ADD COLUMN title_set INTEGER NOT NULL DEFAULT 0;
