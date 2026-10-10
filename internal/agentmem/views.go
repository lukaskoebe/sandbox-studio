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
	PersonaID   string    `json:"personaId"`
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

// Sessions lists an environment's sessions, newest first, optionally of one persona.
func (s *Service) Sessions(ctx context.Context, env, persona string, limit int) ([]SessionView, error) {
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	rows, err := s.db.QueryContext(ctx, `SELECT s.id, s.persona_id, s.sandbox_id, s.sandbox_name, s.harness, s.session_ref, s.git_remote,
		s.started_at, s.last_event_at,
		(SELECT COUNT(*) FROM memory_session_log l WHERE l.session_id = s.id AND l.kind IN ('context', 'recall')),
		(SELECT COUNT(*) FROM memory_session_log l WHERE l.session_id = s.id AND l.kind = 'write')
		FROM memory_agent_sessions s WHERE s.environment_id = ? AND (? = '' OR s.persona_id = ?)
		ORDER BY s.last_event_at DESC LIMIT ?`, env, persona, persona, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []SessionView{}
	for rows.Next() {
		var v SessionView
		var started, last int64
		if err := rows.Scan(&v.ID, &v.PersonaID, &v.SandboxID, &v.SandboxName, &v.Harness, &v.SessionRef, &v.GitRemote,
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
	ID        int64           `json:"id"`
	SessionID string          `json:"sessionId,omitempty"`
	PersonaID string          `json:"personaId"`
	At        time.Time       `json:"at"`
	Kind      string          `json:"kind" enum:"context,recall,write,extraction,tool"`
	ItemType  string          `json:"itemType,omitempty"`
	ItemID    string          `json:"itemId,omitempty"`
	Score     *float64        `json:"score,omitempty"`
	Why       json.RawMessage `json:"why,omitempty" doc:"Why it happened: the search explanation, threshold or extraction details"`
	Summary   string          `json:"summary"`
}

// SessionLog is a session's log in order. ErrNotFound-free: an unknown session has none.
func (s *Service) SessionLog(ctx context.Context, env, sessionID string) ([]LogEntry, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id, IFNULL(session_id, ''), persona_id, at, kind, item_type, item_id, score, why, summary
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
		if err := rows.Scan(&e.ID, &e.SessionID, &e.PersonaID, &at, &e.Kind, &e.ItemType, &e.ItemID, &score, &why, &e.Summary); err != nil {
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
	for _, p := range ps {
		m, _ := UtilityFor(p)
		m.Provider = p.Name
		v.Models = append(v.Models, m)
	}
	return v, nil
}
