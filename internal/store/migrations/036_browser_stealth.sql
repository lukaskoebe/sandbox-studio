-- browser_stealth: the teammate's browser VM runs Chromium in stealth mode (docs/browser.md#stealth-mode):
-- a desktop Chrome user agent, no automation flag, a desktop screen, and human pointer movement
-- for the agents' clicks. Off by default.
ALTER TABLE teammates ADD COLUMN browser_stealth INTEGER NOT NULL DEFAULT 0;
