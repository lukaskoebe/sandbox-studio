package acp

import "encoding/json"

// ProtocolVersion is the ACP version Studio speaks. v2 is still in alpha.
const ProtocolVersion = 1

// Methods, as named by the v1 schema.
const (
	MethodInitialize        = "initialize"
	MethodSessionNew        = "session/new"
	MethodSessionLoad       = "session/load"
	MethodSessionResume     = "session/resume"
	MethodSessionPrompt     = "session/prompt"
	MethodSessionClose      = "session/close"
	MethodSessionCancel     = "session/cancel" // notification
	MethodSessionUpdate     = "session/update" // notification from the agent
	MethodRequestPermission = "session/request_permission"
	MethodElicitationCreate = "elicitation/create"
	// MethodSessionSteering puts a message into the running turn: an extension that agents
	// advertise in InitializeResponse.Meta.
	MethodSessionSteering = "_session/steering"
)

// Outcomes of _session/steering.
const (
	SteerInjected       = "injected"       // the message joined the running turn
	SteerStartedNewTurn = "startedNewTurn" // no turn ran; the agent started one of its own
	SteerPromptRequired = "promptRequired" // no turn ran; the client sends it as session/prompt
)

// Stop reasons of a prompt turn.
const (
	StopEndTurn         = "end_turn"
	StopMaxTokens       = "max_tokens"
	StopMaxTurnRequests = "max_turn_requests"
	StopRefusal         = "refusal"
	StopCancelled       = "cancelled"
)

// Permission option kinds.
const (
	AllowOnce    = "allow_once"
	AllowAlways  = "allow_always"
	RejectOnce   = "reject_once"
	RejectAlways = "reject_always"
)

// Session update kinds Studio folds into items. Others are ignored.
const (
	UpdateUserMessageChunk  = "user_message_chunk"
	UpdateAgentMessageChunk = "agent_message_chunk"
	UpdateAgentThoughtChunk = "agent_thought_chunk"
	UpdateToolCall          = "tool_call"
	UpdateToolCallUpdate    = "tool_call_update"
	UpdatePlan              = "plan"
	UpdateSessionInfo       = "session_info_update"
	UpdateUsage             = "usage_update"
	UpdateNotice            = "notice"
	UpdateCompaction        = "compaction_update"
	UpdateCompactionSummary = "compaction_summary_chunk"
)

// InitializeRequest is what Studio offers the agent. It advertises no fs, terminal or
// auth.terminal capability on purpose: the agent works on the sandbox's filesystem
// itself, and Studio never proxies files, commands or logins.
type InitializeRequest struct {
	ProtocolVersion    int                `json:"protocolVersion"`
	ClientCapabilities ClientCapabilities `json:"clientCapabilities"`
	ClientInfo         *Implementation    `json:"clientInfo,omitempty"`
}

// ClientCapabilities is the only capability Studio has: form elicitation.
type ClientCapabilities struct {
	Elicitation *ElicitationCapabilities `json:"elicitation,omitempty"`
}

// ElicitationCapabilities declares the elicitation modes the client renders.
type ElicitationCapabilities struct {
	Form *struct{} `json:"form,omitempty"`
}

// Implementation names a client or agent; the schema requires both fields.
type Implementation struct {
	Name    string `json:"name"`
	Version string `json:"version"`
}

// InitializeResponse is the agent's answer to initialize.
type InitializeResponse struct {
	ProtocolVersion   int               `json:"protocolVersion"`
	AgentCapabilities AgentCapabilities `json:"agentCapabilities"`
	AuthMethods       []json.RawMessage `json:"authMethods,omitempty"`
	AgentInfo         *Implementation   `json:"agentInfo,omitempty"`
	Meta              *InitializeMeta   `json:"_meta,omitempty"`
}

// InitializeMeta is the subset of the initialize response's _meta Studio reads.
type InitializeMeta struct {
	Steering struct {
		Supported bool `json:"supported"`
	} `json:"steering"`
}

// CanSteer reports whether the agent takes _session/steering.
func (r InitializeResponse) CanSteer() bool { return r.Meta != nil && r.Meta.Steering.Supported }

// AgentCapabilities is the subset Studio reads.
type AgentCapabilities struct {
	LoadSession         bool                `json:"loadSession,omitempty"`
	PromptCapabilities  PromptCapabilities  `json:"promptCapabilities"`
	SessionCapabilities SessionCapabilities `json:"sessionCapabilities"`
}

