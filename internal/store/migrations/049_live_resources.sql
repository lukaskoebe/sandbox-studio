-- What a running VM has while a resize waits for its next start (docs/sandboxes.md#resources).
-- cpus and memory_mib are what the VM starts with; 0 here means the VM has those, or that it
-- doesn't run. A start clears both.
ALTER TABLE sandboxes ADD COLUMN live_cpus INTEGER NOT NULL DEFAULT 0;
ALTER TABLE sandboxes ADD COLUMN live_memory_mib INTEGER NOT NULL DEFAULT 0;
