package chat

import (
	"context"
	"encoding/json"
	"errors"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/lukaskoebe/sandbox-studio/internal/acp"
	"github.com/lukaskoebe/sandbox-studio/internal/acp/acptest"
	"github.com/lukaskoebe/sandbox-studio/internal/integrations"
	"github.com/lukaskoebe/sandbox-studio/internal/store"
)

const permissionStep = `{"toolCall":{"toolCallId":"t1","title":"Edit main.go","kind":"edit",
	"content":[{"type":"diff","path":"/workspace/main.go","oldText":"a\nb\n","newText":"a\nc\nd\n"}],"rawInput":{"secret":"no"}},
	"options":[{"optionId":"a1","name":"Allow","kind":"allow_once"},{"optionId":"a2","name":"Always","kind":"allow_always"},
	{"optionId":"r1","name":"Reject","kind":"reject_once"}]}`

// pendingApproval waits for the chat's pending chat.permission approval.
func (f *fixture) pendingApproval(t *testing.T) store.Approval {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		list, err := f.s.Store.Approvals(context.Background(), f.sb.EnvironmentID, store.StatusPending, 10)
		if err != nil {
			t.Fatal(err)
		}
		for _, a := range list {
			if a.Kind == ApprovalKind {
				it, err := f.s.Store.ChatItemByApproval(context.Background(), a.ID)
				if err == nil && it.Status == StatusPending {
					return a
				}
			}
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("no permission approval")
	return store.Approval{}
}

func TestChatPermissionAllow(t *testing.T) {
	agent := &acptest.Agent{Turns: []acptest.Turn{{Steps: []acptest.Step{{Permission: json.RawMessage(permissionStep)}}}}}
	f := newFixture(t, agent)
	turn := f.send(t, "edit")
	a := f.pendingApproval(t)
	var p PermissionPayload
	if err := json.Unmarshal(a.Payload, &p); err != nil {
		t.Fatal(err)
	}
	if p.ChatID != f.chat.ID || p.Title != "Edit main.go" || p.SandboxName != "ada-dev" || p.TeammateName != "Ada" ||
		len(p.Diffs) != 1 || p.Diffs[0] != (DiffSummary{Path: "/workspace/main.go", Added: 3, Removed: 2}) || len(p.Options) != 3 {
		t.Fatalf("payload %+v", p)
	}
	if strings.Contains(string(a.Payload), "secret") || strings.Contains(string(a.Payload), "c\\nd") {
		t.Fatalf("the payload carries the tool's input or file bodies: %s", a.Payload)
	}
	k, ok := integrations.Set{f.s}.Kind(ApprovalKind)
	if !ok {
		t.Fatal("no chat.permission kind")
	}
	if err := k.Decide(context.Background(), a, integrations.Decision{Action: integrations.Allow, Remember: true}); err != nil {
		t.Fatal(err)
	}
	_, log := f.wait(t, turn.ID)
	_, outcomes, _ := agent.Snapshot()
	if len(outcomes) != 1 || !strings.Contains(string(outcomes[0]), `"optionId":"a2"`) {
		t.Fatalf("outcomes %s", outcomes)
	}
	it := log.Items[1]
	if it.Kind != KindPermission || it.Status != StatusApproved || it.ApprovalID != a.ID || it.Permission == nil ||
		len(it.Permission.Options) != 3 || it.Permission.Outcome != "a2" || it.Permission.ToolCall.RawInput != nil {
		t.Fatalf("item %+v %+v", it.ChatItem, it.Permission)
	}
	if got, _ := f.s.Store.Approval(context.Background(), f.sb.EnvironmentID, a.ID); got.Status != store.StatusApproved {
		t.Fatalf("approval %s", got.Status)
	}
}

// Stopping a turn dismisses its open approvals and tells the agent "cancelled".
func TestChatCancelDismissesPermission(t *testing.T) {
	agent := &acptest.Agent{Turns: []acptest.Turn{{Steps: []acptest.Step{{Permission: json.RawMessage(permissionStep)}, {WaitCancel: true}}}}}
	f := newFixture(t, agent)
	turn := f.send(t, "edit")
	a := f.pendingApproval(t)
	if err := f.s.Cancel(context.Background(), f.sb.EnvironmentID, f.chat.ID); err != nil {
		t.Fatal(err)
	}
	turn, log := f.wait(t, turn.ID)
	if turn.StopReason != acp.StopCancelled || kinds(log.Items) != "user,permission,marker:stopped" || log.Items[1].Status != StatusCancelled {
		t.Fatalf("turn %q items %s %s", turn.StopReason, kinds(log.Items), log.Items[1].Status)
	}
	if got, _ := f.s.Store.Approval(context.Background(), f.sb.EnvironmentID, a.ID); got.Status != store.StatusDismissed {
		t.Fatalf("approval %s", got.Status)
	}
	_, outcomes, _ := agent.Snapshot()
	if len(outcomes) != 1 || !strings.Contains(string(outcomes[0]), `"cancelled"`) {
		t.Fatalf("outcomes %s", outcomes)
	}
	// Deciding it afterwards is refused like any decided approval.
	k, _ := integrations.Set{f.s}.Kind(ApprovalKind)
	if err := k.Decide(context.Background(), a, integrations.Decision{Action: integrations.Allow}); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("late decision: %v", err)
	}
}

