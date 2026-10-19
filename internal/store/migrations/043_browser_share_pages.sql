-- browser_share_pages: after the user hands the teammate's browser back, the agent hears which
-- pages it was on while the user drove (docs/browser.md). Off by default: what the user does in
-- the browser stays private to them.
ALTER TABLE teammates ADD COLUMN browser_share_pages INTEGER NOT NULL DEFAULT 0;
