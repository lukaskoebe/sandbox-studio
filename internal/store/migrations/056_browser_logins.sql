-- A browser login is a teammate's secret that only its browser uses (docs/browser.md): Studio
-- fills it into pages and the gateway sends it as HTTP Basic credentials, never a sandbox. The
-- username goes with it as plain text, since pages show it.
ALTER TABLE secrets ADD COLUMN browser_only INTEGER NOT NULL DEFAULT 0
    CHECK (browser_only = 0 OR (teammate_id IS NOT NULL AND studio_only = 0));
ALTER TABLE secrets ADD COLUMN username TEXT NOT NULL DEFAULT '';
-- Whether the teammate's agents fill the login, and send it as Basic credentials, without asking
-- the user each time.
ALTER TABLE secrets ADD COLUMN agent_may_use INTEGER NOT NULL DEFAULT 0 CHECK (agent_may_use = 0 OR browser_only = 1);
