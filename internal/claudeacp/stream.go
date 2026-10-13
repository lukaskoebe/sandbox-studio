package claudeacp

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/lukaskoebe/sandbox-studio/internal/acp"
)

// The stream-json mapping. Claude Code prints one JSON message per line: system (init and
// compact_boundary), stream_event (the raw API events, with --include-partial-messages),
// assistant (one message per completed content block, all with the API message's id), user
// (tool results) and result (the end of a turn). Text and thinking stream as deltas and
// become chunks; the complete messages repeat them, so they are only shown where they didn't
// stream. A tool call is shown when its tool_use block completes, with its input, and its
// tool_result completes it. TodoWrite is shown as the plan instead of a tool call.
// Messages of subagents aren't shown.

// streamMessage is one line of Claude Code's stream-json output. Fields that vary by message
// type are kept raw and decoded where they are read, so a line is only dropped for its JSON.
type streamMessage struct {
	Type            string          `json:"type"`
	Subtype         string          `json:"subtype"`
	ParentToolUseID *string         `json:"parent_tool_use_id"`
	Message         json.RawMessage `json:"message"`
	Event           json.RawMessage `json:"event"`
	IsError         bool            `json:"is_error"`
	Result          json.RawMessage `json:"result"`
	Errors          json.RawMessage `json:"errors"`
	StopReason      string          `json:"stop_reason"`
	Usage           json.RawMessage `json:"usage"`
	TotalCostUSD    *float64        `json:"total_cost_usd"`
	MCPServers      []struct {
		Name   string `json:"name"`
		Status string `json:"status"`
	} `json:"mcp_servers"`
}

// contentBlock is a block of an API message, of an assistant or a user message.
type contentBlock struct {
	Type      string          `json:"type"`
	Text      string          `json:"text"`
	Thinking  string          `json:"thinking"`
	ID        string          `json:"id"`
	Name      string          `json:"name"`
	Input     json.RawMessage `json:"input"`
	ToolUseID string          `json:"tool_use_id"`
	Content   json.RawMessage `json:"content"`
	IsError   bool            `json:"is_error"`
}

// mapper turns the messages of one Claude Code process into session updates. It remembers
// what it showed: text that streamed isn't shown again from the complete message, and a
// tool result completes only a tool call that was shown.
type mapper struct {
	message  string          // the API message the stream events belong to
	streamed map[string]bool // "<message id>/text" and "<message id>/thinking" that streamed
	tools    map[string]bool // the tool calls shown
}

func newMapper() *mapper {
	return &mapper{streamed: map[string]bool{}, tools: map[string]bool{}}
}

// stream maps one raw API stream event.
func (m *mapper) stream(event json.RawMessage) []acp.SessionUpdate {
	var e struct {
		Type    string `json:"type"`
		Message struct {
			ID string `json:"id"`
		} `json:"message"`
		Delta struct {
			Type     string `json:"type"`
			Text     string `json:"text"`
			Thinking string `json:"thinking"`
		} `json:"delta"`
	}
	if json.Unmarshal(event, &e) != nil {
		return nil
	}
	switch {
	case e.Type == "message_start":
		m.message = e.Message.ID
	case e.Type == "content_block_delta" && e.Delta.Type == "text_delta" && e.Delta.Text != "":
		m.streamed[m.message+"/text"] = true
		return []acp.SessionUpdate{chunk(acp.UpdateAgentMessageChunk, e.Delta.Text)}
	case e.Type == "content_block_delta" && e.Delta.Type == "thinking_delta" && e.Delta.Thinking != "":
		m.streamed[m.message+"/thinking"] = true
		return []acp.SessionUpdate{chunk(acp.UpdateAgentThoughtChunk, e.Delta.Thinking)}
	}
	return nil
}

// assistant maps a complete message, which holds one content block.
func (m *mapper) assistant(raw json.RawMessage) []acp.SessionUpdate {
	var msg struct {
		ID      string         `json:"id"`
		Content []contentBlock `json:"content"`
	}
	if json.Unmarshal(raw, &msg) != nil {
		return nil
	}
	var out []acp.SessionUpdate
	for _, b := range msg.Content {
		switch b.Type {
		case "text":
			if b.Text != "" && !m.streamed[msg.ID+"/text"] {
				out = append(out, chunk(acp.UpdateAgentMessageChunk, b.Text))
			}
		case "thinking":
			if b.Thinking != "" && !m.streamed[msg.ID+"/thinking"] {
				out = append(out, chunk(acp.UpdateAgentThoughtChunk, b.Thinking))
			}
		case "tool_use":
			if b.Name == "TodoWrite" {
				out = append(out, planUpdate(b.Input))
			} else if !m.tools[b.ID] {
				m.tools[b.ID] = true
				title, kind, locations := toolCall(b.Name, b.Input)
				out = append(out, acp.SessionUpdate{Kind: acp.UpdateToolCall, ToolCallID: b.ID, Title: &title,
					ToolKind: kind, Status: "pending", Locations: locations, RawInput: b.Input})
			}
		}
	}
	return out
}

// user maps the tool results of a user message. A prompt sent as a string has none.
func (m *mapper) user(raw json.RawMessage) []acp.SessionUpdate {
	var msg struct {
		Content []contentBlock `json:"content"`
	}
	if json.Unmarshal(raw, &msg) != nil {
		return nil
	}
	var out []acp.SessionUpdate
	for _, b := range msg.Content {
		if b.Type != "tool_result" || !m.tools[b.ToolUseID] {
			continue
		}
		status := "completed"
		if b.IsError {
			status = "failed"
		}
		u := acp.SessionUpdate{Kind: acp.UpdateToolCallUpdate, ToolCallID: b.ToolUseID, Status: status}
		if text := resultText(b.Content); text != "" {
			u.Content, _ = json.Marshal([]acp.ToolCallContent{{Type: "content", Content: &acp.ContentBlock{Type: "text", Text: text}}})
		}
		out = append(out, u)
	}
	return out
}

