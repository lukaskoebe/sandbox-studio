-- One open span per session. Watchers that synced the same sandbox at once each opened the
-- span they didn't see yet, so a terminal could show several times (058). Copies with the
-- same session and opening time go, the first recorded staying; open spans are unique now.
DELETE FROM session_spans WHERE rowid NOT IN (
    SELECT min(rowid) FROM session_spans
    GROUP BY environment_id, sandbox_id, kind, key, opened_at
);
-- Copies opened at different times while one was open: the later ones close as they opened.
UPDATE session_spans SET closed_at = opened_at
WHERE closed_at IS NULL AND EXISTS (
    SELECT 1 FROM session_spans o
    WHERE o.closed_at IS NULL AND o.environment_id = session_spans.environment_id
      AND o.sandbox_id = session_spans.sandbox_id AND o.kind = session_spans.kind
      AND o.key = session_spans.key AND o.rowid < session_spans.rowid
);
CREATE UNIQUE INDEX session_spans_one_open ON session_spans (environment_id, sandbox_id, kind, key)
    WHERE closed_at IS NULL;
