-- A secret's kind names the recipe Studio issues short-lived credentials from
-- (internal/credentials, docs/secrets.md): the sandbox gets those through the tool's own helper,
-- and neither the value nor a placeholder of it. Empty is a plain secret, swapped in for its
-- placeholder. Fields are the kind's other, non-secret fields as a JSON object, such as an AWS
-- access key ID; the sealed value is the kind's secret field.
ALTER TABLE secrets ADD COLUMN kind TEXT NOT NULL DEFAULT ''
    CHECK (kind = '' OR (studio_only = 0 AND browser_only = 0));
ALTER TABLE secrets ADD COLUMN fields TEXT NOT NULL DEFAULT '{}';

-- A user's "always" for a sandbox's credentials of a secret: the secret's credentials are issued
-- to the sandbox, or to every sandbox of the teammate, without asking again.
CREATE TABLE credential_grants (
    environment_id TEXT NOT NULL REFERENCES environments(id) ON DELETE CASCADE,
    secret_id      TEXT NOT NULL REFERENCES secrets(id) ON DELETE CASCADE,
    sandbox_id     TEXT NOT NULL DEFAULT '',
    teammate_id    TEXT NOT NULL DEFAULT '',
    created_at     INTEGER NOT NULL,
    PRIMARY KEY (secret_id, sandbox_id, teammate_id),
    CHECK ((sandbox_id = '') <> (teammate_id = ''))
);