// PromptCapabilities lists the content the agent accepts in prompts besides text.
type PromptCapabilities struct {
	Image           bool `json:"image,omitempty"`
	Audio           bool `json:"audio,omitempty"`
	EmbeddedContext bool `json:"embeddedContext,omitempty"`
}

// SessionCapabilities are advertised as objects; present means supported.
type SessionCapabilities struct {
	Resume json.RawMessage `json:"resume,omitempty"`
	Close  json.RawMessage `json:"close,omitempty"`
	List   json.RawMessage `json:"list,omitempty"`
	Fork   json.RawMessage `json:"fork,omitempty"`
}

func present(raw json.RawMessage) bool {
	return len(raw) > 0 && string(raw) != "null" && string(raw) != "false"
}

// CanResume reports whether session/resume is supported.
func (c AgentCapabilities) CanResume() bool { return present(c.SessionCapabilities.Resume) }

// CanClose reports whether session/close is supported.
func (c AgentCapabilities) CanClose() bool { return present(c.SessionCapabilities.Close) }

// MCPServer is a stdio MCP server passed to a session. Env and Args are required arrays
// in the schema.
type MCPServer struct {
	Name    string        `json:"name"`
	Command string        `json:"command"`
	Args    []string      `json:"args"`
	Env     []EnvVariable `json:"env"`
}

// EnvVariable is one variable of an MCP server's environment.
type EnvVariable struct {
	Name  string `json:"name"`
	Value string `json:"value"`
}

// NewSessionRequest creates a session.
type NewSessionRequest struct {
	Cwd        string      `json:"cwd"`
	MCPServers []MCPServer `json:"mcpServers"`
}

// NewSessionResponse names the new session.
type NewSessionResponse struct {
	SessionID string `json:"sessionId"`
}

// LoadSessionRequest loads (and replays) or resumes (without replay) a session.
type LoadSessionRequest struct {
	SessionID  string      `json:"sessionId"`
	Cwd        string      `json:"cwd"`
	MCPServers []MCPServer `json:"mcpServers"`
}

// SessionRequest names a session, for session/close and session/cancel.
type SessionRequest struct {
	SessionID string `json:"sessionId"`
}

// PromptRequest is one user turn.
type PromptRequest struct {
	SessionID string         `json:"sessionId"`
	Prompt    []ContentBlock `json:"prompt"`
}

// SteerRequest is _session/steering's params.
type SteerRequest struct {
	SessionID string         `json:"sessionId"`
	Prompt    []ContentBlock `json:"prompt"`
	Meta      *SteerMeta     `json:"_meta,omitempty"`
}

// SteerMeta asks the agent what to do when no turn runs: promptRequired leaves the message
// to the client.
type SteerMeta struct {
	Steering struct {
		IdleBehavior string `json:"idleBehavior,omitempty"`
	} `json:"steering"`
}

// SteerResponse says what became of a steering message.
type SteerResponse struct {
	Outcome string `json:"outcome"`
	Reason  string `json:"reason,omitempty"`
}

// PromptResponse ends a turn.
type PromptResponse struct {
	StopReason string          `json:"stopReason"`
	Usage      json.RawMessage `json:"usage,omitempty"`
}

// ContentBlock is text, an image, a resource link or an embedded resource. Other fields
// of the schema are dropped.
type ContentBlock struct {
	Type     string          `json:"type"`
	Text     string          `json:"text,omitempty"`
	Data     string          `json:"data,omitempty"`
	MimeType string          `json:"mimeType,omitempty"`
	URI      string          `json:"uri,omitempty"`
	Name     string          `json:"name,omitempty"`
	Title    string          `json:"title,omitempty"`
	Size     int64           `json:"size,omitempty"`
	Resource json.RawMessage `json:"resource,omitempty"`
}

// SessionNotification carries one session update.
type SessionNotification struct {
	SessionID string        `json:"sessionId"`
	Update    SessionUpdate `json:"update"`
}