func TestChatPermissionAgentExitDismisses(t *testing.T) {
	agent := &acptest.Agent{Turns: []acptest.Turn{{Steps: []acptest.Step{{Permission: json.RawMessage(permissionStep)}}}}}
	f := newFixture(t, agent)
	turn := f.send(t, "edit")
	a := f.pendingApproval(t)
	f.g.killAgent()
	turn, log := f.wait(t, turn.ID)
	if turn.StopReason != StopInterrupted || log.Items[1].Status != StatusCancelled {
		t.Fatalf("turn %q item status %q", turn.StopReason, log.Items[1].Status)
	}
	got, err := f.s.Store.Approval(context.Background(), f.sb.EnvironmentID, a.ID)
	if err != nil || got.Status != store.StatusDismissed {
		t.Fatalf("approval %+v, err %v", got, err)
	}
	if log.Items[1].ApprovalID != a.ID {
		t.Fatalf("item approval id %q, want %q", log.Items[1].ApprovalID, a.ID)
	}
}

func TestChatPermissionDecisionCancelRace(t *testing.T) {
	agent := &acptest.Agent{Turns: []acptest.Turn{{Steps: []acptest.Step{{Permission: json.RawMessage(permissionStep)}, {WaitCancel: true}}}}}
	f := newFixture(t, agent)
	turn := f.send(t, "edit")
	a := f.pendingApproval(t)
	kind, ok := (integrations.Set{f.s}).Kind(ApprovalKind)
	if !ok {
		t.Fatal("no chat.permission kind")
	}
	start := make(chan struct{})
	var wg sync.WaitGroup
	decisionErr := make(chan error, 1)
	cancelErr := make(chan error, 1)
	wg.Add(2)
	go func() {
		defer wg.Done()
		<-start
		decisionErr <- kind.Decide(context.Background(), a, integrations.Decision{Action: integrations.Allow, Remember: true})
	}()
	go func() {
		defer wg.Done()
		<-start
		cancelErr <- f.s.Cancel(context.Background(), f.sb.EnvironmentID, f.chat.ID)
	}()
	close(start)
	wg.Wait()
	decErr, canErr := <-decisionErr, <-cancelErr
	if canErr != nil {
		t.Fatal(canErr)
	}
	if decErr != nil && !errors.Is(decErr, store.ErrNotFound) && !errors.Is(decErr, store.ErrConflict) {
		t.Fatalf("decision: %v", decErr)
	}
	_, log := f.wait(t, turn.ID)
	got, err := f.s.Store.Approval(context.Background(), f.sb.EnvironmentID, a.ID)
	if err != nil {
		t.Fatal(err)
	}
	it, err := f.s.Store.ChatItemByApproval(context.Background(), a.ID)
	if err != nil {
		t.Fatal(err)
	}
	item := ItemView(it)
	_, outcomes, _ := agent.Snapshot()
	switch got.Status {
	case store.StatusApproved:
		if it.Status != StatusApproved || item.Permission == nil || item.Permission.Outcome != "a2" ||
			len(outcomes) != 1 || !strings.Contains(string(outcomes[0]), `"optionId":"a2"`) {
			t.Fatalf("approval/item/outcome split: %s %+v %s", got.Status, item.Permission, outcomes)
		}
	case store.StatusDismissed:
		if it.Status != StatusCancelled || item.Permission == nil || item.Permission.Outcome != "" ||
			len(outcomes) != 1 || !strings.Contains(string(outcomes[0]), `"cancelled"`) {
			t.Fatalf("approval/item/outcome split: %s %+v %s", got.Status, item.Permission, outcomes)
		}
	default:
		t.Fatalf("approval status %q; log items %s", got.Status, kinds(log.Items))
	}
}

