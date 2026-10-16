-- browser_only limits a rule to the requests of the environment's browser VMs: the sandboxes'
-- own connections pass over it as if it weren't there (see docs/network.md).
ALTER TABLE rules ADD COLUMN browser_only INTEGER NOT NULL DEFAULT 0;
