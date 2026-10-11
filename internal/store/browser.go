package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"
)

// --- browser broker ---------------------------------------------------------------------

// CreateBrowserSandbox inserts the browser VM row of a teammate. There is at most one; a
// second one is ErrExists.
func (s *Store) CreateBrowserSandbox(ctx context.Context, sb Sandbox) (Sandbox, error) {
	if sb.TeammateID == "" {
		return sb, errors.New("a browser belongs to a teammate")
	}
	sb.Kind, sb.TemplateID = SandboxKindBrowser, ""
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return sb, err
	}
	defer tx.Rollback()
	var n int
	if err := tx.QueryRowContext(ctx, "SELECT COUNT(*) FROM sandboxes WHERE teammate_id = ? AND kind = 'browser'", sb.TeammateID).Scan(&n); err != nil {
		return sb, err
	}
	if n > 0 {
		return sb, fmt.Errorf("the teammate's browser: %w", ErrExists)
	}
	if sb, err = insertSandbox(ctx, tx, sb); err != nil {
		return sb, err
	}
	return sb, tx.Commit()
}

// BrowserSandbox returns the browser VM row of a teammate.
func (s *Store) BrowserSandbox(ctx context.Context, envID, teammateID string) (Sandbox, error) {
	sb, err := scanSandbox(s.db.QueryRowContext(ctx, "SELECT "+sandboxCols+" FROM sandboxes WHERE environment_id = ? AND teammate_id = ? AND kind = 'browser'", envID, teammateID))
	if errors.Is(err, sql.ErrNoRows) {
		return sb, ErrNotFound
	}
	return sb, err
}

// BrowserSandboxes lists every teammate's browser VM row.
func (s *Store) BrowserSandboxes(ctx context.Context) ([]Sandbox, error) {
	rows, err := s.db.QueryContext(ctx, "SELECT "+sandboxCols+" FROM sandboxes WHERE kind = 'browser' ORDER BY created_at")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Sandbox{}
	for rows.Next() {
		sb, err := scanSandbox(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, sb)
	}
	return out, rows.Err()
}

// Browser pattern verdicts.
const (
	BrowserAllow     = "allow"
	BrowserDeny      = "deny"
	BrowserSensitive = "sensitive"
)

// BrowserPattern is a teammate's rule for its browser; see migration 017.
type BrowserPattern struct {
	ID            string    `json:"id"`
	EnvironmentID string    `json:"environmentId"`
	TeammateID    string    `json:"teammateId"`
	Action        string    `json:"action" doc:"A browser action such as click or fill, or * for every action"`
	Origin        string    `json:"origin" doc:"scheme://host[:port]; the host may start with *."`
	Role          string    `json:"role" doc:"Accessibility role of the element; empty matches any"`
	Label         string    `json:"label" doc:"Glob on the element's accessible name, case-insensitive; empty matches any"`
	Verdict       string    `json:"verdict" enum:"allow,deny,sensitive"`
	CreatedAt     time.Time `json:"createdAt"`
}

const browserPatternCols = "id, environment_id, teammate_id, action, origin, role, label, verdict, created_at"

func scanBrowserPattern(row interface{ Scan(...any) error }) (BrowserPattern, error) {
	var p BrowserPattern
	var created int64
	err := row.Scan(&p.ID, &p.EnvironmentID, &p.TeammateID, &p.Action, &p.Origin, &p.Role, &p.Label, &p.Verdict, &created)
	p.CreatedAt = time.Unix(created, 0)
	return p, err
}

// AddBrowserPattern stores p; an identical pattern is returned instead of a duplicate.
func (s *Store) AddBrowserPattern(ctx context.Context, p BrowserPattern) (BrowserPattern, error) {
	switch p.Verdict {
	case BrowserAllow, BrowserDeny, BrowserSensitive:
	default:
		return p, fmt.Errorf("unknown verdict %q", p.Verdict)
	}
	p.ID, p.CreatedAt = NewID(), time.Unix(now(), 0)
	_, err := s.db.ExecContext(ctx, "INSERT INTO browser_patterns ("+browserPatternCols+") VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?) ON CONFLICT (teammate_id, action, origin, role, label, verdict) DO NOTHING",
		p.ID, p.EnvironmentID, p.TeammateID, p.Action, p.Origin, p.Role, p.Label, p.Verdict, p.CreatedAt.Unix())
	if err != nil {
		if strings.Contains(err.Error(), "FOREIGN KEY") {
			return p, fmt.Errorf("teammate %s: %w", p.TeammateID, ErrNotFound)
		}
		return p, err
	}
	return scanBrowserPattern(s.db.QueryRowContext(ctx, "SELECT "+browserPatternCols+" FROM browser_patterns WHERE teammate_id = ? AND action = ? AND origin = ? AND role = ? AND label = ? AND verdict = ?",
		p.TeammateID, p.Action, p.Origin, p.Role, p.Label, p.Verdict))
}

