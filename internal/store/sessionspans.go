package store

import (
	"context"
	"database/sql"
	"strings"
	"time"
)

// The kinds of session span (migration 058).
const (
	SpanTerminal = "terminal"
	SpanRun      = "run"
	SpanBrowser  = "browser"
	SpanDrive    = "drive"
	SpanPreview  = "preview"
)

// SessionSpan is when one session of a sandbox was open: a terminal, a program running in
// one, a browser session, the user's turn in it, or a port listening.
type SessionSpan struct {
	ID            string     `json:"id"`
	EnvironmentID string     `json:"-"`
	SandboxID     string     `json:"sandboxId"`
	Kind          string     `json:"kind" enum:"terminal,run,browser,drive,preview"`
	Key           string     `json:"key" doc:"What the session is within its sandbox and kind: a terminal's name and creation time, a browser session's ID or a port"`
	Parent        string     `json:"parent,omitempty" doc:"The key of the terminal a run is in, or of the browser session the user drove"`
	Label         string     `json:"label,omitempty" doc:"What it shows: a terminal's name, a program's command line, a page's URL"`
	ChatID        string     `json:"chatId,omitempty" doc:"The chat a terminal continues, or whose turn opened a browser session"`
	TeammateID    string     `json:"teammateId,omitempty" doc:"Whose browser a browser session is in"`
	Harness       string     `json:"harness,omitempty" doc:"The agent harness running in a terminal"`
	OpenedAt      time.Time  `json:"openedAt"`
	ClosedAt      *time.Time `json:"closedAt,omitempty" doc:"Unset while it is open"`
}

const spanCols = "id, environment_id, sandbox_id, kind, key, parent, label, chat_id, teammate_id, harness, opened_at, closed_at"

func scanSpan(row interface{ Scan(...any) error }) (SessionSpan, error) {
	var sp SessionSpan
	var opened int64
	var closed sql.NullInt64
	err := row.Scan(&sp.ID, &sp.EnvironmentID, &sp.SandboxID, &sp.Kind, &sp.Key, &sp.Parent, &sp.Label,
		&sp.ChatID, &sp.TeammateID, &sp.Harness, &opened, &closed)
	sp.OpenedAt = time.Unix(opened, 0)
	if closed.Valid {
		t := time.Unix(closed.Int64, 0)
		sp.ClosedAt = &t
	}
	return sp, err
}

