package store

import (
	"context"
	"database/sql"
	"sort"
	"strings"
	"time"
)

// ActivityCount adds to how busy a terminal or preview was in one minute (migration 060).
type ActivityCount struct {
	EnvironmentID string
	SandboxID     string
	Kind          string // SpanTerminal or SpanPreview
	Key           string
	Minute        int64 // unix seconds / 60
	Count         int
	Errors        int
}

// AddSessionActivity adds the counts to what their minutes already hold.
func (s *Store) AddSessionActivity(ctx context.Context, counts []ActivityCount) error {
	if len(counts) == 0 {
		return nil
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	for _, c := range counts {
		if _, err := tx.ExecContext(ctx, `INSERT INTO session_activity (environment_id, sandbox_id, kind, key, minute, count, errors)
			VALUES (?, ?, ?, ?, ?, ?, ?)
			ON CONFLICT DO UPDATE SET count = count + excluded.count, errors = errors + excluded.errors`,
			c.EnvironmentID, c.SandboxID, c.Kind, c.Key, c.Minute, c.Count, c.Errors); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// PruneSessionActivity deletes the minutes before before.
func (s *Store) PruneSessionActivity(ctx context.Context, before time.Time) error {
	_, err := s.db.ExecContext(ctx, "DELETE FROM session_activity WHERE minute < ?", before.Unix()/60)
	return err
}

// SessionActivity is how busy one session was in one minute.
type SessionActivity struct {
	SandboxID string    `json:"sandboxId"`
	Kind      string    `json:"kind" enum:"chat,terminal,browser,preview"`
	Key       string    `json:"key" doc:"The session: a chat's ID, a terminal's span key, the teammate whose browser it is, or a port"`
	At        time.Time `json:"at" doc:"The minute's start"`
	Count     int       `json:"count" doc:"Tool calls of a chat, looks that found a terminal had printed, actions in a browser, requests a preview answered"`
	Errors    int       `json:"errors,omitempty" doc:"Of those, failed tool calls, failed or denied browser actions, and preview answers of 500 and up"`
}

// SessionActivityIn returns the environment's activity from since to until, by minute: chats'
// tool calls and browser actions from their logs, terminals' output and previews' requests from
// session_activity.
func (s *Store) SessionActivityIn(ctx context.Context, envID string, since, until time.Time) ([]SessionActivity, error) {
	out := []SessionActivity{}
	add := func(kind, query string, args ...any) error {
		rows, err := s.db.QueryContext(ctx, query, args...)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			a := SessionActivity{Kind: kind}
			var minute int64
			if err := rows.Scan(&a.SandboxID, &a.Kind, &a.Key, &minute, &a.Count, &a.Errors); err != nil {
				return err
			}
			a.At = time.Unix(minute*60, 0)
			out = append(out, a)
		}
		return rows.Err()
	}
	from, to := since.Unix(), until.Unix()
	if err := add("chat", `SELECT IFNULL(c.sandbox_id, ''), 'chat', i.chat_id, i.updated_at / 60, count(*), sum(i.status = 'failed')
		FROM chat_turns t JOIN chats c ON c.id = t.chat_id JOIN chat_items i ON i.turn_id = t.id
		WHERE c.environment_id = ? AND t.started_at <= ? AND (t.ended_at IS NULL OR t.ended_at >= ?) AND t.undone_at IS NULL
			AND i.kind = 'tool' AND i.updated_at BETWEEN ? AND ?
		GROUP BY c.sandbox_id, i.chat_id, i.updated_at / 60`, envID, to, from, from, to); err != nil {
		return nil, err
	}
	if err := add("browser", `SELECT '', 'browser', teammate_id, at / 60, count(*), sum(outcome IN ('failed', 'denied'))
		FROM browser_actions WHERE environment_id = ? AND at BETWEEN ? AND ? AND teammate_id != ''
		GROUP BY teammate_id, at / 60`, envID, from, to); err != nil {
		return nil, err
	}
	if err := add("", `SELECT sandbox_id, kind, key, minute, count, errors FROM session_activity
		WHERE environment_id = ? AND minute BETWEEN ? AND ?`, envID, from/60, to/60); err != nil {
		return nil, err
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].At.Before(out[j].At) })
	return out, nil
}

// The kinds of session flag.
const (
	FlagAsked   = "asked"   // a request waited on the user
	FlagBlocked = "blocked" // a network request was denied
	FlagPush    = "push"    // a push went out, or failed
	FlagFailed  = "failed"  // a command a chat ran failed
)

// SessionFlag is something on a sandbox's line worth a word: a question for the user, a
// blocked request, a push, a failed command.
type SessionFlag struct {
	SandboxID string     `json:"sandboxId"`
	ChatID    string     `json:"chatId,omitempty" doc:"The chat it belongs to, when there is one"`
	Kind      string     `json:"kind" enum:"asked,blocked,push,failed"`
	At        time.Time  `json:"at"`
	Until     *time.Time `json:"until,omitempty" doc:"When a question was answered; unset while it waits"`
	State     string     `json:"state,omitempty" doc:"A question's answer (pending, approved, denied, dismissed), or a push's end (pushed, failed, rejected)"`
	Subject   string     `json:"subject,omitempty" doc:"A question's kind, such as git.push or chat.permission"`
	Label     string     `json:"label" doc:"What it was: a host, a branch, a command"`
	Count     int        `json:"count,omitempty" doc:"How often a blocked request was tried"`
}

