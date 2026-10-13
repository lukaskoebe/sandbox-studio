package agentmem

import (
	"context"
	"database/sql"
	"encoding/json"
	"time"
)

// SessionView is a harness session that used memory.
type SessionView struct {
	ID          string    `json:"id"`
	TeammateID  string    `json:"teammateId"`
	SandboxID   string    `json:"sandboxId"`
	SandboxName string    `json:"sandboxName"`
	Harness     string    `json:"harness"`
	SessionRef  string    `json:"sessionRef" doc:"The harness's session ID; \"tools\" for tool calls outside a hooked session"`
	GitRemote   string    `json:"gitRemote,omitempty"`
	StartedAt   time.Time `json:"startedAt"`
	LastEventAt time.Time `json:"lastEventAt"`
	Injections  int       `json:"injections" doc:"Items given to the session as context or recall"`
	Writes      int       `json:"writes"`
}

// Sessions lists an environment's sessions, newest first, optionally of one teammate.
func (s *Service) Sessions(ctx context.Context, env, teammate string, limit int) ([]SessionView, error) {
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	rows, err := s.db.QueryContext(ctx, `SELECT s.id, s.teammate_id, s.sandbox_id, s.sandbox_name, s.harness, s.session_ref, s.git_remote,
		s.started_at, s.last_event_at,
		(SELECT COUNT(*) FROM memory_session_log l WHERE l.session_id = s.id AND l.kind IN ('context', 'recall')),
		(SELECT COUNT(*) FROM memory_session_log l WHERE l.session_id = s.id AND l.kind = 'write')
		FROM memory_agent_sessions s WHERE s.environment_id = ? AND (? = '' OR s.teammate_id = ?)
		ORDER BY s.last_event_at DESC LIMIT ?`, env, teammate, teammate, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []SessionView{}
	for rows.Next() {
		var v SessionView
		var started, last int64
		if err := rows.Scan(&v.ID, &v.TeammateID, &v.SandboxID, &v.SandboxName, &v.Harness, &v.SessionRef, &v.GitRemote,
			&started, &last, &v.Injections, &v.Writes); err != nil {
			return nil, err
		}
		v.StartedAt, v.LastEventAt = time.Unix(started, 0).UTC(), time.Unix(last, 0).UTC()
		out = append(out, v)
	}
	return out, rows.Err()
}

// LogEntry is one thing memory did in a session, with its reason.
type LogEntry struct {
	ID         int64           `json:"id"`
	SessionID  string          `json:"sessionId,omitempty"`
	TeammateID string          `json:"teammateId"`
	At         time.Time       `json:"at"`
	Kind       string          `json:"kind" enum:"context,recall,write,extraction,tool"`
	ItemType   string          `json:"itemType,omitempty"`
	ItemID     string          `json:"itemId,omitempty"`
	Score      *float64        `json:"score,omitempty"`
	Why        json.RawMessage `json:"why,omitempty" doc:"Why it happened: the search explanation, threshold or extraction details"`
	Summary    string          `json:"summary"`
}

// SessionLog is a session's log in order. ErrNotFound-free: an unknown session has none.
func (s *Service) SessionLog(ctx context.Context, env, sessionID string) ([]LogEntry, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id, IFNULL(session_id, ''), teammate_id, at, kind, item_type, item_id, score, why, summary
		FROM memory_session_log WHERE environment_id = ? AND session_id = ? ORDER BY id LIMIT 1000`, env, sessionID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []LogEntry{}
	for rows.Next() {
		var e LogEntry
		var at int64
		var score sql.NullFloat64
		var why string
		if err := rows.Scan(&e.ID, &e.SessionID, &e.TeammateID, &at, &e.Kind, &e.ItemType, &e.ItemID, &score, &why, &e.Summary); err != nil {
			return nil, err
		}
		e.At = time.Unix(at, 0).UTC()
		if score.Valid {
			e.Score = &score.Float64
		}
		if why != "" {
			e.Why = json.RawMessage(why)
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

// UsageView is today's extraction spending against the budget, and the utility model of
// each provider.
type UsageView struct {
	Today  Usage          `json:"today"`
	Budget Budget         `json:"budget"`
	Models []UtilityModel `json:"models" doc:"Per provider; empty model means no extraction"`
	// SharedJudge is nil when no teammate's provider has a utility model.
	SharedJudge *SharedJudge `json:"sharedJudge,omitempty" doc:"Whose utility model consolidates shared memory: the teammate that has had one the longest"`
}

// UsageReport is the environment's extraction usage today.
func (s *Service) UsageReport(ctx context.Context, env string) (UsageView, error) {
	u, err := s.usage(ctx, env)
	if err != nil {
		return UsageView{}, err
	}
	ps, err := s.Store.Providers(ctx, env)
	if err != nil {
		return UsageView{}, err
	}
	v := UsageView{Today: u, Budget: s.Budget, Models: []UtilityModel{}}
	if v.SharedJudge, err = s.SharedJudge(ctx, env); err != nil {
		return UsageView{}, err
	}
	for _, p := range ps {
		m, _ := UtilityFor(p)
		m.Provider = p.Name
		v.Models = append(v.Models, m)
	}
	return v, nil
}

// QualityView is the memory's quality at a glance: what the facts are, how the judge decided,
// what consolidation did last and what the sessions got.
type QualityView struct {
	Facts       map[string]int `json:"facts" doc:"Facts per status: active, superseded, disputed, retracted"`
	Contextual  int            `json:"contextual" doc:"Live facts that hold in a context only"`
	Verdicts    map[string]int `json:"verdicts" doc:"Cached judge verdicts, per verdict"`
	Conflicts   map[string]int `json:"conflicts" doc:"Conflicts per status: open, resolved"`
	Suggestions map[string]int `json:"suggestions" doc:"Open suggestions per kind: promote, core"`
	LastRun     *DreamRun      `json:"lastRun,omitempty" doc:"The latest consolidation run of the environment, of any scope"`
	Sessions    int            `json:"sessions" doc:"Agent sessions with activity in the last seven days"`
	Injected    int            `json:"injected" doc:"Items put into sessions in the last seven days: context packs and recalls"`
	Writes      int            `json:"writes" doc:"Facts written from sessions in the last seven days"`
}

// Quality reports the quality numbers of an environment's memory. They are counted from what is
// stored; the retrieval eval runs as a test and is not part of them.
func (s *Service) Quality(ctx context.Context, env string) (QualityView, error) {
	v := QualityView{Facts: map[string]int{}, Verdicts: map[string]int{}, Conflicts: map[string]int{}, Suggestions: map[string]int{}}
	for _, c := range []struct {
		into  map[string]int
		query string
	}{
		{v.Facts, `SELECT status, COUNT(*) FROM memory_facts WHERE environment_id = ? GROUP BY status`},
		{v.Verdicts, `SELECT verdict, COUNT(*) FROM memory_judgments WHERE environment_id = ? GROUP BY verdict`},
		{v.Conflicts, `SELECT status, COUNT(*) FROM memory_conflicts WHERE environment_id = ? GROUP BY status`},
		{v.Suggestions, `SELECT kind, COUNT(*) FROM memory_suggestions WHERE environment_id = ? AND status = 'open' GROUP BY kind`},
	} {
		rows, err := s.db.QueryContext(ctx, c.query, env)
		if err != nil {
			return v, err
		}
		for rows.Next() {
			var k string
			var n int
			if err := rows.Scan(&k, &n); err != nil {
				rows.Close()
				return v, err
			}
			c.into[k] = n
		}
		if err := rows.Err(); err != nil {
			rows.Close()
			return v, err
		}
		rows.Close()
	}
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM memory_facts WHERE environment_id = ? AND context != '' AND status IN ('active', 'disputed')`,
		env).Scan(&v.Contextual); err != nil {
		return v, err
	}
	runs, err := s.DreamRuns(ctx, env, "", 1)
	if err != nil {
		return v, err
	}
	if len(runs) > 0 {
		v.LastRun = &runs[0]
	}
	week := s.now().Add(-7 * 24 * time.Hour).Unix()
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM memory_agent_sessions WHERE environment_id = ? AND last_event_at >= ?`,
		env, week).Scan(&v.Sessions); err != nil {
		return v, err
	}
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM memory_session_log WHERE environment_id = ? AND at >= ? AND kind IN ('context', 'recall')`,
		env, week).Scan(&v.Injected); err != nil {
		return v, err
	}
	err = s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM memory_session_log WHERE environment_id = ? AND at >= ? AND kind = 'write' AND item_type = 'fact'`,
		env, week).Scan(&v.Writes)
	return v, err
}
