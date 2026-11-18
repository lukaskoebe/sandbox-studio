-- The pictures agents show in their messages, written ![what it shows](/path/in/the/sandbox):
-- Studio copies each out of the sandbox once the message is written, so the thread keeps it
-- after the file changes or the sandbox is gone (docs/design/chat-mode.md#pictures).
CREATE TABLE chat_images (
    item_id    TEXT NOT NULL REFERENCES chat_items(id) ON DELETE CASCADE,
    src        TEXT NOT NULL,             -- as the message writes it
    chat_id    TEXT NOT NULL,
    file       TEXT NOT NULL DEFAULT '',  -- in Studio's chat-images folder; empty when not copied
    mime_type  TEXT NOT NULL DEFAULT '',
    size       INTEGER NOT NULL DEFAULT 0,
    error      TEXT NOT NULL DEFAULT '',  -- why it wasn't copied, for the user
    created_at INTEGER NOT NULL,
    PRIMARY KEY (item_id, src)
);