// maxFlags is how many flags of each source a window returns, the newest kept.
const maxFlags = 300

// networkKinds are the approvals of network requests: only the denied and the waiting ones are
// flagged, since most are decided by rules without asking.
var networkKinds = map[string]bool{"network": true, "network.request": true}

// SessionFlags returns the environment's flags from since to until, oldest first.
func (s *Store) SessionFlags(ctx context.Context, envID string, since, until time.Time) ([]SessionFlag, error) {
	from, to := since.Unix(), until.Unix()
	out := []SessionFlag{}

	rows, err := s.db.QueryContext(ctx, `SELECT a.sandbox_id, a.kind, a.subject, a.status, a.attempts, a.created_at, a.decided_at,
			IFNULL((SELECT chat_id FROM chat_items WHERE approval_id = a.id LIMIT 1), ''),
			IFNULL(json_extract(a.payload, '$.decided'), ''), IFNULL(json_extract(a.payload, '$.host'), ''),
			IFNULL(json_extract(a.payload, '$.title'), '')
		FROM approvals a
		WHERE a.environment_id = ? AND a.sandbox_id IS NOT NULL AND a.created_at <= ?
			AND ((a.status = 'pending' AND a.decided_at IS NULL) OR a.decided_at >= ?)
		ORDER BY a.created_at DESC LIMIT ?`, envID, to, from, maxFlags)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var f SessionFlag
		var kind, subject, decided, host, title string
		var attempts int
		var created int64
		var decidedAt sql.NullInt64
		if err := rows.Scan(&f.SandboxID, &kind, &subject, &f.State, &attempts, &created, &decidedAt, &f.ChatID, &decided, &host, &title); err != nil {
			rows.Close()
			return nil, err
		}
		f.At = time.Unix(created, 0)
		if decidedAt.Valid {
			t := time.Unix(decidedAt.Int64, 0)
			f.Until = &t
		}
		if networkKinds[kind] {
			// Trackers and Chromium's own requests are noise.
			if decided == "chromium" || strings.HasPrefix(subject, "tracker ") || strings.HasPrefix(subject, "chromium ") {
				continue
			}
			f.Label = host
			if f.Label == "" {
				f.Label = subject
			}
			switch f.State {
			case StatusDenied:
				f.Kind, f.Count = FlagBlocked, attempts
				if f.Until != nil {
					f.At, f.Until = *f.Until, nil
				}
			case StatusPending:
				f.Kind = FlagAsked
			default:
				continue
			}
			f.Subject = kind
			out = append(out, f)
			continue
		}
		// A chat's permission request is named by its tool call's title; other requests'
		// subjects say what they ask.
		f.Kind, f.Subject, f.Label = FlagAsked, kind, subject
		if kind == "chat.permission" && title != "" {
			f.Label = cutLabel(title)
		}
		out = append(out, f)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}

	rows, err = s.db.QueryContext(ctx, `SELECT sandbox_id, ref, repo, state, updated_at FROM git_pushes
		WHERE environment_id = ? AND updated_at BETWEEN ? AND ? AND state IN ('pushed', 'failed', 'rejected')
		ORDER BY updated_at DESC LIMIT ?`, envID, from, to, maxFlags)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		f := SessionFlag{Kind: FlagPush}
		var ref, repo string
		var at int64
		if err := rows.Scan(&f.SandboxID, &ref, &repo, &f.State, &at); err != nil {
			rows.Close()
			return nil, err
		}
		f.At, f.Label = time.Unix(at, 0), strings.TrimPrefix(ref, "refs/heads/")+" → "+repo
		out = append(out, f)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}

	rows, err = s.db.QueryContext(ctx, `SELECT IFNULL(c.sandbox_id, ''), i.chat_id, i.updated_at, IFNULL(json_extract(i.body, '$.title'), '')
		FROM chat_turns t JOIN chats c ON c.id = t.chat_id JOIN chat_items i ON i.turn_id = t.id
		WHERE c.environment_id = ? AND t.started_at <= ? AND (t.ended_at IS NULL OR t.ended_at >= ?) AND t.undone_at IS NULL
			AND i.kind = 'tool' AND i.status = 'failed' AND json_extract(i.body, '$.kind') = 'execute'
			AND i.updated_at BETWEEN ? AND ?
		ORDER BY i.updated_at DESC LIMIT ?`, envID, to, from, from, to, maxFlags)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		f := SessionFlag{Kind: FlagFailed}
		var at int64
		if err := rows.Scan(&f.SandboxID, &f.ChatID, &at, &f.Label); err != nil {
			rows.Close()
			return nil, err
		}
		f.At, f.Label = time.Unix(at, 0), cutLabel(f.Label)
		out = append(out, f)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].At.Before(out[j].At) })
	return out, nil
}
