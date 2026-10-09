-- Secret values are sealed by the vault; the store never sees plaintext.
CREATE TABLE secrets (
    id             TEXT PRIMARY KEY,
    environment_id TEXT NOT NULL REFERENCES environments(id) ON DELETE CASCADE,
    name           TEXT NOT NULL,          -- also the env var name sandboxes see
    value          BLOB NOT NULL,          -- sealed by the vault (nonce || AES-GCM ciphertext)
    hosts          TEXT NOT NULL,          -- comma-separated host patterns the value may be sent to
    placeholder    TEXT NOT NULL UNIQUE,   -- what sandboxes see instead of the value
    note           TEXT NOT NULL DEFAULT '',
    created_at     INTEGER NOT NULL,
    updated_at     INTEGER NOT NULL,
    UNIQUE (environment_id, name)
);
