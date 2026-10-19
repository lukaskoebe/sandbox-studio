package chat

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"reflect"
	"regexp"
	"strings"
	"unicode/utf8"

	"github.com/lukaskoebe/sandbox-studio/internal/acp"
	"github.com/lukaskoebe/sandbox-studio/internal/events"
	"github.com/lukaskoebe/sandbox-studio/internal/gateway"
	"github.com/lukaskoebe/sandbox-studio/internal/integrations"
	"github.com/lukaskoebe/sandbox-studio/internal/store"
)

// ApprovalKind is the approval kind of an agent's permission request.
const ApprovalKind = "chat.permission"

// Item statuses of permissions and questions.
const (
	StatusPending   = "pending"
	StatusApproved  = "approved"  // permission: allowed
	StatusDenied    = "denied"    // permission: rejected
	StatusDismissed = "dismissed" // permission: dismissed from the approval stack
	StatusAnswered  = "answered"  // question: accepted with an answer
	StatusDeclined  = "declined"  // question: declined
	StatusCancelled = "cancelled" // the request was cancelled or its turn ended
)

// MaxAnswer and MaxQuestionSchema cap the JSON sent to and accepted from form
// elicitations. Question schemas use a deliberately small subset documented below.
const (
	MaxAnswer              = 64 << 10
	MaxQuestionSchema      = 64 << 10
	MaxQuestionSchemaDepth = 16

	MaxPermissionOptions     = 32
	MaxPermissionOptionID    = 256
	MaxPermissionOptionName  = 256
	MaxPermissionRequestBody = 64 << 10
)

// PermissionPayload is a chat.permission approval's payload. It describes the tool call
// without file bodies: diffs are summarized as paths and line counts.
type PermissionPayload struct {
	ChatID       string                 `json:"chatId"`
	ChatTitle    string                 `json:"chatTitle"`
	ItemID       string                 `json:"itemId"`
	SandboxName  string                 `json:"sandboxName"`
	TeammateName string                 `json:"teammateName"`
	Title        string                 `json:"title" doc:"The tool call's title"`
	ToolKind     string                 `json:"toolKind,omitempty"`
	Locations    []acp.ToolCallLocation `json:"locations,omitempty"`
	Diffs        []DiffSummary          `json:"diffs,omitempty"`
	Options      []acp.PermissionOption `json:"options"`
}

// DiffSummary is one file a tool call would change.
type DiffSummary struct {
	Path    string `json:"path"`
	Added   int    `json:"added"`
	Removed int    `json:"removed"`
}

// ask is a permission request or question waiting for the user.
type ask struct {
	r      *run
	e      *entry
	perm   chan acp.RequestPermissionResponse
	elicit chan acp.ElicitationResponse
}

// ID, Routes and ApprovalKinds make the service an integration: it settles the
// chat.permission approvals it raises.
func (s *Service) ID() string                    { return "chat" }
func (s *Service) Routes() []gateway.VirtualHost { return nil }
func (s *Service) ApprovalKinds() []integrations.ApprovalKind {
	return []integrations.ApprovalKind{{Kind: ApprovalKind, Decide: s.decide}}
}

// Tools and Jobs: the chat service serves no MCP tools and runs no integration job.
func (s *Service) Tools() []integrations.Tool { return nil }
func (s *Service) Jobs() []integrations.Job   { return nil }

func (s *Service) notifyApprovals(envID string) {
	if s.Bus != nil {
		s.Bus.Publish(events.Event{Topic: events.TopicApprovals, EnvironmentID: envID})
	}
}