func (s *Store) spans(ctx context.Context, where string, args ...any) ([]SessionSpan, error) {
	rows, err := s.db.QueryContext(ctx, "SELECT "+spanCols+" FROM session_spans WHERE "+where, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []SessionSpan{}
	for rows.Next() {
		sp, err := scanSpan(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, sp)
	}
	return out, rows.Err()
}

// SessionSpans lists the environment's spans open at some time from since to until,
// oldest first.
func (s *Store) SessionSpans(ctx context.Context, envID string, since, until time.Time) ([]SessionSpan, error) {
	return s.spans(ctx, "environment_id = ? AND opened_at <= ? AND (closed_at IS NULL OR closed_at >= ?) ORDER BY opened_at, id",
		envID, until.Unix(), since.Unix())
}

// SpanScope is the open spans a watcher saw all of: those of one kind, in one sandbox or,
// without SandboxID, in the whole environment.
type SpanScope struct {
	EnvironmentID, SandboxID, Kind string
}

// SyncSessionSpans makes the open spans in scope what live says is open at: a span whose
// sandbox and key live lacks closes, and one live has that isn't open opens at its
// OpenedAt, or at if that is unset or later. A span that stays open keeps its row, though its
// label follows live's. It reports whether a span opened or closed.
func (s *Store) SyncSessionSpans(ctx context.Context, scope SpanScope, live []SessionSpan, at time.Time) (bool, error) {
	where, args := "environment_id = ? AND kind = ? AND closed_at IS NULL", []any{scope.EnvironmentID, scope.Kind}
	if scope.SandboxID != "" {
		where += " AND sandbox_id = ?"
		args = append(args, scope.SandboxID)
	}
	open, err := s.spans(ctx, where, args...)
	if err != nil {
		return false, err
	}
	id := func(sp SessionSpan) string { return sp.SandboxID + "\x00" + sp.Key }
	seen := map[string]SessionSpan{}
	for _, sp := range live {
		if scope.SandboxID != "" {
			sp.SandboxID = scope.SandboxID
		}
		seen[id(sp)] = sp
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return false, err
	}
	defer tx.Rollback()
	changed := false
	have := map[string]bool{}
	for _, sp := range open {
		if now, ok := seen[id(sp)]; ok && !have[id(sp)] {
			have[id(sp)] = true
			if label := cutLabel(now.Label); label != sp.Label && label != "" {
				if _, err := tx.ExecContext(ctx, "UPDATE session_spans SET label = ? WHERE id = ?", label, sp.ID); err != nil {
					return false, err
				}
			}
			continue
		}
		if _, err := tx.ExecContext(ctx, "UPDATE session_spans SET closed_at = ? WHERE id = ?", max(at.Unix(), sp.OpenedAt.Unix()), sp.ID); err != nil {
			return false, err
		}
		changed = true
	}
	for _, sp := range seen {
		if have[id(sp)] {
			continue
		}
		have[id(sp)] = true
		opened := sp.OpenedAt
		if opened.IsZero() || opened.After(at) {
			opened = at
		}
		if _, err := tx.ExecContext(ctx, "INSERT INTO session_spans ("+spanCols+") VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, NULL)",
			NewID(), scope.EnvironmentID, sp.SandboxID, scope.Kind, sp.Key, sp.Parent, cutLabel(sp.Label), sp.ChatID,
			sp.TeammateID, sp.Harness, opened.Unix()); err != nil {
			return false, err
		}
		changed = true
	}
	return changed, tx.Commit()
}

// CloseSessionSpansExcept closes the open spans of kinds at, but those of the sandboxes in
// keep: their watcher no longer sees their sandboxes running.
func (s *Store) CloseSessionSpansExcept(ctx context.Context, kinds []string, keep []string, at time.Time) (int64, error) {
	args := []any{at.Unix()}
	q := "UPDATE session_spans SET closed_at = max(?, opened_at) WHERE closed_at IS NULL AND kind IN (" + marks(len(kinds)) + ")"
	for _, k := range kinds {
		args = append(args, k)
	}
	if len(keep) > 0 {
		q += " AND sandbox_id NOT IN (" + marks(len(keep)) + ")"
		for _, id := range keep {
			args = append(args, id)
		}
	}
	res, err := s.db.ExecContext(ctx, q, args...)
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}

// PruneSessionSpans deletes the spans that closed before.
func (s *Store) PruneSessionSpans(ctx context.Context, before time.Time) error {
	_, err := s.db.ExecContext(ctx, "DELETE FROM session_spans WHERE closed_at IS NOT NULL AND closed_at < ?", before.Unix())
	return err
}

func marks(n int) string { return strings.TrimSuffix(strings.Repeat("?, ", n), ", ") }

// cutLabel keeps a label to 200 characters.
func cutLabel(s string) string {
	if r := []rune(s); len(r) > 200 {
		return string(r[:199]) + "…"
	}
	return s
}

// ChatTurnSpan is one turn of a chat, with the chat it is in, for Home's lines.
type ChatTurnSpan struct {
	ChatID     string     `json:"chatId"`
	SandboxID  string     `json:"sandboxId"`
	TeammateID string     `json:"teammateId"`
	Title      string     `json:"title"`
	State      string     `json:"state" doc:"The chat's state now"`
	Scheduled  bool       `json:"scheduled,omitempty" doc:"The chat was opened by a heartbeat or a scheduled task, not by the user"`
	Origin     string     `json:"origin" enum:"chat,terminal"`
	StopReason string     `json:"stopReason,omitempty"`
	StartedAt  time.Time  `json:"startedAt"`
	EndedAt    *time.Time `json:"endedAt,omitempty" doc:"Unset while the turn runs"`
}

// maxTurnSpans caps the turns EnvironmentTurns returns, oldest dropped first.
const maxTurnSpans = 2000

// EnvironmentTurns lists the turns of the environment's chats that ran at some time from
// since to until, oldest first; turns undone are left out.
func (s *Store) EnvironmentTurns(ctx context.Context, envID string, since, until time.Time) ([]ChatTurnSpan, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT * FROM (SELECT t.chat_id, c.sandbox_id, c.teammate_id, c.title, c.state,
			c.heartbeat = 1 OR c.task_id != '', t.origin, t.stop_reason, t.started_at, t.ended_at
		FROM chat_turns t JOIN chats c ON c.id = t.chat_id
		WHERE c.environment_id = ? AND t.started_at <= ? AND (t.ended_at IS NULL OR t.ended_at >= ?) AND t.undone_at IS NULL
		ORDER BY t.started_at DESC LIMIT ?) ORDER BY started_at`, envID, until.Unix(), since.Unix(), maxTurnSpans)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []ChatTurnSpan{}
	for rows.Next() {
		var t ChatTurnSpan
		var started int64
		var ended sql.NullInt64
		if err := rows.Scan(&t.ChatID, &t.SandboxID, &t.TeammateID, &t.Title, &t.State, &t.Scheduled, &t.Origin, &t.StopReason,
			&started, &ended); err != nil {
			return nil, err
		}
		t.StartedAt = time.Unix(started, 0)
		if ended.Valid {
			at := time.Unix(ended.Int64, 0)
			t.EndedAt = &at
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

// EnvironmentEvents lists the events of the environment's sandboxes from since to until, in
// order.
func (s *Store) EnvironmentEvents(ctx context.Context, envID string, since, until time.Time) ([]SandboxEvent, error) {
	rows, err := s.db.QueryContext(ctx, "SELECT "+sandboxEventCols+" FROM sandbox_events WHERE environment_id = ? AND created_at >= ? AND created_at <= ? ORDER BY created_at, id",
		envID, since.Unix(), until.Unix())
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []SandboxEvent{}
	for rows.Next() {
		ev, err := scanSandboxEvent(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, ev)
	}
	return out, rows.Err()
}