func TestChatPermissionDecisionWithoutOptionAndDismiss(t *testing.T) {
	permission := `{"toolCall":{"toolCallId":"t1","title":"Run","kind":"execute"},"options":[{"optionId":"allow","name":"Allow","kind":"allow_always"}]}`
	agent := &acptest.Agent{Turns: []acptest.Turn{{Steps: []acptest.Step{{Permission: json.RawMessage(permission)}}}}}
	f := newFixture(t, agent)
	turn := f.send(t, "run")
	a := f.pendingApproval(t)
	kind, _ := (integrations.Set{f.s}).Kind(ApprovalKind)
	if err := kind.Decide(context.Background(), a, integrations.Decision{Action: integrations.Allow}); !errors.Is(err, ErrInvalidDecision) {
		t.Fatalf("allow without a one-time option: %v", err)
	}
	if err := kind.Decide(context.Background(), a, integrations.Decision{Action: integrations.Deny}); !errors.Is(err, ErrInvalidDecision) {
		t.Fatalf("deny without a reject option: %v", err)
	}
	if got, _ := f.s.Store.Approval(context.Background(), f.sb.EnvironmentID, a.ID); got.Status != store.StatusPending {
		t.Fatalf("approval was consumed: %s", got.Status)
	}
	if err := kind.Decide(context.Background(), a, integrations.Decision{Action: integrations.Dismiss}); err != nil {
		t.Fatalf("dismiss without a reject option: %v", err)
	}
	if got, _ := f.s.Store.Approval(context.Background(), f.sb.EnvironmentID, a.ID); got.Status != store.StatusDismissed {
		t.Fatalf("dismiss left approval %s", got.Status)
	}
	if it, err := f.s.Store.ChatItemByApproval(context.Background(), a.ID); err != nil || it.Status != StatusDismissed || ItemView(it).Permission.Outcome != "" {
		t.Fatalf("dismiss item %+v, err %v", it, err)
	}
	f.wait(t, turn.ID)
}

func TestChatDismissNeverSelectsRejectOption(t *testing.T) {
	permission := `{"toolCall":{"toolCallId":"t1","title":"Run","kind":"execute"},"options":[{"optionId":"allow","name":"Allow","kind":"allow_once"},{"optionId":"reject-once","name":"Reject once","kind":"reject_once"},{"optionId":"reject-always","name":"Reject always","kind":"reject_always"}]}`
	agent := &acptest.Agent{Turns: []acptest.Turn{{Steps: []acptest.Step{{Permission: json.RawMessage(permission)}}}}}
	f := newFixture(t, agent)
	turn := f.send(t, "run")
	a := f.pendingApproval(t)
	kind, _ := (integrations.Set{f.s}).Kind(ApprovalKind)
	if err := kind.Decide(context.Background(), a, integrations.Decision{Action: integrations.Dismiss, Remember: true}); err != nil {
		t.Fatal(err)
	}
	if got, err := f.s.Store.Approval(context.Background(), f.sb.EnvironmentID, a.ID); err != nil || got.Status != store.StatusDismissed {
		t.Fatalf("dismissed approval %+v: %v", got, err)
	}
	it, err := f.s.Store.ChatItemByApproval(context.Background(), a.ID)
	if err != nil || it.Status != StatusDismissed || ItemView(it).Permission.Outcome != "" {
		t.Fatalf("dismissed item %+v: %v", it, err)
	}
	f.wait(t, turn.ID)
	_, outcomes, _ := agent.Snapshot()
	if len(outcomes) != 1 || !strings.Contains(string(outcomes[0]), `"cancelled"`) || strings.Contains(string(outcomes[0]), "optionId") {
		t.Fatalf("dismiss response selected a reject option: %s", outcomes)
	}
}