// RequestPermission shows the request as a permission item and a chat.permission
// approval, and waits until the user decides or the turn ends. There is no timeout: the
// agent is blocked, not a client that gives up.
func (l *live) RequestPermission(ctx context.Context, req acp.RequestPermissionRequest) (acp.RequestPermissionResponse, error) {
	tc := req.ToolCall
	tc.RawInput = nil
	if !validOptions(req.Options) || !permissionBodyWithinLimit(tc, req.Options) {
		l.s.log().Warn("chat: invalid or oversized permission request", "chat", l.chatID)
		return acp.Cancelled(), nil
	}
	l.mu.Lock()
	r := l.turn
	if r == nil || r.cancelled || r.ended || req.SessionID != l.session || len(req.Options) == 0 {
		l.mu.Unlock()
		return acp.Cancelled(), nil
	}
	e, ch := r.f.add(KindPermission, &PermissionBody{ToolCall: tc, Options: req.Options})
	e.status = StatusPending
	l.write(r, ch)
	a := &ask{r: r, e: e, perm: make(chan acp.RequestPermissionResponse, 1)}
	l.asks[e.id] = a
	if !e.saved {
		delete(l.asks, e.id)
		e.status = StatusCancelled
		e.dirty = true
		l.mu.Unlock()
		return acp.Cancelled(), nil
	}
	_, err := l.s.raise(ctx, l, r, e, req)
	if err != nil {
		_, settleErr := l.settleLocked(context.Background(), a, StatusCancelled, "", acp.ElicitCancel, nil, "")
		l.mu.Unlock()
		l.s.log().Warn("chat: permission approval", "chat", l.chatID, "err", err)
		if settleErr != nil {
			l.s.log().Warn("chat: settle failed permission request", "chat", l.chatID, "err", settleErr)
		}
		return acp.Cancelled(), nil
	}
	l.mu.Unlock()
	l.s.changed(l.envID, l.chatID, false)
	l.s.notifyApprovals(l.envID)
	select {
	case res := <-a.perm:
		return res, nil
	case <-ctx.Done():
		settled, err := l.settleAsk(context.Background(), a, StatusCancelled, "", acp.ElicitCancel, nil, store.StatusDismissed)
		if err != nil && !errors.Is(err, store.ErrNotFound) {
			l.s.log().Warn("chat: cancel permission request", "chat", l.chatID, "err", err)
		}
		if !settled {
			select {
			case res := <-a.perm:
				return res, nil
			default:
			}
		}
		return acp.Cancelled(), nil
	}
}

// raise stores the approval of a permission request.
func (s *Service) raise(ctx context.Context, l *live, r *run, e *entry, req acp.RequestPermissionRequest) (store.Approval, error) {
	c, err := s.Store.Chat(ctx, l.envID, l.chatID)
	if err != nil {
		return store.Approval{}, err
	}
	p := PermissionPayload{ChatID: c.ID, ChatTitle: c.Title, ItemID: e.id, Title: req.ToolCall.Title,
		ToolKind: req.ToolCall.Kind, Locations: req.ToolCall.Locations, Options: req.Options}
	if sb, err := s.Store.Sandbox(ctx, c.EnvironmentID, c.SandboxID); err == nil {
		p.SandboxName = sb.Name
	}
	if tm, err := s.Store.Teammate(ctx, c.EnvironmentID, c.TeammateID); err == nil {
		p.TeammateName = tm.Name
	}
	if p.Title == "" {
		p.Title = "Run a tool"
	}
	for _, tc := range req.ToolCall.Content {
		if tc.Type == "diff" && tc.Path != "" {
			d := DiffSummary{Path: tc.Path, Added: lines(tc.NewText)}
			if tc.OldText != nil {
				d.Removed = lines(*tc.OldText)
			}
			p.Diffs = append(p.Diffs, d)
		}
	}
	payload, _ := json.Marshal(p)
	it := store.ChatItem{ID: e.id, ChatID: c.ID, TurnID: r.turn.ID, Kind: e.kind, ToolCallID: e.toolCallID,
		Status: e.status, ApprovalID: e.approvalID, Body: encodeBody(e.body)}
	a, updated, _, err := s.Store.RequestApprovalAndUpdateChatItem(ctx, store.Approval{EnvironmentID: c.EnvironmentID, SandboxID: c.SandboxID,
		Kind: ApprovalKind, Subject: "chat/" + c.ID + "/" + e.id, Payload: payload}, it)
	if err == nil {
		e.approvalID, e.saved, e.dirty = updated.ApprovalID, true, false
	}
	return a, err
}

func lines(s string) int {
	if s == "" {
		return 0
	}
	return strings.Count(strings.TrimSuffix(s, "\n"), "\n") + 1
}

