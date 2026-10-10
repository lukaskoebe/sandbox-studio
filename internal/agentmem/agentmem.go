// Package agentmem is the host side of memory in agent sessions (PLAN §6.7): it answers
// the hooks and memory tools that guests call over their channel, keeps a log of what was
// recalled and written in each session, and extracts facts from transcripts with the
// persona's provider's utility model.
//
// Identity comes only from the channel: the sandbox whose socket a call arrived on, its
// environment and its owning persona. Nothing in a call's payload can name another one.
// A persona reads the shared scope and its own and writes only its own; shared memory
// changes only through an approved memory.share request.
package agentmem

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/lukaskoebe/sandbox-studio/internal/agenthook"
	"github.com/lukaskoebe/sandbox-studio/internal/agentproto"
	"github.com/lukaskoebe/sandbox-studio/internal/memory"
	"github.com/lukaskoebe/sandbox-studio/internal/store"
)

// ApprovalKind is the approval a persona's share request creates.
const ApprovalKind = "memory.share"

// ErrNoPersona means the calling sandbox has no persona, so it has no memory.
var ErrNoPersona = errors.New("this sandbox has no persona, so it has no memory")

// Keys returns the real value of a vault secret.
type Keys interface {
	Value(ctx context.Context, envID, secretID string) (string, error)
}

// Service answers memory calls from guests.
type Service struct {
	Store  *store.Store
	Memory *memory.Service
	Keys   Keys
	// Utility returns the extraction client for a provider; DefaultUtility when nil.
	Utility func(p store.Provider, key string) (LLM, UtilityModel, error)
	// Budget limits extraction per environment and day.
	Budget Budget
	Log    *slog.Logger
	Now    func() time.Time
	// Notify, when set, is told that an environment has a new approval.
	Notify func(envID string)

	db   *sql.DB
	jobs chan extractJob
}

// Budget is the daily limit on extraction calls per environment. Priced models are held
// to MaxCostMicros, estimated before each call; models without a known price (an
// OpenAI-compatible endpoint) to MaxCalls.
type Budget struct {
	MaxCostMicros int64 `json:"maxCostMicros" doc:"USD × 10^6 per day for priced models"`
	MaxCalls      int   `json:"maxCalls" doc:"Calls per day for models without a known price"`
}

// DefaultBudget is $1 a day, or 200 calls.
var DefaultBudget = Budget{MaxCostMicros: 1_000_000, MaxCalls: 200}

// queueSize bounds the transcripts waiting for extraction; a full queue makes the hook
// fail, so the guest keeps its offset and sends the delta again later.
const queueSize = 64

// New returns a service; call Run to extract in the background.
func New(st *store.Store, mem *memory.Service, keys Keys, log *slog.Logger) *Service {
	if log == nil {
		log = slog.New(slog.DiscardHandler)
	}
	return &Service{Store: st, Memory: mem, Keys: keys, Log: log, Budget: DefaultBudget, Now: time.Now,
		db: st.DB(), jobs: make(chan extractJob, queueSize)}
}

func (s *Service) now() time.Time { return s.Now().UTC() }

// caller is who a call comes from, as Studio knows it.
type caller struct {
	env     string
	sandbox store.Sandbox
	persona store.Persona
	scope   string
}

func (s *Service) who(ctx context.Context, sandboxID string) (caller, error) {
	sb, err := s.Store.LookupSandbox(ctx, sandboxID)
	if err != nil {
		return caller{}, fmt.Errorf("unknown sandbox: %w", err)
	}
	if sb.PersonaID == "" {
		return caller{}, ErrNoPersona
	}
	p, err := s.Store.Persona(ctx, sb.EnvironmentID, sb.PersonaID)
	if err != nil {
		return caller{}, fmt.Errorf("persona of the sandbox: %w", err)
	}
	return caller{env: sb.EnvironmentID, sandbox: sb, persona: p, scope: memory.PersonaScope(p.ID)}, nil
}

// HandleCall answers one call of sandboxID's guest; it is agentchan.Hub.OnCall.
func (s *Service) HandleCall(ctx context.Context, sandboxID, method string, params json.RawMessage) (any, error) {
	c, err := s.who(ctx, sandboxID)
	if method == agentproto.MethodHook {
		if err != nil {
			// No persona, no memory: the harness goes on without it.
			return agenthook.Result{}, nil
		}
		var ev agenthook.Event
		if err := json.Unmarshal(params, &ev); err != nil {
			return nil, fmt.Errorf("hook: %w", err)
		}
		return s.hook(ctx, c, ev)
	}
	if err != nil {
		return nil, err
	}
	t, ok := tools[method]
	if !ok {
		return nil, fmt.Errorf("unknown call %q", method)
	}
	res, err := t(s, ctx, c, params)
	if err != nil {
		var ue userError
		if errors.As(err, &ue) || errors.Is(err, memory.ErrInvalid) || errors.Is(err, memory.ErrNotFound) {
			return nil, err
		}
		s.Log.Warn("memory tool failed", "tool", method, "sandbox", sandboxID, "err", err)
		return nil, errors.New(method + " failed")
	}
	return res, nil
}