func TestChooseOption(t *testing.T) {
	all := []acp.PermissionOption{{OptionID: "ao", Kind: "allow_once"}, {OptionID: "aa", Kind: "allow_always"},
		{OptionID: "ro", Kind: "reject_once"}, {OptionID: "ra", Kind: "reject_always"}}
	onceOnly := []acp.PermissionOption{{OptionID: "ao", Kind: "allow_once"}, {OptionID: "ro", Kind: "reject_once"}}
	rejectAlwaysOnly := []acp.PermissionOption{{OptionID: "ra", Kind: acp.RejectAlways}}
	unknown := []acp.PermissionOption{{OptionID: "x", Kind: "allow_all_dangerous"}, {OptionID: "y", Kind: "reject_with_feedback"}}
	allowAlwaysOnly := []acp.PermissionOption{{OptionID: "plan", Name: "Always", Kind: acp.AllowAlways}}
	for _, tc := range []struct {
		options []acp.PermissionOption
		d       integrations.Decision
		want    string
	}{
		{all, integrations.Decision{Action: integrations.Allow}, "ao"},
		{all, integrations.Decision{Action: integrations.Allow, Remember: true}, "aa"},
		{all, integrations.Decision{Action: integrations.Deny}, "ro"},
		{all, integrations.Decision{Action: integrations.Deny, Remember: true}, "ra"},
		{all, integrations.Decision{Action: integrations.Dismiss, Remember: true}, ""},
		{onceOnly, integrations.Decision{Action: integrations.Allow, Remember: true}, "ao"},
		{onceOnly, integrations.Decision{Action: integrations.Deny, Remember: true}, "ro"},
		{rejectAlwaysOnly, integrations.Decision{Action: integrations.Deny}, ""},
		{rejectAlwaysOnly, integrations.Decision{Action: integrations.Deny, Remember: true}, "ra"},
		{allowAlwaysOnly, integrations.Decision{Action: integrations.Allow}, ""},
		{allowAlwaysOnly, integrations.Decision{Action: integrations.Allow, Remember: true}, "plan"},
		{unknown, integrations.Decision{Action: integrations.Allow}, ""},
		{unknown, integrations.Decision{Action: integrations.Deny}, ""},
		{unknown, integrations.Decision{Action: integrations.Dismiss}, ""},
	} {
		if got := chooseOption(tc.options, tc.d); got != tc.want {
			t.Errorf("%v %+v: %q, want %q", tc.options, tc.d, got, tc.want)
		}
		if got := chooseOption(tc.options, tc.d); got != "" && !optionIn(tc.options, got) {
			t.Errorf("%v %+v selected an unoffered option %q", tc.options, tc.d, got)
		}
	}
	if validOptions([]acp.PermissionOption{{OptionID: "x", Name: "Allow", Kind: acp.AllowOnce}, {OptionID: "x", Name: "Allow", Kind: acp.AllowOnce}}) ||
		validOptions([]acp.PermissionOption{{OptionID: " ", Name: "Allow", Kind: acp.AllowOnce}}) ||
		validOptions([]acp.PermissionOption{{OptionID: "x", Name: " ", Kind: acp.AllowOnce}}) ||
		validOptions([]acp.PermissionOption{{OptionID: strings.Repeat("x", MaxPermissionOptionID+1), Name: "Allow", Kind: acp.AllowOnce}}) ||
		validOptions([]acp.PermissionOption{{OptionID: "x", Name: strings.Repeat("x", MaxPermissionOptionName+1), Kind: acp.AllowOnce}}) ||
		validOptions(make([]acp.PermissionOption, MaxPermissionOptions+1)) {
		t.Fatal("invalid or overlarge permission options were accepted")
	}
}

