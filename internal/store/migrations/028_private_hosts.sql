-- Private hosts: the host names of an environment whose connections may reach private
-- addresses (company VPN hosts), as a JSON array of {"host", "ports"}. Only the owner edits
-- the list in Network settings; approvals, imports and reviewers never write it.
ALTER TABLE environments ADD COLUMN private_hosts TEXT NOT NULL DEFAULT '[]';
