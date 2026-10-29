-- A sandbox's home disk has a size of its own (docs/sandboxes.md#resources): home_mib, or 0
-- for a sandbox whose home is on its root disk, as with a template built before the home
-- skeleton (exporter version 1). Until now every home disk had 4096 MiB, and whether a sandbox
-- had one followed from its template, so existing sandboxes get that. Builds, browsers and
-- the system's sandboxes have none. A rebase records the home its new generation gets.
ALTER TABLE sandboxes ADD COLUMN home_mib INTEGER NOT NULL DEFAULT 0 CHECK (home_mib >= 0);
UPDATE sandboxes SET home_mib = 4096
 WHERE kind = '' AND build_job_id IS NULL
   AND (template_id IS NULL OR template_id IN (SELECT id FROM templates WHERE exporter_version = '2'));
ALTER TABLE rebases ADD COLUMN home_mib INTEGER NOT NULL DEFAULT 0 CHECK (home_mib >= 0);
UPDATE rebases SET home_mib = (SELECT home_mib FROM sandboxes WHERE sandboxes.id = rebases.sandbox_id);
