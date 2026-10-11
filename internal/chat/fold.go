package chat

import (
	"encoding/json"
	"strings"

	"github.com/lukaskoebe/sandbox-studio/internal/acp"
)

// entry is an item while its turn runs: the folder changes its body in place and the
// service writes it to the store.
type entry struct {
	id         string
	kind       string
	toolCallID string
	status     string
	body       any
	saved      bool // inserted into the store
	dirty      bool // changed since it was last written
}

// change is what an update did to an entry.
type change struct {
	e     *entry
	ended bool // the entry won't change again from updates: write it now
}

// folder turns a session's updates into items. It keeps one open text item that chunks
// of the same kind append to; anything else closes it.
type folder struct {
	newID   func() string
	users   bool // fold user_message_chunk too (replays); live turns add the prompt themselves
	entries []*entry
	open    *entry // the text item chunks append to
	tools   map[string]*entry
	plan    *entry
	title   string // from session_info_update, empty if none
	usage   TurnUsage
}

// TurnUsage is what a turn cost, as far as the agent reports it.
type TurnUsage struct {
	Tokens      json.RawMessage `json:"tokens,omitempty" doc:"The prompt response's usage, as the agent reports it"`
	ContextUsed *int64          `json:"contextUsed,omitempty" doc:"Tokens in the context window"`
	ContextSize *int64          `json:"contextSize,omitempty" doc:"Size of the context window"`
	Cost        json.RawMessage `json:"cost,omitempty" doc:"The session's cost so far, as the agent reports it"`
}

func newFolder(newID func() string) *folder {
	return &folder{newID: newID, tools: map[string]*entry{}}
}

// add appends a new entry, closing the open text item.
func (f *folder) add(kind string, body any) (*entry, []change) {
	out := f.close()
	e := &entry{id: f.newID(), kind: kind, body: body, dirty: true}
	f.entries = append(f.entries, e)
	return e, append(out, change{e: e})
}

// close ends the open text item.
func (f *folder) close() []change {
	if f.open == nil {
		return nil
	}
	e := f.open
	f.open = nil
	return []change{{e: e, ended: true}}
}

// finish ends every entry, at the end of the turn.
func (f *folder) finish() []change {
	f.open = nil
	out := make([]change, 0, len(f.entries))
	for _, e := range f.entries {
		out = append(out, change{e: e, ended: true})
	}
	return out
}

// apply folds one update and reports the entries it changed.
func (f *folder) apply(u acp.SessionUpdate) []change {
	switch u.Kind {
	case acp.UpdateUserMessageChunk:
		if !f.users {
			return nil
		}
		return f.text(KindUser, u)
	case acp.UpdateAgentMessageChunk:
		return f.text(KindAgent, u)
	case acp.UpdateAgentThoughtChunk:
		return f.text(KindThought, u)
	case acp.UpdateToolCall, acp.UpdateToolCallUpdate:
		return f.tool(u)
	case acp.UpdatePlan:
		if f.plan != nil {
			f.plan.body.(*PlanBody).Entries = u.Entries
			f.plan.dirty = true
			return []change{{e: f.plan}}
		}
		e, out := f.add(KindPlan, &PlanBody{Entries: nonNilEntries(u.Entries)})
		f.plan = e
		return out
	case acp.UpdateSessionInfo:
		if u.Title != nil {
			f.title = strings.TrimSpace(*u.Title)
		}
	case acp.UpdateUsage:
		if u.Used != nil {
			f.usage.ContextUsed = u.Used
		}
		if u.Size != nil {
			f.usage.ContextSize = u.Size
		}
		if len(u.Cost) > 0 {
			f.usage.Cost = u.Cost
		}
	case acp.UpdateNotice:
		if u.Message != "" {
			_, out := f.add(KindMarker, &MarkerBody{Kind: MarkerNotice, Text: clip(u.Message, 4096)})
			return out
		}
	case acp.UpdateCompaction:
		if u.Status == "" || u.Status == "completed" {
			_, out := f.add(KindMarker, &MarkerBody{Kind: MarkerCompaction, Text: "The agent summarized the conversation to make room"})
			return out
		}
	}
	return nil
}

// text appends a chunk to the open text item of kind, or starts one.
func (f *folder) text(kind string, u acp.SessionUpdate) []change {
	block, ok := u.Chunk()
	if !ok {
		return nil
	}
	s := chunkText(block)
	if s == "" {
		return nil
	}
	var out []change
	if f.open == nil || f.open.kind != kind {
		var body any = &TextBody{}
		if kind == KindUser {
			body = &UserBody{}
		}
		var e *entry
		e, out = f.add(kind, body)
		f.open = e
	}
	e := f.open
	switch b := e.body.(type) {
	case *TextBody:
		if b.Truncated {
			return out
		}
		if len(b.Text)+len(s) > MaxBody {
			s, b.Truncated = clip(s, MaxBody-len(b.Text)), true
		}
		b.Text += s
	case *UserBody:
		if len(b.Text)+len(s) <= MaxBody {
			b.Text += s
		}
	}
	e.dirty = true
	if len(out) == 0 {
		out = []change{{e: e}}
	}
	return out
}

// chunkText is what a content chunk shows: its text, or a link for resources.
func chunkText(b acp.ContentBlock) string {
	switch b.Type {
	case "text":
		return b.Text
	case "resource_link":
		name := b.Title
		if name == "" {
			name = b.Name
		}
		return "[" + name + "](" + b.URI + ")"
	case "image":
		return "[image]"
	}
	return ""
}

// tool folds tool_call and tool_call_update. Fields an update leaves out keep their
// values; content and locations are replaced whole, as ACP specifies.
func (f *folder) tool(u acp.SessionUpdate) []change {
	if u.ToolCallID == "" {
		return nil
	}
	e, known := f.tools[u.ToolCallID]
	var out []change
	if !known {
		e, out = f.add(KindTool, &ToolBody{Status: "pending"})
		e.toolCallID = u.ToolCallID
		f.tools[u.ToolCallID] = e
	}
	b := e.body.(*ToolBody)
	if u.Title != nil {
		b.Title = *u.Title
	}
	if u.ToolKind != "" {
		b.Kind = u.ToolKind
	}
	if u.Status != "" {
		b.Status = u.Status
	}
	if c, ok := u.ToolContent(); ok {
		b.Content = c
	}
	if u.Locations != nil {
		b.Locations = u.Locations
	}
	if len(u.RawInput) > 0 {
		b.Input = u.RawInput
	}
	e.status = b.Status
	e.dirty = true
	ended := b.Status == "completed" || b.Status == "failed"
	if known {
		return []change{{e: e, ended: ended}}
	}
	if ended {
		out[len(out)-1].ended = true
	}
	return out
}

func nonNilEntries(e []acp.PlanEntry) []acp.PlanEntry {
	if e == nil {
		return []acp.PlanEntry{}
	}
	return e
}
