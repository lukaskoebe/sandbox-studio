package chat

import (
	"encoding/json"
	"unicode/utf8"

	"github.com/lukaskoebe/sandbox-studio/internal/acp"
	"github.com/lukaskoebe/sandbox-studio/internal/store"
)

// Item kinds.
const (
	KindUser       = "user"
	KindAgent      = "agent"
	KindThought    = "thought"
	KindTool       = "tool"
	KindPlan       = "plan"
	KindPermission = "permission"
	KindQuestion   = "question"
	KindMarker     = "marker"
)

// Marker kinds.
const (
	MarkerStopped     = "stopped"     // the user stopped the turn
	MarkerInterrupted = "interrupted" // the agent or Studio went away mid-turn
	MarkerError       = "error"       // the turn failed
	MarkerLimit       = "limit"       // max tokens, max turn requests or a refusal
	MarkerNotice      = "notice"      // the agent's own notice
	MarkerCompaction  = "compaction"  // the agent compacted its context
	MarkerTerminal    = "terminal"    // the turn happened in the TUI
	MarkerUndo        = "undo"        // the user undid turns
)

// MaxBody caps an item's stored body. Text beyond it is dropped and the body marked
// truncated.
const MaxBody = 256 << 10

// UserBody is the user's prompt.
type UserBody struct {
	Text        string       `json:"text"`
	Attachments []Attachment `json:"attachments,omitempty"`
}

// Attachment describes a file sent with a prompt; its content goes to the agent only.
type Attachment struct {
	Name     string `json:"name"`
	MimeType string `json:"mimeType"`
	Size     int64  `json:"size"`
}

// TextBody is an agent message or thought, streamed in chunks.
type TextBody struct {
	Text      string `json:"text"`
	Truncated bool   `json:"truncated,omitempty"`
}

// ToolBody is a tool call: its status follows the agent's updates.
type ToolBody struct {
	Title     string                 `json:"title"`
	Kind      string                 `json:"kind,omitempty" doc:"read, edit, delete, move, search, execute, think, fetch, switch_mode or other"`
	Status    string                 `json:"status,omitempty" enum:"pending,in_progress,completed,failed"`
	Content   []acp.ToolCallContent  `json:"content,omitempty"`
	Locations []acp.ToolCallLocation `json:"locations,omitempty"`
	Input     json.RawMessage        `json:"input,omitempty" doc:"The tool's raw input, as the agent reports it"`
	Truncated bool                   `json:"truncated,omitempty"`
}

// PlanBody is the agent's plan, replaced whole by each update.
type PlanBody struct {
	Entries []acp.PlanEntry `json:"entries"`
}

// PermissionBody is a tool call the agent asks to run; it is also an approval.
type PermissionBody struct {
	ToolCall acp.ToolCallUpdate     `json:"toolCall"`
	Options  []acp.PermissionOption `json:"options"`
	// Outcome is the option chosen, empty while pending or when cancelled.
	Outcome string `json:"outcome,omitempty"`
}

// QuestionBody is a form the agent asks the user to fill in (ACP elicitation).
type QuestionBody struct {
	Message string          `json:"message"`
	Schema  json.RawMessage `json:"schema,omitempty" doc:"The form, as a restricted JSON Schema"`
	Action  string          `json:"action,omitempty" enum:"accept,decline,cancel" doc:"The answer's action, once answered"`
	Answer  json.RawMessage `json:"answer,omitempty" doc:"The submitted values, for accept"`
}

// MarkerBody is a line between items: why a turn ended, an undo, a handover.
type MarkerBody struct {
	Kind string `json:"kind" enum:"stopped,interrupted,error,limit,notice,compaction,terminal,undo"`
	Text string `json:"text"`
}