func TestChatDenyWithoutRememberDoesNotChooseRejectAlways(t *testing.T) {
	permission := `{"toolCall":{"toolCallId":"t1","title":"Run","kind":"execute"},"options":[{"optionId":"allow","name":"Allow","kind":"allow_once"},{"optionId":"reject","name":"Reject always","kind":"reject_always"}]}`
	agent := &acptest.Agent{Turns: []acptest.Turn{{Steps: []acptest.Step{{Permission: json.RawMessage(permission)}}}}}
	f := newFixture(t, agent)
	turn := f.send(t, "run")
	a := f.pendingApproval(t)
	kind, _ := (integrations.Set{f.s}).Kind(ApprovalKind)
	if err := kind.Decide(context.Background(), a, integrations.Decision{Action: integrations.Deny}); !errors.Is(err, ErrInvalidDecision) {
		t.Fatalf("unremembered deny selected reject_always: %v", err)
	}
	if got, err := f.s.Store.Approval(context.Background(), f.sb.EnvironmentID, a.ID); err != nil || got.Status != store.StatusPending {
		t.Fatalf("invalid deny consumed approval: %+v, %v", got, err)
	}
	if err := kind.Decide(context.Background(), a, integrations.Decision{Action: integrations.Deny, Remember: true}); err != nil {
		t.Fatal(err)
	}
	_, log := f.wait(t, turn.ID)
	_, outcomes, _ := agent.Snapshot()
	if len(outcomes) != 1 || !strings.Contains(string(outcomes[0]), `"optionId":"reject"`) {
		t.Fatalf("remembered deny outcome %s", outcomes)
	}
	item := log.Items[1]
	if item.Status != StatusDenied || item.Permission == nil || item.Permission.Outcome != "reject" {
		t.Fatalf("denied item %+v", item)
	}
}

func TestChatPermissionBoundsRejectBeforeSaving(t *testing.T) {
	option := acp.PermissionOption{OptionID: "allow", Name: "Allow", Kind: acp.AllowOnce}
	encode := func(toolCall acp.ToolCallUpdate, options []acp.PermissionOption) json.RawMessage {
		b, _ := json.Marshal(struct {
			ToolCall acp.ToolCallUpdate     `json:"toolCall"`
			Options  []acp.PermissionOption `json:"options"`
		}{toolCall, options})
		return b
	}
	tooMany := make([]acp.PermissionOption, MaxPermissionOptions+1)
	for i := range tooMany {
		tooMany[i] = acp.PermissionOption{OptionID: "option-" + strconv.Itoa(i), Name: "Allow", Kind: acp.AllowOnce}
	}
	requests := []json.RawMessage{
		encode(acp.ToolCallUpdate{}, []acp.PermissionOption{{OptionID: strings.Repeat("i", MaxPermissionOptionID+1), Name: "Allow", Kind: acp.AllowOnce}}),
		encode(acp.ToolCallUpdate{}, []acp.PermissionOption{{OptionID: "allow", Name: strings.Repeat("n", MaxPermissionOptionName+1), Kind: acp.AllowOnce}}),
		encode(acp.ToolCallUpdate{}, tooMany),
		encode(acp.ToolCallUpdate{Content: []acp.ToolCallContent{{Type: "content", Content: &acp.ContentBlock{Type: "text", Text: strings.Repeat("x", MaxPermissionRequestBody)}}}}, []acp.PermissionOption{option}),
	}
	steps := make([]acptest.Step, len(requests))
	for i, request := range requests {
		steps[i] = acptest.Step{Permission: request}
	}
	agent := &acptest.Agent{Turns: []acptest.Turn{{Steps: steps}}}
	f := newFixture(t, agent)
	turn := f.send(t, "check request bounds")
	_, log := f.wait(t, turn.ID)
	for _, item := range log.Items {
		if item.Kind == KindPermission {
			t.Fatalf("rejected request left a permission item: %+v", item)
		}
	}
	_, outcomes, _ := agent.Snapshot()
	if len(outcomes) != len(requests) {
		t.Fatalf("permission outcomes %s", outcomes)
	}
	for _, outcome := range outcomes {
		if !strings.Contains(string(outcome), `"cancelled"`) {
			t.Fatalf("invalid permission was not cancelled: %s", outcome)
		}
	}
	if pending, err := f.s.Store.Approvals(context.Background(), f.sb.EnvironmentID, store.StatusPending, 10); err != nil || len(pending) != 0 {
		t.Fatalf("rejected request left approvals %+v: %v", pending, err)
	}
}

