package api

import (
	"context"
	"net/http"
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/danielgtaylor/huma/v2"

	"github.com/lukaskoebe/sandbox-studio/internal/memory"
	"github.com/lukaskoebe/sandbox-studio/internal/store"
)

// Bounds of a teammate's timeline.
const (
	timelineDays     = 14
	timelineTurns    = 80
	timelineFacts    = 60
	timelineSessions = 30
	timelinePrompt   = 4000
	timelineAnswer   = 8000
	timelineTools    = 6
)

// TeammateEvent is one thing in a teammate's timeline: a chat turn, a fact it learned, a task,
// or a terminal session as its summary tells it. Exactly the field of its kind is set.
type TeammateEvent struct {
	Kind        string             `json:"kind" enum:"turn,fact,task,session"`
	At          time.Time          `json:"at"`
	SandboxID   string             `json:"sandboxId,omitempty"`
	SandboxName string             `json:"sandboxName,omitempty"`
	Turn        *TeammateTurn      `json:"turn,omitempty"`
	Fact        *TeammateFact      `json:"fact,omitempty"`
	Task        *TeammateTaskEvent `json:"task,omitempty"`
	Session     *TeammateSession   `json:"session,omitempty"`
}

// TeammateTurn is a chat turn: what the user asked, what the teammate answered and which tools
// it used on the way.
type TeammateTurn struct {
	ChatID     string         `json:"chatId"`
	ChatTitle  string         `json:"chatTitle"`
	TurnID     string         `json:"turnId"`
	Origin     string         `json:"origin" enum:"chat,terminal"`
	Heartbeat  bool           `json:"heartbeat" doc:"A check-in the teammate started on its own"`
	Prompt     string         `json:"prompt"`
	Answer     string         `json:"answer"`
	Tools      []TeammateTool `json:"tools"`
	ToolCount  int            `json:"toolCount"`
	Marker     string         `json:"marker,omitempty" doc:"How the turn ended when not normally: stopped, interrupted, error, limit"`
	MarkerText string         `json:"markerText,omitempty"`
	EndedAt    *time.Time     `json:"endedAt,omitempty"`
}

// TeammateTool is a tool call of a turn, by its title.
type TeammateTool struct {
	Title  string `json:"title"`
	Kind   string `json:"kind,omitempty"`
	Status string `json:"status,omitempty"`
}

// TeammateFact is a fact in the teammate's own memory.
type TeammateFact struct {
	ID     string `json:"id"`
	Text   string `json:"text"`
	Kind   string `json:"kind"`
	Tier   string `json:"tier"`
	Status string `json:"status"`
}

// TeammateTaskEvent is a task added to the teammate's list, or finished.
type TeammateTaskEvent struct {
	ID        string `json:"id"`
	Title     string `json:"title"`
	Event     string `json:"event" enum:"added,done"`
	CreatedBy string `json:"createdBy"`
}

// TeammateSession is a terminal session of the teammate, as its memory summary tells it.
type TeammateSession struct {
	ID      string `json:"id"`
	Harness string `json:"harness"`
	Summary string `json:"summary"`
}

func (s *Server) registerTimeline(api huma.API) {
	huma.Register(api, huma.Operation{
		OperationID: "teammateTimeline", Method: http.MethodGet, Path: "/api/environments/{env}/teammates/{id}/timeline", Tags: []string{"teammates"},
		Description: "The teammate's last two weeks in one thread, oldest first: its chat turns with their prompts, answers and tool " +
			"titles, the facts it learned, its tasks, and its terminal sessions as their memory summaries tell them.",
	}, func(ctx context.Context, in *teammatePath) (*struct{ Body []TeammateEvent }, error) {
		if _, err := s.Store.Teammate(ctx, in.Env, in.ID); err != nil {
			return nil, apiError(err)
		}
		out, err := s.timeline(ctx, in.Env, in.ID, time.Now().Add(-timelineDays*24*time.Hour))
		return &struct{ Body []TeammateEvent }{out}, apiError(err)
	})
}