// BrowserPatterns lists a teammate's patterns, oldest first.
func (s *Store) BrowserPatterns(ctx context.Context, envID, teammateID string) ([]BrowserPattern, error) {
	rows, err := s.db.QueryContext(ctx, "SELECT "+browserPatternCols+" FROM browser_patterns WHERE environment_id = ? AND teammate_id = ? ORDER BY created_at, id", envID, teammateID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []BrowserPattern{}
	for rows.Next() {
		p, err := scanBrowserPattern(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

// DeleteBrowserPattern removes one pattern of a teammate.
func (s *Store) DeleteBrowserPattern(ctx context.Context, envID, teammateID, id string) error {
	res, err := s.db.ExecContext(ctx, "DELETE FROM browser_patterns WHERE environment_id = ? AND teammate_id = ? AND id = ?", envID, teammateID, id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

// BrowserAction is one entry of a browser's action log.
type BrowserAction struct {
	ID            int64     `json:"id"`
	EnvironmentID string    `json:"environmentId"`
	TeammateID    string    `json:"teammateId"`
	SessionID     string    `json:"sessionId"`
	SandboxID     string    `json:"sandboxId,omitempty" doc:"The agent sandbox that drove the action; empty for the user"`
	Actor         string    `json:"actor" enum:"agent,user"`
	Action        string    `json:"action"`
	Target        string    `json:"target,omitempty" doc:"The element, as role and accessible name"`
	URL           string    `json:"url,omitempty"`
	Outcome       string    `json:"outcome" enum:"ok,denied,pending,failed"`
	Detail        string    `json:"detail,omitempty"`
	Screenshot    string    `json:"screenshot,omitempty" doc:"File name of the screenshot taken after the action"`
	At            time.Time `json:"at"`
}

const browserActionCols = "id, environment_id, teammate_id, session_id, sandbox_id, actor, action, target, url, outcome, detail, screenshot, at"

func scanBrowserAction(row interface{ Scan(...any) error }) (BrowserAction, error) {
	var a BrowserAction
	var at int64
	err := row.Scan(&a.ID, &a.EnvironmentID, &a.TeammateID, &a.SessionID, &a.SandboxID, &a.Actor, &a.Action, &a.Target, &a.URL, &a.Outcome, &a.Detail, &a.Screenshot, &at)
	a.At = time.Unix(at, 0)
	return a, err
}

// AddBrowserAction appends to the action log and returns the entry with its ID.
func (s *Store) AddBrowserAction(ctx context.Context, a BrowserAction) (BrowserAction, error) {
	if a.At.IsZero() {
		a.At = time.Unix(now(), 0)
	}
	res, err := s.db.ExecContext(ctx, "INSERT INTO browser_actions (environment_id, teammate_id, session_id, sandbox_id, actor, action, target, url, outcome, detail, screenshot, at) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)",
		a.EnvironmentID, a.TeammateID, a.SessionID, a.SandboxID, a.Actor, a.Action, a.Target, a.URL, a.Outcome, a.Detail, a.Screenshot, a.At.Unix())
	if err != nil {
		return a, err
	}
	a.ID, err = res.LastInsertId()
	return a, err
}

// BrowserActions lists a teammate's action log, newest first; a non-empty sessionID limits
// it to one session.
func (s *Store) BrowserActions(ctx context.Context, envID, teammateID, sessionID string, limit int) ([]BrowserAction, error) {
	rows, err := s.db.QueryContext(ctx, "SELECT "+browserActionCols+` FROM browser_actions
		WHERE environment_id = ? AND teammate_id = ? AND (? = '' OR session_id = ?) ORDER BY id DESC LIMIT ?`,
		envID, teammateID, sessionID, sessionID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []BrowserAction{}
	for rows.Next() {
		a, err := scanBrowserAction(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

// BrowserAction returns one entry of a teammate's action log.
func (s *Store) BrowserAction(ctx context.Context, envID, teammateID string, id int64) (BrowserAction, error) {
	a, err := scanBrowserAction(s.db.QueryRowContext(ctx, "SELECT "+browserActionCols+" FROM browser_actions WHERE environment_id = ? AND teammate_id = ? AND id = ?", envID, teammateID, id))
	if errors.Is(err, sql.ErrNoRows) {
		return a, ErrNotFound
	}
	return a, err
}

// BrowserScreenshots lists the screenshot files the action log refers to, oldest first.
func (s *Store) BrowserScreenshots(ctx context.Context) ([]BrowserAction, error) {
	rows, err := s.db.QueryContext(ctx, "SELECT "+browserActionCols+" FROM browser_actions WHERE screenshot <> '' ORDER BY id")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []BrowserAction{}
	for rows.Next() {
		a, err := scanBrowserAction(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

// ClearBrowserScreenshot forgets the screenshot of an action, once its file is pruned.
func (s *Store) ClearBrowserScreenshot(ctx context.Context, id int64) error {
	_, err := s.db.ExecContext(ctx, "UPDATE browser_actions SET screenshot = '' WHERE id = ?", id)
	return err
}

// PruneBrowserActions keeps the newest keep entries of a teammate's log and returns the
// screenshots of the dropped ones, for the caller to delete.
func (s *Store) PruneBrowserActions(ctx context.Context, envID, teammateID string, keep int) ([]string, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	const old = "environment_id = ? AND teammate_id = ? AND id NOT IN (SELECT id FROM browser_actions WHERE environment_id = ? AND teammate_id = ? ORDER BY id DESC LIMIT ?)"
	shots, err := names(ctx, tx, "SELECT screenshot FROM browser_actions WHERE screenshot <> '' AND "+old, envID, teammateID, envID, teammateID, keep)
	if err != nil {
		return nil, err
	}
	if _, err := tx.ExecContext(ctx, "DELETE FROM browser_actions WHERE "+old, envID, teammateID, envID, teammateID, keep); err != nil {
		return nil, err
	}
	return shots, tx.Commit()
}

// DeleteBrowserActions drops a teammate's whole action log and returns its screenshots.
func (s *Store) DeleteBrowserActions(ctx context.Context, envID, teammateID string) ([]string, error) {
	return s.PruneBrowserActions(ctx, envID, teammateID, 0)
}