// resultText is a tool result's text: a string, or the text blocks of a list.
func resultText(raw json.RawMessage) string {
	var s string
	if json.Unmarshal(raw, &s) == nil {
		return s
	}
	var blocks []contentBlock
	if json.Unmarshal(raw, &blocks) != nil {
		return ""
	}
	var parts []string
	for _, b := range blocks {
		if b.Type == "text" && b.Text != "" {
			parts = append(parts, b.Text)
		}
	}
	return strings.Join(parts, "\n")
}

// chunk is a text update of kind.
func chunk(kind, text string) acp.SessionUpdate {
	b, _ := json.Marshal(acp.ContentBlock{Type: "text", Text: text})
	return acp.SessionUpdate{Kind: kind, Content: b}
}

// planUpdate is the plan a TodoWrite call sets.
func planUpdate(input json.RawMessage) acp.SessionUpdate {
	var in struct {
		Todos []struct {
			Content string `json:"content"`
			Status  string `json:"status"`
		} `json:"todos"`
	}
	_ = json.Unmarshal(input, &in)
	entries := make([]acp.PlanEntry, 0, len(in.Todos))
	for _, t := range in.Todos {
		status := "pending"
		if t.Status == "in_progress" || t.Status == "completed" {
			status = t.Status
		}
		entries = append(entries, acp.PlanEntry{Content: t.Content, Priority: "medium", Status: status})
	}
	return acp.SessionUpdate{Kind: acp.UpdatePlan, Entries: entries}
}

// toolCall is a tool call's title, its ACP kind and the files it touches.
func toolCall(name string, input json.RawMessage) (title, kind string, locations []acp.ToolCallLocation) {
	var in struct {
		Command      string `json:"command"`
		Description  string `json:"description"`
		Prompt       string `json:"prompt"`
		FilePath     string `json:"file_path"`
		NotebookPath string `json:"notebook_path"`
		Path         string `json:"path"`
		Pattern      string `json:"pattern"`
		URL          string `json:"url"`
		Query        string `json:"query"`
	}
	_ = json.Unmarshal(input, &in)
	path := firstNonEmpty(in.FilePath, in.NotebookPath, in.Path)
	if path != "" {
		locations = []acp.ToolCallLocation{{Path: path}}
	}
	switch name {
	case "Bash":
		return titled("Run", firstNonEmpty(in.Description, in.Command)), "execute", nil
	case "Read":
		return titled("Read", path), "read", locations
	case "Edit", "MultiEdit", "NotebookEdit":
		return titled("Edit", path), "edit", locations
	case "Write":
		return titled("Write", path), "edit", locations
	case "Glob", "Grep":
		return titled("Search", in.Pattern), "search", locations
	case "WebFetch":
		return titled("Fetch", in.URL), "fetch", nil
	case "WebSearch":
		return titled("Search the web for", in.Query), "fetch", nil
	case "Task", "Agent":
		return titled("Agent:", firstNonEmpty(in.Description, in.Prompt)), "think", nil
	}
	return name, "other", locations
}

func titled(verb, arg string) string {
	return strings.TrimSpace(verb + " " + clip(arg, 200))
}

// claudeContent turns a prompt's ACP content into the content of a Claude Code user message.
// Text stays text, images of the types the API takes are images, PDFs are documents, and
// other embedded files are named in a text block that says they are not shown.
func claudeContent(blocks []acp.ContentBlock) ([]any, error) {
	out := make([]any, 0, len(blocks))
	for _, b := range blocks {
		switch b.Type {
		case "text":
			out = append(out, map[string]any{"type": "text", "text": b.Text})
		case "image":
			switch b.MimeType {
			case "image/png", "image/jpeg", "image/gif", "image/webp":
				out = append(out, map[string]any{"type": "image", "source": map[string]any{
					"type": "base64", "media_type": b.MimeType, "data": b.Data}})
			default:
				out = append(out, map[string]any{"type": "text", "text": fmt.Sprintf("An image (%s) isn't shown: only PNG, JPEG, GIF and WebP reach the agent.", b.MimeType)})
			}
		case "resource":
			var r struct {
				URI      string  `json:"uri"`
				MimeType string  `json:"mimeType"`
				Text     *string `json:"text"`
				Blob     string  `json:"blob"`
			}
			if err := json.Unmarshal(b.Resource, &r); err != nil {
				return nil, fmt.Errorf("invalid attachment: %w", err)
			}
			switch {
			case r.Text != nil:
				out = append(out, map[string]any{"type": "text", "text": "Attached file " + r.URI + ":\n\n" + *r.Text})
			case r.Blob != "" && r.MimeType == "application/pdf":
				out = append(out, map[string]any{"type": "document", "source": map[string]any{
					"type": "base64", "media_type": "application/pdf", "data": r.Blob}})
			default:
				out = append(out, map[string]any{"type": "text", "text": "Attached file " + r.URI + " (" + r.MimeType + ") isn't shown: only text and PDF files reach the agent."})
			}
		}
	}
	return out, nil
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if v != "" {
			return v
		}
	}
	return ""
}

// clip shortens s to at most n bytes, at a character boundary.
func clip(s string, n int) string {
	if len(s) <= n {
		return s
	}
	for n > 0 && s[n]&0xc0 == 0x80 {
		n--
	}
	return s[:n] + "..."
}
