-- Host ports: the ports of the computer Studio runs on that an environment's sandboxes may
-- connect to, as host.studio.internal, as a JSON array of numbers. Only the owner edits the list
-- in Network settings; approvals, imports and reviewers never write it.
ALTER TABLE environments ADD COLUMN host_ports TEXT NOT NULL DEFAULT '[]';
