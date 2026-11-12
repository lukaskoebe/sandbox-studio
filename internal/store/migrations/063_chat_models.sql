-- A teammate offers more models than its default in chats, and each chat runs on one of them
-- (docs/teammates.md#models). models is a JSON array of model ids besides the default; a chat's
-- model is empty while it runs on the teammate's default, and a turn keeps the model it was sent
-- with, so that the thread shows where it changed.
ALTER TABLE teammates ADD COLUMN models TEXT NOT NULL DEFAULT '[]';
ALTER TABLE chats ADD COLUMN model TEXT NOT NULL DEFAULT '';
ALTER TABLE chat_turns ADD COLUMN model TEXT NOT NULL DEFAULT '';