// decide settles a chat.permission approval and answers the agent with the option the
// decision maps to.
func (s *Service) decide(ctx context.Context, a store.Approval, d integrations.Decision) error {
	status := map[string]string{integrations.Allow: store.StatusApproved, integrations.Deny: store.StatusDenied,
		integrations.Dismiss: store.StatusDismissed}[d.Action]
	if status == "" {
		return integrations.ErrAction
	}
	var p PermissionPayload
	if err := json.Unmarshal(a.Payload, &p); err != nil {
		return err
	}
	if p.ChatID == "" || p.ItemID == "" || a.Subject != "chat/"+p.ChatID+"/"+p.ItemID || !validOptions(p.Options) {
		return fmt.Errorf("%w: the approval has invalid permission options", ErrInvalidDecision)
	}
	option := chooseOption(p.Options, d)
	if (option == "" && d.Action != integrations.Dismiss) || (option != "" && !optionIn(p.Options, option)) {
		return fmt.Errorf("%w: the agent offered no option for this decision", ErrInvalidDecision)
	}
	itemStatus := map[string]string{store.StatusApproved: StatusApproved, store.StatusDenied: StatusDenied,
		store.StatusDismissed: StatusDismissed}[status]
	if l := s.get(p.ChatID); l != nil {
		l.mu.Lock()
		q := l.asks[p.ItemID]
		if q != nil && q.perm != nil && q.r == l.turn && l.envID == a.EnvironmentID && q.e.approvalID == a.ID {
			if q.r.cancelled || q.r.ended {
				settled, err := l.settleLocked(context.Background(), q, StatusCancelled, "", acp.ElicitCancel, nil, store.StatusDismissed)
				l.mu.Unlock()
				if err != nil {
					return err
				}
				if settled {
					s.notifyApprovals(a.EnvironmentID)
				}
				return Conflict("the chat turn is stopping")
			}
			body, ok := q.e.body.(*PermissionBody)
			if !ok || !sameOptions(body.Options, p.Options) || (option != "" && !optionIn(body.Options, option)) {
				l.mu.Unlock()
				return fmt.Errorf("%w: the selected option is not on the permission item", ErrInvalidDecision)
			}
			settled, err := l.settleLocked(ctx, q, itemStatus, option, "", nil, status)
			l.mu.Unlock()
			if err != nil {
				return err
			}
			if !settled {
				return Conflict("the agent no longer waits for this permission")
			}
			s.notifyApprovals(a.EnvironmentID)
			return nil
		}
		l.mu.Unlock()
	}
	if err := s.dismissStalePermission(ctx, a, p); err != nil {
		return err
	}
	return Conflict("the agent no longer waits for this permission")
}

// chooseOption maps a decision to a supported option the agent offered. Empty means
// cancelled for Dismiss or no safe matching option for Allow/Deny.
func chooseOption(options []acp.PermissionOption, d integrations.Decision) string {
	switch d.Action {
	case integrations.Dismiss:
		return ""
	case integrations.Allow:
		if d.Remember {
			if id := optionOfKind(options, acp.AllowAlways); id != "" {
				return id
			}
		}
		// A one-time approval is the safe fallback for a remembered decision.
		// An unremembered Allow must never become persistent by fallback.
		return optionOfKind(options, acp.AllowOnce)
	case integrations.Deny:
		if d.Remember {
			if id := optionOfKind(options, acp.RejectAlways); id != "" {
				return id
			}
		}
		// RejectOnce is also the only safe fallback for a remembered Deny. A
		// one-time decision must never become persistent implicitly.
		return optionOfKind(options, acp.RejectOnce)
	default:
		return ""
	}
}

func optionOfKind(options []acp.PermissionOption, kind string) string {
	for _, option := range options {
		if option.Kind == kind {
			return option.OptionID
		}
	}
	return ""
}

func validOptions(options []acp.PermissionOption) bool {
	if len(options) == 0 || len(options) > MaxPermissionOptions {
		return false
	}
	seen := make(map[string]struct{}, len(options))
	for _, option := range options {
		if strings.TrimSpace(option.OptionID) == "" || len(option.OptionID) > MaxPermissionOptionID ||
			strings.TrimSpace(option.Name) == "" || len(option.Name) > MaxPermissionOptionName || !supportedPermissionKind(option.Kind) {
			return false
		}
		if _, ok := seen[option.OptionID]; ok {
			return false
		}
		seen[option.OptionID] = struct{}{}
	}
	return true
}

func supportedPermissionKind(kind string) bool {
	switch kind {
	case acp.AllowOnce, acp.AllowAlways, acp.RejectOnce, acp.RejectAlways:
		return true
	default:
		return false
	}
}

// permissionBodyWithinLimit rejects a request before creating an item if its sanitized
// tool-call metadata and complete option list would exceed the storage bound.
func permissionBodyWithinLimit(toolCall acp.ToolCallUpdate, options []acp.PermissionOption) bool {
	b, err := json.Marshal(&PermissionBody{ToolCall: toolCall, Options: options})
	return err == nil && len(b) <= MaxPermissionRequestBody
}

func optionIn(options []acp.PermissionOption, id string) bool {
	for _, option := range options {
		if option.OptionID == id {
			return true
		}
	}
	return false
}

func sameOptions(a, b []acp.PermissionOption) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// settleAsk serializes an asynchronous completion with user decisions and cancellation.
func (l *live) settleAsk(ctx context.Context, q *ask, status, option, action string, content json.RawMessage, approvalStatus string) (bool, error) {
	l.mu.Lock()
	if q.perm != nil && q.e.approvalID == "" {
		approvalStatus = ""
	}
	settled, err := l.settleLocked(ctx, q, status, option, action, content, approvalStatus)
	l.mu.Unlock()
	if settled && approvalStatus != "" {
		l.s.notifyApprovals(l.envID)
	}
	return settled, err
}