// Item is a chat item with its body decoded; exactly the field of its kind is set
// (text for agent and thought).
type Item struct {
	store.ChatItem
	User       *UserBody       `json:"user,omitempty"`
	Text       *TextBody       `json:"text,omitempty"`
	Tool       *ToolBody       `json:"tool,omitempty"`
	Plan       *PlanBody       `json:"plan,omitempty"`
	Permission *PermissionBody `json:"permission,omitempty"`
	Question   *QuestionBody   `json:"question,omitempty"`
	Marker     *MarkerBody     `json:"marker,omitempty"`
}

// newBody returns an empty body of kind.
func newBody(kind string) any {
	switch kind {
	case KindUser:
		return &UserBody{}
	case KindAgent, KindThought:
		return &TextBody{}
	case KindTool:
		return &ToolBody{}
	case KindPlan:
		return &PlanBody{}
	case KindPermission:
		return &PermissionBody{}
	case KindQuestion:
		return &QuestionBody{}
	default:
		return &MarkerBody{}
	}
}

// ItemView decodes a stored item.
func ItemView(it store.ChatItem) Item {
	v := Item{ChatItem: it}
	body := newBody(it.Kind)
	if json.Unmarshal(it.Body, body) != nil {
		return v
	}
	switch b := body.(type) {
	case *UserBody:
		v.User = b
	case *TextBody:
		v.Text = b
	case *ToolBody:
		v.Tool = b
	case *PlanBody:
		v.Plan = b
	case *PermissionBody:
		v.Permission = b
	case *QuestionBody:
		v.Question = b
	case *MarkerBody:
		v.Marker = b
	}
	return v
}

// encodeBody marshals a body within MaxBody, truncating text where it has to.
func encodeBody(body any) json.RawMessage {
	b, _ := json.Marshal(body)
	if len(b) <= MaxBody {
		return b
	}
	switch v := body.(type) {
	case *TextBody:
		c := *v
		c.Text, c.Truncated = clip(c.Text, MaxBody-1024), true
		return encodeBody(&c)
	case *ToolBody:
		c := *v
		c.Truncated = true
		if c.Input != nil {
			c.Input = nil
			return encodeBody(&c)
		}
		c.Content = shrinkContent(c.Content)
		if c.Content == nil {
			c.Locations = nil
			b, _ = json.Marshal(&c)
			return b
		}
		return encodeBody(&c)
	case *QuestionBody:
		c := *v
		c.Schema, c.Answer = nil, nil
		b, _ = json.Marshal(&c)
		return b
	}
	b, _ = json.Marshal(newBody(""))
	return b
}

// shrinkContent halves the longest text of tool content, or returns nil when nothing
// is left to cut.
func shrinkContent(in []acp.ToolCallContent) []acp.ToolCallContent {
	out := make([]acp.ToolCallContent, len(in))
	copy(out, in)
	longest, field := -1, 0
	size := func(c acp.ToolCallContent) (int, int) {
		best, f := 0, 0
		if c.Content != nil && len(c.Content.Text) > best {
			best, f = len(c.Content.Text), 1
		}
		if len(c.NewText) > best {
			best, f = len(c.NewText), 2
		}
		if c.OldText != nil && len(*c.OldText) > best {
			best, f = len(*c.OldText), 3
		}
		return best, f
	}
	max := 64
	for i, c := range out {
		if n, f := size(c); n > max {
			max, longest, field = n, i, f
		}
	}
	if longest < 0 {
		return nil
	}
	c := out[longest]
	switch field {
	case 1:
		block := *c.Content
		block.Text = clip(block.Text, len(block.Text)/2)
		c.Content = &block
	case 2:
		c.NewText = clip(c.NewText, len(c.NewText)/2)
	case 3:
		old := clip(*c.OldText, len(*c.OldText)/2)
		c.OldText = &old
	}
	out[longest] = c
	return out
}

// clip cuts s to at most n bytes on a rune boundary.
func clip(s string, n int) string {
	if len(s) <= n {
		return s
	}
	for n > 0 && !utf8.RuneStart(s[n]) {
		n--
	}
	return s[:n]
}
