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
	Mentions    []Mention    `json:"mentions,omitempty"`
}

// Mention is a file of the workspace the prompt names as @path, or @path:start-end for
// some of its lines. The agent gets a link to it and reads it itself.
type Mention struct {
	Path  string `json:"path" minLength:"1" maxLength:"4096" doc:"Relative to /workspace, slash-separated"`
	Start int    `json:"start,omitempty" minimum:"0" doc:"The first line mentioned; 0 for the whole file"`
	End   int    `json:"end,omitempty" minimum:"0" doc:"The last line mentioned; 0 for just start"`
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
	Status    string                 `json:"status,omitempty" enum:"pending,in_progress,completed,failed,stopped" doc:"stopped: the turn ended before the call did"`
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

// encodeBody marshals a body within MaxBody, truncating or dropping optional content
// with a strictly decreasing fallback for every body type.
func encodeBody(body any) json.RawMessage {
	if b, ok := marshalBody(body); ok {
		return b
	}
	switch v := body.(type) {
	case *TextBody:
		return encodeTextBody(v)
	case *ToolBody:
		return encodeToolBody(v)
	case *QuestionBody:
		return encodeQuestionBody(v)
	case *UserBody:
		return encodeUserBody(v)
	case *PlanBody:
		return encodePlanBody(v)
	case *PermissionBody:
		return encodePermissionBody(v)
	case *MarkerBody:
		return encodeMarkerBody(v)
	default:
		return json.RawMessage(`{"kind":"","text":""}`)
	}
}

func marshalBody(body any) (json.RawMessage, bool) {
	b, err := json.Marshal(body)
	return b, err == nil && len(b) <= MaxBody
}

func encodeTextBody(body *TextBody) json.RawMessage {
	c := *body
	c.Truncated = true
	for {
		if b, ok := marshalBody(&c); ok {
			return b
		}
		if c.Text == "" {
			b, _ := json.Marshal(&TextBody{Truncated: true})
			return b
		}
		c.Text = halveString(c.Text)
	}
}

func encodeToolBody(body *ToolBody) json.RawMessage {
	c := *body
	c.Truncated = true
	for {
		if b, ok := marshalBody(&c); ok {
			return b
		}
		if c.Input != nil {
			c.Input = nil
			continue
		}
		if content := shrinkContent(c.Content); content != nil {
			c.Content = content
			continue
		}
		if len(c.Content) != 0 {
			c.Content = nil
			continue
		}
		if len(c.Locations) != 0 {
			c.Locations = nil
			continue
		}
		if shrinkLongestString(&c.Title, &c.Kind, &c.Status) {
			continue
		}
		b, _ := json.Marshal(&ToolBody{Truncated: true})
		return b
	}
}

func encodeQuestionBody(body *QuestionBody) json.RawMessage {
	c := *body
	for {
		if b, ok := marshalBody(&c); ok {
			return b
		}
		if c.Answer != nil {
			c.Answer = nil
			continue
		}
		if c.Schema != nil {
			c.Schema = nil
			continue
		}
		if shrinkLongestString(&c.Message, &c.Action) {
			continue
		}
		b, _ := json.Marshal(&QuestionBody{})
		return b
	}
}

func encodeUserBody(body *UserBody) json.RawMessage {
	c := *body
	for {
		if b, ok := marshalBody(&c); ok {
			return b
		}
		if len(c.Attachments) != 0 {
			c.Attachments = c.Attachments[:len(c.Attachments)/2]
			continue
		}
		if len(c.Mentions) != 0 {
			c.Mentions = c.Mentions[:len(c.Mentions)/2]
			continue
		}
		if c.Text != "" {
			c.Text = halveString(c.Text)
			continue
		}
		b, _ := json.Marshal(&UserBody{})
		return b
	}
}

func encodePlanBody(body *PlanBody) json.RawMessage {
	c := *body
	for {
		if b, ok := marshalBody(&c); ok {
			return b
		}
		if len(c.Entries) != 0 {
			c.Entries = c.Entries[:len(c.Entries)/2]
			continue
		}
		b, _ := json.Marshal(&PlanBody{Entries: []acp.PlanEntry{}})
		return b
	}
}

func encodePermissionBody(body *PermissionBody) json.RawMessage {
	c := *body
	c.ToolCall.RawInput = nil
	for {
		if b, ok := marshalBody(&c); ok {
			return b
		}
		if content := shrinkContent(c.ToolCall.Content); content != nil {
			c.ToolCall.Content = content
			continue
		}
		if len(c.ToolCall.Content) != 0 {
			c.ToolCall.Content = nil
			continue
		}
		if len(c.ToolCall.Locations) != 0 {
			c.ToolCall.Locations = nil
			continue
		}
		if shrinkLongestString(&c.ToolCall.Title, &c.ToolCall.Kind, &c.ToolCall.Status, &c.ToolCall.ToolCallID, &c.Outcome) {
			continue
		}
		// RequestPermission bounds the full option list before saving, so this fallback
		// is only for malformed or legacy bodies whose options alone exceed MaxBody.
		if b, ok := marshalBody(&PermissionBody{Options: c.Options}); ok {
			return b
		}
		b, _ := json.Marshal(&PermissionBody{Options: []acp.PermissionOption{}})
		return b
	}
}

func encodeMarkerBody(body *MarkerBody) json.RawMessage {
	c := *body
	for {
		if b, ok := marshalBody(&c); ok {
			return b
		}
		if shrinkLongestString(&c.Text, &c.Kind) {
			continue
		}
		b, _ := json.Marshal(&MarkerBody{})
		return b
	}
}

func shrinkLongestString(values ...*string) bool {
	longest := -1
	for i, value := range values {
		if len(*value) > 0 && (longest < 0 || len(*value) > len(*values[longest])) {
			longest = i
		}
	}
	if longest < 0 {
		return false
	}
	*values[longest] = halveString(*values[longest])
	return true
}

func halveString(s string) string {
	if s == "" {
		return ""
	}
	return clip(s, len(s)/2)
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