func (s *Server) timeline(ctx context.Context, env, teammate string, since time.Time) ([]TeammateEvent, error) {
	sandboxes, err := s.Store.Sandboxes(ctx, env)
	if err != nil {
		return nil, err
	}
	names := map[string]string{}
	for _, sb := range sandboxes {
		names[sb.ID] = sb.Name
	}
	out := []TeammateEvent{}

	turns, err := s.Store.TeammateTurns(ctx, env, teammate, since, timelineTurns)
	if err != nil {
		return nil, err
	}
	for _, d := range turns {
		t := &TeammateTurn{ChatID: d.ChatID, ChatTitle: d.ChatTitle, TurnID: d.TurnID, Origin: d.Origin, Heartbeat: d.Heartbeat,
			Prompt: clipText(d.Prompt, timelinePrompt), Answer: clipText(strings.Join(d.Answer, "\n\n"), timelineAnswer),
			Tools: []TeammateTool{}, ToolCount: len(d.Tools), EndedAt: d.EndedAt}
		for _, tool := range d.Tools[max(len(d.Tools)-timelineTools, 0):] {
			t.Tools = append(t.Tools, TeammateTool{Title: clipText(tool.Title, 200), Kind: tool.Kind, Status: tool.Status})
		}
		switch d.Marker {
		case "stopped", "interrupted", "error", "limit":
			t.Marker, t.MarkerText = d.Marker, clipText(d.MarkerText, 500)
		}
		out = append(out, TeammateEvent{Kind: "turn", At: d.StartedAt, SandboxID: d.SandboxID, SandboxName: names[d.SandboxID], Turn: t})
	}

	if s.Memory != nil {
		facts, err := s.Memory.Facts(ctx, env, memory.FactFilter{Scope: memory.TeammateScope(teammate)})
		if err != nil {
			return nil, err
		}
		n := 0
		for _, f := range facts { // newest first
			if f.CreatedAt.Before(since) || f.Status == memory.StatusSuperseded || n == timelineFacts {
				continue
			}
			n++
			out = append(out, TeammateEvent{Kind: "fact", At: f.CreatedAt,
				Fact: &TeammateFact{ID: f.ID, Text: f.Text, Kind: f.Kind, Tier: f.Tier, Status: f.Status}})
		}
	}

	tasks, err := s.Store.TeammateTasks(ctx, env, teammate)
	if err != nil {
		return nil, err
	}
	for _, t := range tasks {
		if !t.CreatedAt.Before(since) {
			out = append(out, TeammateEvent{Kind: "task", At: t.CreatedAt, Task: &TeammateTaskEvent{ID: t.ID, Title: t.Title, Event: "added", CreatedBy: t.CreatedBy}})
		}
		if t.Status == store.TaskDone && !t.UpdatedAt.Before(since) {
			out = append(out, TeammateEvent{Kind: "task", At: t.UpdatedAt, Task: &TeammateTaskEvent{ID: t.ID, Title: t.Title, Event: "done", CreatedBy: t.CreatedBy}})
		}
	}

	if s.AgentMem != nil {
		chats, err := s.Store.Chats(ctx, env, "")
		if err != nil {
			return nil, err
		}
		inChat := map[string]bool{}
		for _, c := range chats {
			inChat[c.ACPSessionID], inChat["chat-"+c.ID] = true, true
		}
		sessions, err := s.AgentMem.Sessions(ctx, env, teammate, 100)
		if err != nil {
			return nil, err
		}
		n := 0
		for _, se := range sessions {
			if se.Summary == "" || inChat[se.SessionRef] || se.LastEventAt.Before(since) || n == timelineSessions {
				continue
			}
			n++
			out = append(out, TeammateEvent{Kind: "session", At: se.LastEventAt, SandboxID: se.SandboxID, SandboxName: se.SandboxName,
				Session: &TeammateSession{ID: se.ID, Harness: se.Harness, Summary: se.Summary}})
		}
	}

	slices.SortStableFunc(out, func(a, b TeammateEvent) int { return a.At.Compare(b.At) })
	return out, nil
}

// clipText cuts s to n bytes at a rune boundary, with an ellipsis.
func clipText(s string, n int) string {
	if len(s) <= n {
		return s
	}
	cut := n
	for cut > 0 && !utf8.RuneStart(s[cut]) {
		cut--
	}
	return s[:cut] + "…"
}