// settleLocked persists a permission or question outcome and then releases its waiter.
// The caller holds l.mu so competing decisions cannot split the approval and item state.
func (l *live) settleLocked(ctx context.Context, q *ask, status, option, action string, content json.RawMessage, approvalStatus string) (bool, error) {
	if l.asks[q.e.id] != q {
		return false, nil
	}
	var body any
	switch {
	case q.perm != nil:
		current, ok := q.e.body.(*PermissionBody)
		if !ok {
			return false, fmt.Errorf("chat: permission item has an invalid body")
		}
		updated := *current
		updated.Outcome = option
		body = &updated
	case q.elicit != nil:
		current, ok := q.e.body.(*QuestionBody)
		if !ok {
			return false, fmt.Errorf("chat: question item has an invalid body")
		}
		updated := *current
		updated.Action, updated.Answer = action, content
		body = &updated
	default:
		return false, fmt.Errorf("chat: ask has no response channel")
	}
	it := store.ChatItem{ID: q.e.id, ChatID: l.chatID, TurnID: q.r.turn.ID, Kind: q.e.kind,
		ToolCallID: q.e.toolCallID, Status: status, ApprovalID: q.e.approvalID, Body: encodeBody(body)}
	var updated store.ChatItem
	var err error
	if q.perm != nil && approvalStatus != "" {
		if q.e.approvalID == "" {
			return false, fmt.Errorf("chat: permission approval is not linked to its item")
		}
		updated, err = l.s.Store.DecideApprovalAndUpdateChatItem(ctx, l.envID, q.e.approvalID, approvalStatus, "", it)
	} else if q.e.saved {
		updated, err = l.s.Store.UpdateChatItem(ctx, it)
	} else {
		oldStatus, oldBody, oldDirty := q.e.status, q.e.body, q.e.dirty
		q.e.status, q.e.body, q.e.dirty = status, body, true
		err = l.s.save(q.r.turn, q.e)
		if err != nil {
			q.e.status, q.e.body, q.e.dirty = oldStatus, oldBody, oldDirty
		}
	}
	if err != nil {
		return false, err
	}
	q.e.status, q.e.body, q.e.dirty, q.e.saved = status, body, false, true
	if updated.ID != "" {
		q.e.approvalID = updated.ApprovalID
	}
	delete(l.asks, q.e.id)
	l.s.changed(l.envID, l.chatID, false)
	if q.perm != nil {
		res := acp.Cancelled()
		if option != "" && status != StatusCancelled {
			res = acp.Selected(option)
		}
		q.perm <- res
	}
	if q.elicit != nil {
		if status == StatusCancelled {
			action, content = acp.ElicitCancel, nil
		}
		q.elicit <- acp.ElicitationResponse{Action: action, Content: content}
	}
	return true, nil
}

func (s *Service) dismissStalePermission(ctx context.Context, a store.Approval, p PermissionPayload) error {
	_, chatErr := s.Store.Chat(ctx, a.EnvironmentID, p.ChatID)
	if errors.Is(chatErr, store.ErrNotFound) {
		chatErr = s.Store.DecideApproval(ctx, a.EnvironmentID, a.ID, store.StatusDismissed, "")
		if chatErr == nil {
			s.notifyApprovals(a.EnvironmentID)
		}
		return chatErr
	}
	if chatErr != nil {
		return chatErr
	}
	it, err := s.Store.ChatItem(ctx, p.ChatID, p.ItemID)
	if err == nil && it.Kind == KindPermission && (it.ApprovalID == "" || it.ApprovalID == a.ID) {
		if it.Status == StatusPending {
			it.Status = StatusCancelled
		}
		it.ApprovalID = a.ID
		_, err = s.Store.DecideApprovalAndUpdateChatItem(ctx, a.EnvironmentID, a.ID, store.StatusDismissed, "", it)
	} else if errors.Is(err, store.ErrNotFound) || err == nil {
		err = s.Store.DecideApproval(ctx, a.EnvironmentID, a.ID, store.StatusDismissed, "")
	}
	if err == nil {
		s.notifyApprovals(a.EnvironmentID)
		s.changed(a.EnvironmentID, p.ChatID, false)
	}
	return err
}