// userError is a tool error the agent should see verbatim.
type userError struct{ msg string }

func (e userError) Error() string { return e.msg }

func userErrorf(format string, args ...any) error { return userError{fmt.Sprintf(format, args...)} }

// --- sessions and the log -------------------------------------------------------------

// session is a harness session as Studio saw it through hooks.
type session struct {
	id        string
	ref       string
	startedAt time.Time
}

// session returns the session of ref, creating it on its first hook.
func (s *Service) session(ctx context.Context, c caller, ref, remote string) (session, error) {
	now := s.now().Unix()
	_, err := s.db.ExecContext(ctx, `INSERT INTO memory_agent_sessions
		(id, environment_id, persona_id, sandbox_id, sandbox_name, harness, session_ref, git_remote, started_at, last_event_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT (environment_id, sandbox_id, harness, session_ref) DO UPDATE SET last_event_at = excluded.last_event_at,
			git_remote = CASE WHEN excluded.git_remote != '' THEN excluded.git_remote ELSE git_remote END`,
		store.NewID(), c.env, c.persona.ID, c.sandbox.ID, c.sandbox.Name, c.persona.Harness, ref, remote, now, now)
	if err != nil {
		return session{}, err
	}
	var se session
	var started int64
	err = s.db.QueryRowContext(ctx, `SELECT id, session_ref, started_at FROM memory_agent_sessions
		WHERE environment_id = ? AND sandbox_id = ? AND harness = ? AND session_ref = ?`,
		c.env, c.sandbox.ID, c.persona.Harness, ref).Scan(&se.id, &se.ref, &started)
	se.startedAt = time.Unix(started, 0).UTC()
	return se, err
}

// toolSessionRef names the session that tool calls are logged under when the sandbox has
// had no hooked session yet.
const toolSessionRef = "tools"

// latestSession is the sandbox's most recently active session, for tool calls, which
// carry no session ID.
func (s *Service) latestSession(ctx context.Context, c caller) (session, error) {
	var se session
	var started int64
	err := s.db.QueryRowContext(ctx, `SELECT id, session_ref, started_at FROM memory_agent_sessions
		WHERE environment_id = ? AND sandbox_id = ? AND persona_id = ? ORDER BY last_event_at DESC, started_at DESC LIMIT 1`,
		c.env, c.sandbox.ID, c.persona.ID).Scan(&se.id, &se.ref, &started)
	if errors.Is(err, sql.ErrNoRows) {
		return s.session(ctx, c, toolSessionRef, "")
	}
	se.startedAt = time.Unix(started, 0).UTC()
	return se, err
}

// previousSessionStart is when the persona's session before se started; zero if none.
func (s *Service) previousSessionStart(ctx context.Context, c caller, se session) (time.Time, error) {
	var started sql.NullInt64
	err := s.db.QueryRowContext(ctx, `SELECT MAX(started_at) FROM memory_agent_sessions
		WHERE environment_id = ? AND persona_id = ? AND id != ? AND started_at <= ? AND session_ref != ?`,
		c.env, c.persona.ID, se.id, se.startedAt.Unix(), toolSessionRef).Scan(&started)
	if err != nil || !started.Valid {
		return time.Time{}, err
	}
	return time.Unix(started.Int64, 0).UTC(), nil
}

// Log kinds.
const (
	LogContext    = "context"    // part of a session-start context pack
	LogRecall     = "recall"     // injected for a prompt
	LogWrite      = "write"      // a fact written, reinforced, corrected, retracted or proposed
	LogExtraction = "extraction" // an extraction run
	LogTool       = "tool"       // a read through a memory tool
)

// entry is one row of the session log.
type entry struct {
	kind     string
	itemType string
	itemID   string
	score    *float64
	why      any
	summary  string
}

func (s *Service) log(ctx context.Context, c caller, sessionID string, entries ...entry) {
	now := s.now().Unix()
	for _, e := range entries {
		why := ""
		if e.why != nil {
			b, _ := json.Marshal(e.why)
			why = string(b)
		}
		var score any
		if e.score != nil {
			score = *e.score
		}
		var sid any
		if sessionID != "" {
			sid = sessionID
		}
		if _, err := s.db.ExecContext(ctx, `INSERT INTO memory_session_log
			(environment_id, session_id, persona_id, sandbox_id, at, kind, item_type, item_id, score, why, summary)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			c.env, sid, c.persona.ID, c.sandbox.ID, now, e.kind, e.itemType, e.itemID, score, why, truncate(e.summary, 500)); err != nil {
			s.Log.Warn("writing the memory session log failed", "err", err)
		}
	}
}

// injectedIDs are the items already given to a session, which recall skips.
func (s *Service) injectedIDs(ctx context.Context, sessionID string) (map[string]bool, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT item_id FROM memory_session_log WHERE session_id = ? AND kind IN ('context', 'recall') AND item_id != ''`, sessionID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]bool{}
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		out[id] = true
	}
	return out, rows.Err()
}

func truncate(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n-1]) + "…"
}
