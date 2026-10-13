-- Browser site scope and tracker blocking (C1 part 2).
--
-- method and path_prefix narrow a rule to requests of a terminated connection (see docs/network.md):
-- '' matches any method or path; a path prefix ends in * and matches the paths below it.
ALTER TABLE rules ADD COLUMN method TEXT NOT NULL DEFAULT '';
ALTER TABLE rules ADD COLUMN path_prefix TEXT NOT NULL DEFAULT '';

-- block_trackers: the environment's browser VMs are denied the requests to the tracker list
-- (internal/policy/trackers.txt), unless a rule allows them. On by default.
ALTER TABLE environments ADD COLUMN block_trackers INTEGER NOT NULL DEFAULT 1;