// CreateElicitation shows a form question and waits for the answer.
func (l *live) CreateElicitation(ctx context.Context, req acp.ElicitationRequest) (acp.ElicitationResponse, error) {
	if err := validateQuestionSchema(req.RequestedSchema); err != nil {
		l.s.log().Warn("chat: invalid question schema", "chat", l.chatID, "err", err)
		return acp.ElicitationResponse{Action: acp.ElicitCancel}, nil
	}
	l.mu.Lock()
	r := l.turn
	if r == nil || r.cancelled || r.ended || (req.SessionID != "" && req.SessionID != l.session) || (req.Mode != "" && req.Mode != "form") {
		l.mu.Unlock()
		return acp.ElicitationResponse{Action: acp.ElicitCancel}, nil
	}
	e, ch := r.f.add(KindQuestion, &QuestionBody{Message: clip(req.Message, 4096), Schema: req.RequestedSchema})
	e.status = StatusPending
	l.write(r, ch)
	if !e.saved {
		e.status = StatusCancelled
		e.dirty = true
		l.mu.Unlock()
		return acp.ElicitationResponse{Action: acp.ElicitCancel}, nil
	}
	a := &ask{r: r, e: e, elicit: make(chan acp.ElicitationResponse, 1)}
	l.asks[e.id] = a
	l.mu.Unlock()
	select {
	case res := <-a.elicit:
		return res, nil
	case <-ctx.Done():
		settled, err := l.settleAsk(context.Background(), a, StatusCancelled, "", acp.ElicitCancel, nil, "")
		if err != nil {
			l.s.log().Warn("chat: cancel question", "chat", l.chatID, "err", err)
		}
		if !settled {
			select {
			case res := <-a.elicit:
				return res, nil
			default:
			}
		}
		return acp.ElicitationResponse{Action: acp.ElicitCancel}, nil
	}
}

// Answer answers a question item: accept with content (a JSON object), decline or
// cancel.
func (s *Service) Answer(ctx context.Context, envID, chatID, itemID, action string, content json.RawMessage) error {
	if _, err := s.Store.Chat(ctx, envID, chatID); err != nil {
		return err
	}
	it, err := s.Store.ChatItem(ctx, chatID, itemID)
	if err != nil {
		return err
	}
	if it.Kind != KindQuestion {
		return fmt.Errorf("%w: the item is not a question", ErrInvalidAnswer)
	}
	if it.Status != StatusPending {
		return Conflict("the agent no longer waits for this answer")
	}
	status := map[string]string{acp.ElicitAccept: StatusAnswered, acp.ElicitDecline: StatusDeclined, acp.ElicitCancel: StatusCancelled}[action]
	if status == "" {
		return fmt.Errorf("%w: the action must be accept, decline or cancel", ErrInvalidAnswer)
	}
	if action != acp.ElicitAccept {
		content = nil
	} else if len(content) > MaxAnswer {
		return fmt.Errorf("%w: the answer is limited to %d KiB", ErrInvalidAnswer, MaxAnswer>>10)
	} else if !isObject(content) {
		return fmt.Errorf("%w: the answer must be a JSON object", ErrInvalidAnswer)
	} else {
		question := ItemView(it).Question
		if question == nil {
			return fmt.Errorf("%w: the question has an invalid body", ErrInvalidAnswer)
		}
		if err := validateAnswer(question.Schema, content); err != nil {
			return fmt.Errorf("%w: %v", ErrInvalidAnswer, err)
		}
	}
	l := s.get(chatID)
	if l == nil {
		return Conflict("the agent no longer waits for this answer")
	}
	l.mu.Lock()
	q := l.asks[itemID]
	if q == nil || q.elicit == nil || q.r == nil || q.r != l.turn || q.r.cancelled || q.r.ended || q.e.status != StatusPending {
		l.mu.Unlock()
		return Conflict("the agent no longer waits for this answer")
	}
	settled, err := l.settleLocked(ctx, q, status, "", action, content, "")
	l.mu.Unlock()
	if err != nil {
		return err
	}
	if !settled {
		return Conflict("the agent no longer waits for this answer")
	}
	return nil
}

func isObject(b json.RawMessage) bool {
	var v map[string]any
	return json.Unmarshal(b, &v) == nil && v != nil
}

// validateAnswer checks the JSON Schema subset used by ACP form elicitations.
func validateAnswer(schemaRaw, answerRaw json.RawMessage) error {
	if err := validateQuestionSchema(schemaRaw); err != nil {
		return err
	}
	if len(schemaRaw) == 0 || string(schemaRaw) == "null" {
		return nil
	}
	var schema, answer any
	if err := json.Unmarshal(schemaRaw, &schema); err != nil {
		return fmt.Errorf("the question schema is invalid")
	}
	root, ok := schema.(map[string]any)
	if !ok {
		return nil
	}
	if err := json.Unmarshal(answerRaw, &answer); err != nil {
		return fmt.Errorf("the answer is not valid JSON")
	}
	return validateSchemaValue(answer, root, "answer")
}

func validateQuestionSchema(schemaRaw json.RawMessage) error {
	if len(schemaRaw) == 0 || strings.TrimSpace(string(schemaRaw)) == "null" {
		return nil
	}
	if len(schemaRaw) > MaxQuestionSchema {
		return fmt.Errorf("the question schema exceeds %d KiB", MaxQuestionSchema>>10)
	}
	var schema any
	if err := json.Unmarshal(schemaRaw, &schema); err != nil {
		return fmt.Errorf("the question schema is invalid")
	}
	root, ok := schema.(map[string]any)
	if !ok {
		return fmt.Errorf("the question schema must be an object")
	}
	if err := validateSchemaDefinition(root, 1); err != nil {
		return fmt.Errorf("the question schema is invalid: %v", err)
	}
	return nil
}