// SessionUpdate is the union of the update kinds Studio reads; Kind tells which fields
// are set. Unknown kinds keep only Kind.
type SessionUpdate struct {
	Kind string `json:"sessionUpdate"`

	// Content chunks.
	Content json.RawMessage `json:"content,omitempty"` // a ContentBlock for chunks, []ToolCallContent for tools
	// Tool calls.
	ToolCallID string             `json:"toolCallId,omitempty"`
	Title      *string            `json:"title,omitempty"` // tools, session_info_update
	ToolKind   string             `json:"kind,omitempty"`
	Status     string             `json:"status,omitempty"`
	Locations  []ToolCallLocation `json:"locations,omitempty"`
	RawInput   json.RawMessage    `json:"rawInput,omitempty"`
	RawOutput  json.RawMessage    `json:"rawOutput,omitempty"`
	// Plan.
	Entries []PlanEntry `json:"entries,omitempty"`
	// Usage.
	Used *int64          `json:"used,omitempty"`
	Size *int64          `json:"size,omitempty"`
	Cost json.RawMessage `json:"cost,omitempty"`
	// Notices.
	Message string `json:"message,omitempty"`
}

// Chunk decodes Content as a single content block.
func (u SessionUpdate) Chunk() (ContentBlock, bool) {
	var b ContentBlock
	if len(u.Content) == 0 || json.Unmarshal(u.Content, &b) != nil {
		return b, false
	}
	return b, true
}

// ToolContent decodes Content as tool call content.
func (u SessionUpdate) ToolContent() ([]ToolCallContent, bool) {
	var c []ToolCallContent
	if len(u.Content) == 0 || json.Unmarshal(u.Content, &c) != nil {
		return nil, false
	}
	return c, true
}

// ToolCallContent is content, a diff or a terminal reference produced by a tool call.
type ToolCallContent struct {
	Type       string        `json:"type"`
	Content    *ContentBlock `json:"content,omitempty"`
	Path       string        `json:"path,omitempty"`
	OldText    *string       `json:"oldText,omitempty"`
	NewText    string        `json:"newText,omitempty"`
	TerminalID string        `json:"terminalId,omitempty"`
}

// ToolCallLocation is a file a tool call touches.
type ToolCallLocation struct {
	Path string `json:"path"`
	Line *int   `json:"line,omitempty"`
}

// PlanEntry is one step of the agent's plan.
type PlanEntry struct {
	Content  string `json:"content"`
	Priority string `json:"priority"`
	Status   string `json:"status"`
}

// ToolCallUpdate is the tool call a permission request is about.
type ToolCallUpdate struct {
	ToolCallID string             `json:"toolCallId"`
	Title      string             `json:"title,omitempty"`
	Kind       string             `json:"kind,omitempty"`
	Status     string             `json:"status,omitempty"`
	Content    []ToolCallContent  `json:"content,omitempty"`
	Locations  []ToolCallLocation `json:"locations,omitempty"`
	RawInput   json.RawMessage    `json:"rawInput,omitempty"`
}

// PermissionOption is one answer the agent offers.
type PermissionOption struct {
	OptionID string `json:"optionId"`
	Name     string `json:"name"`
	Kind     string `json:"kind"`
}

// RequestPermissionRequest asks the user to allow a tool call.
type RequestPermissionRequest struct {
	SessionID string             `json:"sessionId"`
	ToolCall  ToolCallUpdate     `json:"toolCall"`
	Options   []PermissionOption `json:"options"`
}

// RequestPermissionResponse is the user's answer: an option or cancelled.
type RequestPermissionResponse struct {
	Outcome PermissionOutcome `json:"outcome"`
}

// PermissionOutcome is "selected" with an option ID or "cancelled".
type PermissionOutcome struct {
	Outcome  string `json:"outcome"`
	OptionID string `json:"optionId,omitempty"`
}

// Cancelled is the outcome of a request the turn's cancellation ended.
func Cancelled() RequestPermissionResponse {
	return RequestPermissionResponse{Outcome: PermissionOutcome{Outcome: "cancelled"}}
}

// Selected is the outcome of a chosen option.
func Selected(optionID string) RequestPermissionResponse {
	return RequestPermissionResponse{Outcome: PermissionOutcome{Outcome: "selected", OptionID: optionID}}
}

// ElicitationRequest asks the user to fill in a form (mode "form"). The schema is a
// restricted JSON Schema, kept as is for the UI.
type ElicitationRequest struct {
	SessionID       string          `json:"sessionId,omitempty"`
	Mode            string          `json:"mode,omitempty"`
	Message         string          `json:"message"`
	RequestedSchema json.RawMessage `json:"requestedSchema,omitempty"`
}

// Elicitation actions.
const (
	ElicitAccept  = "accept"
	ElicitDecline = "decline"
	ElicitCancel  = "cancel"
)

// ElicitationResponse is the user's answer to a form.
type ElicitationResponse struct {
	Action  string          `json:"action"`
	Content json.RawMessage `json:"content,omitempty"`
}