func TestChatRejectsInvalidQuestionSchemasBeforeSaving(t *testing.T) {
	deep := strings.Repeat(`{"type":"object","properties":{"nested":`, MaxQuestionSchemaDepth) +
		`{"type":"string"}` + strings.Repeat(`}}`, MaxQuestionSchemaDepth)
	large := `{"type":"object","description":"` + strings.Repeat("x", MaxQuestionSchema) + `"}`
	badSchemas := []json.RawMessage{
		json.RawMessage(large),
		json.RawMessage(deep),
		json.RawMessage(`{"type":"object","oneOf":[{"type":"string"}]}`),
		json.RawMessage(`{"type":"object","properties":{"value":{"$ref":"#/definitions/value"}}}`),
	}
	steps := make([]acptest.Step, len(badSchemas))
	for i, schema := range badSchemas {
		request, _ := json.Marshal(acp.ElicitationRequest{Mode: "form", Message: "Answer", RequestedSchema: schema})
		steps[i] = acptest.Step{Elicit: request}
	}
	agent := &acptest.Agent{Turns: []acptest.Turn{{Steps: steps}}}
	f := newFixture(t, agent)
	turn := f.send(t, "check schema bounds")
	_, log := f.wait(t, turn.ID)
	for _, item := range log.Items {
		if item.Kind == KindQuestion {
			t.Fatalf("rejected schema left a question item: %+v", item)
		}
	}
	_, _, answers := agent.Snapshot()
	if len(answers) != len(badSchemas) {
		t.Fatalf("elicitation answers %s", answers)
	}
	for _, answer := range answers {
		if !strings.Contains(string(answer), `"action":"cancel"`) {
			t.Fatalf("invalid schema was not cancelled: %s", answer)
		}
	}
}

func TestChatQuestion(t *testing.T) {
	agent := &acptest.Agent{Turns: []acptest.Turn{{Steps: []acptest.Step{{Elicit: json.RawMessage(
		`{"mode":"form","message":"Which database?","requestedSchema":{"type":"object","properties":{"db":{"type":"string","enum":["sqlite","postgres"]}},"required":["db"],"additionalProperties":false}}`)}}}}}
	f := newFixture(t, agent)
	ctx := context.Background()
	turn := f.send(t, "set up")
	var q store.ChatItem
	deadline := time.Now().Add(5 * time.Second)
	for q.ID == "" && time.Now().Before(deadline) {
		items, _ := f.s.Store.ChatItems(ctx, f.chat.ID, turn.ID)
		for _, it := range items {
			if it.Kind == KindQuestion {
				q = it
			}
		}
		time.Sleep(10 * time.Millisecond)
	}
	if q.Status != StatusPending || !strings.Contains(ItemView(q).Question.Message, "Which database?") {
		t.Fatalf("question %+v", q)
	}
	for _, invalid := range []json.RawMessage{
		json.RawMessage(`["sqlite"]`),
		json.RawMessage(`null`),
		json.RawMessage(`{"db":"mysql"}`),
		json.RawMessage(`{"db":7}`),
		json.RawMessage(`{}`),
		json.RawMessage(`{"db":"sqlite","extra":true}`),
	} {
		if err := f.s.Answer(ctx, f.sb.EnvironmentID, f.chat.ID, q.ID, "accept", invalid); !errors.Is(err, ErrInvalidAnswer) {
			t.Fatalf("invalid answer %s: %v", invalid, err)
		}
	}
	large := json.RawMessage(`{"db":"` + strings.Repeat("x", MaxAnswer) + `"}`)
	if err := f.s.Answer(ctx, f.sb.EnvironmentID, f.chat.ID, q.ID, "accept", large); !errors.Is(err, ErrInvalidAnswer) {
		t.Fatalf("oversized answer: %v", err)
	}
	if err := f.s.Answer(ctx, f.sb.EnvironmentID, f.chat.ID, q.ID, "unknown", nil); !errors.Is(err, ErrInvalidAnswer) {
		t.Fatalf("invalid action: %v", err)
	}
	if err := f.s.Answer(ctx, f.sb.EnvironmentID, f.chat.ID, q.ID, "accept", json.RawMessage(`{"db":"sqlite"}`)); err != nil {
		t.Fatal(err)
	}
	if err := f.s.Answer(ctx, f.sb.EnvironmentID, f.chat.ID, q.ID, "decline", nil); !errors.Is(err, store.ErrConflict) {
		t.Fatalf("second answer: %v", err)
	}
	_, log := f.wait(t, turn.ID)
	_, _, answers := agent.Snapshot()
	if len(answers) != 1 || string(answers[0]) != `{"action":"accept","content":{"db":"sqlite"}}` {
		t.Fatalf("answers %s", answers)
	}
	if it := log.Items[1]; it.Status != StatusAnswered || it.Question.Action != "accept" || string(it.Question.Answer) != `{"db":"sqlite"}` {
		t.Fatalf("item %+v %+v", it.ChatItem, it.Question)
	}
}