// The enforced validation subset is type, properties, required, enum, const,
// additionalProperties, items, minLength, maxLength, pattern, minimum, maximum,
// exclusiveMinimum, exclusiveMaximum, minItems, maxItems, uniqueItems, minProperties
// and maxProperties. Annotation keywords $schema, $id, $comment, title, description,
// default, examples, deprecated, readOnly and writeOnly are retained but ignored.
// References, combinators and every other validation keyword are rejected; this is
// intentionally not a general JSON Schema implementation.
var questionSchemaKeywords = map[string]struct{}{
	"$schema": {}, "$id": {}, "$comment": {},
	"title": {}, "description": {}, "default": {}, "examples": {}, "deprecated": {}, "readOnly": {}, "writeOnly": {},
	"type": {}, "properties": {}, "required": {}, "enum": {}, "const": {}, "additionalProperties": {}, "items": {},
	"minLength": {}, "maxLength": {}, "pattern": {}, "minimum": {}, "maximum": {}, "exclusiveMinimum": {}, "exclusiveMaximum": {},
	"minItems": {}, "maxItems": {}, "uniqueItems": {}, "minProperties": {}, "maxProperties": {},
}

func validateSchemaDefinition(schema map[string]any, depth int) error {
	if depth > MaxQuestionSchemaDepth {
		return fmt.Errorf("schema nesting exceeds %d levels", MaxQuestionSchemaDepth)
	}
	for name := range schema {
		if _, ok := questionSchemaKeywords[name]; !ok {
			return fmt.Errorf("unsupported keyword %q", name)
		}
	}
	if rawType, ok := schema["type"]; ok {
		validType := func(t string) bool {
			switch t {
			case "object", "array", "string", "number", "integer", "boolean", "null":
				return true
			default:
				return false
			}
		}
		switch t := rawType.(type) {
		case string:
			if !validType(t) {
				return fmt.Errorf("unsupported type %q", t)
			}
		case []any:
			if len(t) == 0 {
				return fmt.Errorf("type list is empty")
			}
			for _, item := range t {
				name, ok := item.(string)
				if !ok || !validType(name) {
					return fmt.Errorf("invalid type list")
				}
			}
		default:
			return fmt.Errorf("invalid type")
		}
	}
	if raw, ok := schema["required"]; ok {
		list, ok := raw.([]any)
		if !ok {
			return fmt.Errorf("required must be an array")
		}
		for _, item := range list {
			if _, ok := item.(string); !ok {
				return fmt.Errorf("required entries must be strings")
			}
		}
	}
	if raw, ok := schema["enum"]; ok {
		if _, ok := raw.([]any); !ok {
			return fmt.Errorf("enum must be an array")
		}
	}
	if raw, ok := schema["properties"]; ok {
		properties, ok := raw.(map[string]any)
		if !ok {
			return fmt.Errorf("properties must be an object")
		}
		for _, rawChild := range properties {
			child, ok := rawChild.(map[string]any)
			if !ok {
				return fmt.Errorf("property schemas must be objects")
			}
			if err := validateSchemaDefinition(child, depth+1); err != nil {
				return err
			}
		}
	}
	if raw, ok := schema["items"]; ok {
		child, ok := raw.(map[string]any)
		if !ok {
			return fmt.Errorf("items must be an object")
		}
		if err := validateSchemaDefinition(child, depth+1); err != nil {
			return err
		}
	}
	if raw, ok := schema["additionalProperties"]; ok {
		switch additional := raw.(type) {
		case bool:
		case map[string]any:
			if err := validateSchemaDefinition(additional, depth+1); err != nil {
				return err
			}
		default:
			return fmt.Errorf("additionalProperties must be a boolean or object")
		}
	}
	for _, name := range []string{"minLength", "maxLength", "minItems", "maxItems", "minProperties", "maxProperties"} {
		if raw, ok := schema[name]; ok {
			n, ok := schemaNumber(raw)
			if !ok || n < 0 || math.Trunc(n) != n {
				return fmt.Errorf("%s must be a non-negative integer", name)
			}
		}
	}
	for _, name := range []string{"minimum", "maximum", "exclusiveMinimum", "exclusiveMaximum"} {
		if raw, ok := schema[name]; ok {
			if _, ok := schemaNumber(raw); !ok {
				return fmt.Errorf("%s must be a number", name)
			}
		}
	}
	if raw, ok := schema["uniqueItems"]; ok {
		if _, ok := raw.(bool); !ok {
			return fmt.Errorf("uniqueItems must be a boolean")
		}
	}
	if raw, ok := schema["pattern"]; ok {
		pattern, ok := raw.(string)
		if !ok {
			return fmt.Errorf("pattern must be a string")
		}
		if _, err := regexp.Compile(pattern); err != nil {
			return fmt.Errorf("pattern is invalid")
		}
	}
	return nil
}

