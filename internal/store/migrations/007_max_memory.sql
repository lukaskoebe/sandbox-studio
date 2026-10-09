ALTER TABLE sandboxes ADD COLUMN max_memory_mib INTEGER NOT NULL DEFAULT 0;
UPDATE sandboxes SET max_memory_mib = memory_mib;