func TestChatCancelQuestion(t *testing.T) {
	agent := &acptest.Agent{Turns: []acptest.Turn{{Steps: []acptest.Step{{Elicit: json.RawMessage(
		`{"mode":"form","message":"Choose a database","requestedSchema":{"type":"object"}}`)}, {WaitCancel: true}}}}}
	f := newFixture(t, agent)
	turn := f.send(t, "set up")
	var question store.ChatItem
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) && question.ID == "" {
		items, _ := f.s.Store.ChatItems(context.Background(), f.chat.ID, turn.ID)
		for _, it := range items {
			if it.Kind == KindQuestion {
				question = it
				break
			}
		}
		time.Sleep(10 * time.Millisecond)
	}
	if question.ID == "" {
		t.Fatal("no question item")
	}
	if err := f.s.Cancel(context.Background(), f.sb.EnvironmentID, f.chat.ID); err != nil {
		t.Fatal(err)
	}
	_, log := f.wait(t, turn.ID)
	_, _, answers := agent.Snapshot()
	if log.Items[1].Status != StatusCancelled || log.Items[1].Question.Action != acp.ElicitCancel ||
		len(answers) != 1 || !strings.Contains(string(answers[0]), `"action":"cancel"`) {
		t.Fatalf("question %+v answers %s", log.Items[1], answers)
	}
}

// A restart dismisses the permissions no agent waits for any more.
func TestRecoverDismissesPermissions(t *testing.T) {
	f := newFixture(t, &acptest.Agent{})
	ctx := context.Background()
	turn, err := f.s.Store.StartChatTurn(ctx, f.chat.ID, store.OriginChat, "")
	if err != nil {
		t.Fatal(err)
	}
	itemBody, _ := json.Marshal(&PermissionBody{Options: []acp.PermissionOption{{OptionID: "allow", Kind: "allow_once"}}})
	it, err := f.s.Store.AddChatItem(ctx, store.ChatItem{ChatID: f.chat.ID, TurnID: turn.ID, Kind: KindPermission, Status: StatusPending, Body: itemBody})
	if err != nil {
		t.Fatal(err)
	}
	questionBody, _ := json.Marshal(&QuestionBody{Message: "Choose a database", Schema: json.RawMessage(`{"type":"object"}`)})
	question, err := f.s.Store.AddChatItem(ctx, store.ChatItem{ChatID: f.chat.ID, TurnID: turn.ID, Kind: KindQuestion, Status: StatusPending, Body: questionBody})
	if err != nil {
		t.Fatal(err)
	}
	payload, _ := json.Marshal(PermissionPayload{ChatID: f.chat.ID, ItemID: it.ID, Options: []acp.PermissionOption{{OptionID: "allow", Kind: "allow_once"}}})
	a, _, err := f.s.Store.RequestApproval(ctx, store.Approval{EnvironmentID: f.sb.EnvironmentID, SandboxID: f.sb.ID, Kind: ApprovalKind, Subject: "chat/" + f.chat.ID + "/" + it.ID, Payload: payload})
	if err != nil {
		t.Fatal(err)
	}
	if err := f.s.Recover(ctx); err != nil {
		t.Fatal(err)
	}
	if got, _ := f.s.Store.Approval(ctx, f.sb.EnvironmentID, a.ID); got.Status != store.StatusDismissed {
		t.Fatalf("approval %s", got.Status)
	}
	if got, _ := f.s.Store.ChatItem(ctx, f.chat.ID, it.ID); got.Status != StatusCancelled || got.ApprovalID != a.ID {
		t.Fatalf("item %+v", got)
	}
	if got, _ := f.s.Store.ChatItem(ctx, f.chat.ID, question.ID); got.Status != StatusCancelled || ItemView(got).Question.Action != acp.ElicitCancel {
		t.Fatalf("question %+v", got)
	}
}