func validateSchemaValue(value any, schema map[string]any, path string) error {
	if rawType, ok := schema["type"]; ok && !matchesSchemaType(value, rawType) {
		return fmt.Errorf("%s has the wrong type", path)
	}
	if raw, ok := schema["enum"]; ok {
		values, _ := raw.([]any)
		matched := false
		for _, candidate := range values {
			if reflect.DeepEqual(value, candidate) {
				matched = true
				break
			}
		}
		if !matched {
			return fmt.Errorf("%s is not an allowed value", path)
		}
	}
	if raw, ok := schema["const"]; ok && !reflect.DeepEqual(value, raw) {
		return fmt.Errorf("%s does not match the required value", path)
	}
	switch v := value.(type) {
	case map[string]any:
		if raw, ok := schema["required"].([]any); ok {
			for _, name := range raw {
				key := name.(string)
				if _, exists := v[key]; !exists {
					return fmt.Errorf("%s.%s is required", path, key)
				}
			}
		}
		properties, _ := schema["properties"].(map[string]any)
		if n, ok := schemaNumber(schema["minProperties"]); ok && float64(len(v)) < n {
			return fmt.Errorf("%s has too few fields", path)
		}
		if n, ok := schemaNumber(schema["maxProperties"]); ok && float64(len(v)) > n {
			return fmt.Errorf("%s has too many fields", path)
		}
		additional, hasAdditional := schema["additionalProperties"].(bool)
		additionalSchema, hasAdditionalSchema := schema["additionalProperties"].(map[string]any)
		for key, child := range v {
			rawChild, exists := properties[key]
			if exists {
				childSchema, ok := rawChild.(map[string]any)
				if ok {
					if err := validateSchemaValue(child, childSchema, path+"."+key); err != nil {
						return err
					}
				}
				continue
			}
			if hasAdditional && !additional && !hasAdditionalSchema {
				return fmt.Errorf("%s.%s is not an allowed field", path, key)
			}
			if hasAdditionalSchema {
				if err := validateSchemaValue(child, additionalSchema, path+"."+key); err != nil {
					return err
				}
			}
		}
	case []any:
		if itemSchema, ok := schema["items"].(map[string]any); ok {
			for i, child := range v {
				if err := validateSchemaValue(child, itemSchema, fmt.Sprintf("%s[%d]", path, i)); err != nil {
					return err
				}
			}
		}
		if n, ok := schemaNumber(schema["minItems"]); ok && float64(len(v)) < n {
			return fmt.Errorf("%s has too few items", path)
		}
		if n, ok := schemaNumber(schema["maxItems"]); ok && float64(len(v)) > n {
			return fmt.Errorf("%s has too many items", path)
		}
		if unique, _ := schema["uniqueItems"].(bool); unique {
			seen := make(map[string]struct{}, len(v))
			for _, item := range v {
				raw, _ := json.Marshal(item)
				if _, exists := seen[string(raw)]; exists {
					return fmt.Errorf("%s has duplicate items", path)
				}
				seen[string(raw)] = struct{}{}
			}
		}
	case string:
		length := float64(utf8.RuneCountInString(v))
		if n, ok := schemaNumber(schema["minLength"]); ok && length < n {
			return fmt.Errorf("%s is too short", path)
		}
		if n, ok := schemaNumber(schema["maxLength"]); ok && length > n {
			return fmt.Errorf("%s is too long", path)
		}
		if pattern, ok := schema["pattern"].(string); ok && !regexp.MustCompile(pattern).MatchString(v) {
			return fmt.Errorf("%s does not match the required pattern", path)
		}
	case float64:
		if n, ok := schemaNumber(schema["minimum"]); ok && v < n {
			return fmt.Errorf("%s is below the minimum", path)
		}
		if n, ok := schemaNumber(schema["maximum"]); ok && v > n {
			return fmt.Errorf("%s is above the maximum", path)
		}
		if n, ok := schemaNumber(schema["exclusiveMinimum"]); ok && v <= n {
			return fmt.Errorf("%s is not above the exclusive minimum", path)
		}
		if n, ok := schemaNumber(schema["exclusiveMaximum"]); ok && v >= n {
			return fmt.Errorf("%s is not below the exclusive maximum", path)
		}
	}
	return nil
}

func matchesSchemaType(value, rawType any) bool {
	if list, ok := rawType.([]any); ok {
		for _, item := range list {
			if matchesSchemaType(value, item) {
				return true
			}
		}
		return false
	}
	typ, ok := rawType.(string)
	if !ok {
		return false
	}
	switch typ {
	case "object":
		_, ok := value.(map[string]any)
		return ok
	case "array":
		_, ok := value.([]any)
		return ok
	case "string":
		_, ok := value.(string)
		return ok
	case "number":
		_, ok := value.(float64)
		return ok
	case "integer":
		n, ok := value.(float64)
		return ok && math.Trunc(n) == n
	case "boolean":
		_, ok := value.(bool)
		return ok
	case "null":
		return value == nil
	default:
		return false
	}
}

func schemaNumber(v any) (float64, bool) {
	n, ok := v.(float64)
	return n, ok
}

// dismissAsks settles the turn's open permission requests and questions as cancelled:
// their approvals are dismissed and the agent hears "cancelled".
func (l *live) dismissAsks(r *run, why string) {
	l.mu.Lock()
	var open []*ask
	for _, q := range l.asks {
		if q.r == r {
			open = append(open, q)
		}
	}
	l.mu.Unlock()
	for _, q := range open {
		approvalStatus := ""
		if q.perm != nil {
			approvalStatus = store.StatusDismissed
		}
		_, err := l.settleAsk(context.Background(), q, StatusCancelled, "", acp.ElicitCancel, nil, approvalStatus)
		if err != nil && !errors.Is(err, store.ErrNotFound) {
			l.s.log().Warn("chat: dismiss pending item", "chat", l.chatID, "why", why, "err", err)
		}
	}
}

// recoverApprovals dismisses chat.permission approvals left pending by a restart: no
// agent waits for them any more.
func (s *Service) recoverApprovals(ctx context.Context) error {
	const batch = 10000
	for {
		list, err := s.Store.ApprovalsByKind(ctx, ApprovalKind, store.StatusPending, batch)
		if err != nil {
			return err
		}
		for _, a := range list {
			err = nil
			var p PermissionPayload
			var it store.ChatItem
			linked := false
			if json.Unmarshal(a.Payload, &p) == nil && p.ChatID != "" && p.ItemID != "" && a.Subject == "chat/"+p.ChatID+"/"+p.ItemID {
				if _, err = s.Store.Chat(ctx, a.EnvironmentID, p.ChatID); err == nil {
					it, err = s.Store.ChatItem(ctx, p.ChatID, p.ItemID)
					if err == nil && it.Kind == KindPermission && (it.ApprovalID == "" || it.ApprovalID == a.ID) &&
						(it.Status == StatusPending || it.Status == StatusCancelled) {
						if it.Status == StatusPending {
							it.Status = StatusCancelled
						}
						it.ApprovalID = a.ID
						_, err = s.Store.DecideApprovalAndUpdateChatItem(ctx, a.EnvironmentID, a.ID, store.StatusDismissed, "", it)
						linked = err == nil
					} else if errors.Is(err, store.ErrNotFound) || err == nil {
						err = s.Store.DecideApproval(ctx, a.EnvironmentID, a.ID, store.StatusDismissed, "")
					}
				} else if errors.Is(err, store.ErrNotFound) {
					err = s.Store.DecideApproval(ctx, a.EnvironmentID, a.ID, store.StatusDismissed, "")
				}
			} else if errors.Is(err, store.ErrNotFound) || err == nil {
				err = s.Store.DecideApproval(ctx, a.EnvironmentID, a.ID, store.StatusDismissed, "")
			}
			if errors.Is(err, store.ErrNotFound) {
				continue
			}
			if err != nil {
				return err
			}
			s.notifyApprovals(a.EnvironmentID)
			if linked {
				s.changed(a.EnvironmentID, p.ChatID, false)
			}
		}
		if len(list) < batch {
			return nil
		}
	}
}

// settleOpenItems marks a turn's pending permissions and questions cancelled, and its open
// tool calls stopped.
func (s *Service) settleOpenItems(ctx context.Context, chatID, turnID string) error {
	items, err := s.Store.ChatItems(ctx, chatID, turnID)
	if err != nil {
		return err
	}
	for _, it := range items {
		if tool := ItemView(it).Tool; it.Kind == KindTool && tool != nil && toolOpen(tool.Status) {
			tool.Status, it.Status = ToolStopped, ToolStopped
			it.Body = encodeBody(tool)
			if _, err := s.Store.UpdateChatItem(ctx, it); err != nil {
				return err
			}
			continue
		}
		if it.Status == StatusPending && (it.Kind == KindPermission || it.Kind == KindQuestion) {
			it.Status = StatusCancelled
			if it.Kind == KindQuestion {
				if question := ItemView(it).Question; question != nil {
					question.Action, question.Answer = acp.ElicitCancel, nil
					it.Body = encodeBody(question)
				}
			}
			if _, err := s.Store.UpdateChatItem(ctx, it); err != nil {
				return err
			}
		}
	}
	return nil
}
