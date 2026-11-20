package lead

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/lukaskoebe/sandbox-studio/internal/chat"
	"github.com/lukaskoebe/sandbox-studio/internal/integrations"
	"github.com/lukaskoebe/sandbox-studio/internal/store"
)

// messenger records Lead's messages. A message to the fixture's chat starts a turn there at
// once, and Ada answers it unless quiet is set; then open is the turn, for the test to end.
type messenger struct {
	f     *fixture
	t     *testing.T
	mu    sync.Mutex
	sent  []string // chat: text
	from  []chat.FromLead
	quiet bool
	open  string
	err   error
}

func (m *messenger) Queue(_ context.Context, _ string, chatID, text string, from chat.FromLead, sent func()) error {
	m.mu.Lock()
	if m.err != nil {
		m.mu.Unlock()
		return m.err
	}
	m.sent, m.from = append(m.sent, chatID+": "+text), append(m.from, from)
	quiet := m.quiet
	m.mu.Unlock()
	ctx := context.Background()
	turn, err := m.f.st.StartChatTurn(ctx, chatID, "chat", "")
	if err != nil {
		return err
	}
	body, _ := json.Marshal(chat.UserBody{Text: text, Lead: &from})
	if _, err := m.f.st.AddChatItem(ctx, store.ChatItem{ChatID: chatID, TurnID: turn.ID, Kind: chat.KindUser, Body: body}); err != nil {
		return err
	}
	sent()
	if quiet {
		m.mu.Lock()
		m.open = turn.ID
		m.mu.Unlock()
		return nil
	}
	m.f.answer(m.t, turn.ID, "The coupon tests pass on main.")
	return nil
}

// answer ends a turn of the fixture's chat with Ada's answer.
func (f *fixture) answer(t *testing.T, turnID, text string) {
	t.Helper()
	ctx := context.Background()
	if _, err := f.st.AddChatItem(ctx, store.ChatItem{ChatID: f.chat.ID, TurnID: turnID, Kind: chat.KindAgent, Body: json.RawMessage(`{"text":` + quote(text) + `}`)}); err != nil {
		t.Fatal(err)
	}
	if _, err := f.st.EndChatTurn(ctx, f.chat.ID, turnID, "end_turn", nil); err != nil {
		t.Fatal(err)
	}
}

func (m *messenger) Start(_ context.Context, _ string, sandboxID, title, text string, from chat.FromLead) (string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.err != nil {
		return "", m.err
	}
	m.sent, m.from = append(m.sent, "new "+sandboxID+" "+title+": "+text), append(m.from, from)
	return "chat-new", nil
}

func (m *messenger) calls() []string {
	m.mu.Lock()
	defer m.mu.Unlock()
	return append([]string(nil), m.sent...)
}

func planFixture(t *testing.T) (*fixture, *messenger) {
	f := newFixture(t)
	m := &messenger{f: f, t: t}
	f.svc.Send = m
	f.svc.PlanPoll = 5 * time.Millisecond
	return f, m
}

func (f *fixture) draft(t *testing.T, text string) LeadPlanView {
	t.Helper()
	p, err := f.svc.NewPlan(context.Background(), f.env, text)
	if err != nil {
		t.Fatal(err)
	}
	f.svc.plans.Wait()
	p, err = f.svc.Plan(context.Background(), f.env, p.ID)
	if err != nil {
		t.Fatal(err)
	}
	return p
}

func (f *fixture) decide(t *testing.T, approvalID, action string) error {
	t.Helper()
	a, err := f.st.Approval(context.Background(), f.env, approvalID)
	if err != nil {
		t.Fatal(err)
	}
	return f.svc.decide(context.Background(), a, integrations.Decision{Action: action})
}

const twoSteps = `{"reply": "I'll ask Ada to run the tests.", "title": "Coupon tests", "steps": [
 {"kind": "message", "thread": "th1", "text": "Run the coupon tests on main and tell me how they do. My key is sk-ant-api03-` + "xxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxx" + `."},
 {"kind": "message", "thread": "th9", "text": "A thread that isn't there."},
 {"kind": "start", "teammate": "tm1", "computer": "pc7", "title": "Elsewhere", "text": "A computer that isn't hers."}]}`

func TestPlanSendsNothingUntilApproved(t *testing.T) {
	f, m := planFixture(t)
	ctx := context.Background()
	f.turn(t, "Write the coupon importer", "Done.", 1)
	f.llm.plan = "```json\n" + twoSteps + "\n```"
	f.llm.report = "Ada says the coupon tests pass on main."

	p := f.draft(t, "  Have Ada check the coupon tests  ")
	if p.State != store.LeadPlanProposed || p.Title != "Coupon tests" || len(p.Steps) != 1 || p.ApprovalID == "" {
		t.Fatalf("plan = %+v", p)
	}
	st := p.Steps[0]
	if st.Kind != StepMessage || st.ChatID != f.chat.ID || st.Thread != "Coupon import" || st.Teammate != "Ada" || st.Computer != "ada-shop" ||
		st.State != StepProposed || strings.Contains(st.Text, "sk-ant") {
		t.Errorf("step = %+v", st)
	}
	if len(p.Items) != 2 || p.Items[0].Kind != ItemYou || p.Items[0].Text != "Have Ada check the coupon tests" ||
		p.Items[1].Kind != ItemLead || !strings.Contains(p.Items[1].Text, "left out 2 step(s)") || len(p.Items[1].Steps) != 1 || p.Items[1].Cost == 0 {
		t.Errorf("items = %+v", p.Items)
	}
	a, err := f.st.Approval(ctx, f.env, p.ApprovalID)
	if err != nil || a.Kind != ApprovalKind || a.Status != store.StatusPending || a.Subject != p.ID {
		t.Fatalf("approval = %+v, %v", a, err)
	}
	var payload PlanPayload
	if err := json.Unmarshal(a.Payload, &payload); err != nil || payload.Lead != "Lead" || len(payload.Steps) != 1 || payload.Digest != digest(p.Steps) {
		t.Errorf("payload = %+v, %v", payload, err)
	}
	if calls := m.calls(); len(calls) != 0 {
		t.Fatalf("sent before approval: %v", calls)
	}

	if err := f.decide(t, p.ApprovalID, integrations.Allow); err != nil {
		t.Fatal(err)
	}
	f.svc.plans.Wait()
	calls := m.calls()
	if len(calls) != 1 || !strings.HasPrefix(calls[0], f.chat.ID+": Run the coupon tests") || m.from[0].PlanID != p.ID ||
		m.from[0].Name != "Lead" || m.from[0].Plan != "Coupon tests" || m.from[0].ApprovedAt.IsZero() {
		t.Fatalf("sent = %v from %+v", calls, m.from)
	}
	p, err = f.svc.Plan(ctx, f.env, p.ID)
	if err != nil {
		t.Fatal(err)
	}
	if p.State != store.LeadPlanDone || p.ApprovedAt == nil || len(p.Steps) != 3 {
		t.Fatalf("plan after = %+v", p)
	}
	for _, st := range p.Steps {
		if st.State != StepDone {
			t.Errorf("step %s = %s (%s)", st.Kind, st.State, st.Note)
		}
	}
	last := p.Items[len(p.Items)-1]
	if last.Kind != ItemReport || last.Text != "Ada says the coupon tests pass on main." || len(last.Threads) != 1 ||
		last.Threads[0].ChatID != f.chat.ID || !last.Threads[0].Answered {
		t.Errorf("report = %+v", last)
	}
	if a, _ := f.st.Approval(ctx, f.env, p.ApprovalID); a.Status != store.StatusApproved {
		t.Errorf("approval after = %s", a.Status)
	}
	if _, err := f.svc.PlanMessage(ctx, f.env, p.ID, "Again?"); !errors.Is(err, ErrPlanClosed) {
		t.Errorf("a message to a done plan: %v", err)
	}
}

func TestPlanChangeWithdrawsItsApproval(t *testing.T) {
	f, m := planFixture(t)
	ctx := context.Background()
	f.turn(t, "Write the coupon importer", "Done.", 1)
	f.llm.plan = twoSteps
	p := f.draft(t, "Have Ada check the coupon tests")
	first := p.ApprovalID

	f.llm.plan = `{"reply": "Then a new thread.", "title": "Coupon tests", "steps": [{"kind": "start", "teammate": "tm1", "computer": "pc1", "title": "Coupon tests", "text": "Run them."}]}`
	if _, err := f.svc.PlanMessage(ctx, f.env, p.ID, "Use a new thread"); err != nil {
		t.Fatal(err)
	}
	f.svc.plans.Wait()
	if a, _ := f.st.Approval(ctx, f.env, first); a.Status != store.StatusDismissed {
		t.Errorf("the first approval = %s", a.Status)
	}
	if err := f.decide(t, first, integrations.Allow); err == nil {
		t.Error("the withdrawn approval was allowed")
	}
	p, _ = f.svc.Plan(ctx, f.env, p.ID)
	if p.State != store.LeadPlanProposed || p.ApprovalID == first || len(p.Steps) != 1 || p.Steps[0].Kind != StepStart || p.Steps[0].SandboxID != f.sb.ID {
		t.Fatalf("redrafted = %+v", p)
	}
	calls := f.llm.calls
	if last := calls[len(calls)-1]; !strings.Contains(last.User, "Use a new thread") || !strings.Contains(last.User, "Step 1: message Ada") {
		t.Errorf("redraft call = %s", last.User)
	}

	if err := f.decide(t, p.ApprovalID, integrations.Deny); err != nil {
		t.Fatal(err)
	}
	p, _ = f.svc.Plan(ctx, f.env, p.ID)
	if p.State != store.LeadPlanCancelled || p.Steps[0].State != StepSkipped || len(m.calls()) != 0 {
		t.Errorf("denied = %+v, sent %v", p, m.calls())
	}
}

func TestPlanQuestionAndFailures(t *testing.T) {
	f, m := planFixture(t)
	ctx := context.Background()
	f.llm.plan = `{"reply": "Which branch?", "title": "Tests", "steps": [], "question": {"text": "Which branch?", "options": ["main", "main", "release"]}}`
	p := f.draft(t, "Run the tests")
	q := p.Items[len(p.Items)-1].Question
	if p.State != store.LeadPlanAsking || p.ApprovalID != "" || q == nil || len(q.Options) != 2 {
		t.Fatalf("asking = %+v", p)
	}

	// A message that can't be sent fails its step, and a plan with nothing sent fails.
	f.turn(t, "Write the coupon importer", "Done.", 1)
	f.llm.plan = twoSteps
	if _, err := f.svc.PlanMessage(ctx, f.env, p.ID, "main"); err != nil {
		t.Fatal(err)
	}
	f.svc.plans.Wait()
	p, _ = f.svc.Plan(ctx, f.env, p.ID)
	m.err = errors.New("Ada's computer did not start")
	if err := f.decide(t, p.ApprovalID, integrations.Allow); err != nil {
		t.Fatal(err)
	}
	f.svc.plans.Wait()
	p, _ = f.svc.Plan(ctx, f.env, p.ID)
	if p.State != store.LeadPlanFailed || p.Steps[0].State != StepFailed || p.Steps[0].Note != "Ada's computer did not start" {
		t.Fatalf("failed = %+v", p)
	}
	if last := p.Items[len(p.Items)-1]; last.Kind != ItemReport || !strings.Contains(last.Text, "None of the messages went out") {
		t.Errorf("report = %+v", last)
	}

	// Without a model Lead says why.
	f.svc.Utility = nil
	p = f.draft(t, "Anything")
	if last := p.Items[len(p.Items)-1]; p.State != store.LeadPlanAsking || last.Kind != ItemNote || !strings.Contains(last.Text, "utility model") {
		t.Errorf("no model = %+v", p)
	}
}

func TestPlanWaitsForTheAnswerAndRecovers(t *testing.T) {
	f, m := planFixture(t)
	ctx := context.Background()
	f.turn(t, "Write the coupon importer", "Done.", 1)
	f.llm.plan = twoSteps
	f.llm.report = "Ada answered."
	m.quiet = true
	p := f.draft(t, "Have Ada check the coupon tests")
	if err := f.decide(t, p.ApprovalID, integrations.Allow); err != nil {
		t.Fatal(err)
	}
	waiting := func() bool {
		v, _ := f.svc.Plan(ctx, f.env, p.ID)
		return v.State == store.LeadPlanRunning && len(v.Steps) == 3 && v.Steps[1].State == StepRunning
	}
	for deadline := time.Now().Add(5 * time.Second); !waiting(); time.Sleep(5 * time.Millisecond) {
		if time.Now().After(deadline) {
			t.Fatal("the plan never waited")
		}
	}
	m.mu.Lock()
	open := m.open
	m.mu.Unlock()
	f.answer(t, open, "They pass.")
	f.svc.plans.Wait()
	p, _ = f.svc.Plan(ctx, f.env, p.ID)
	if p.State != store.LeadPlanDone || p.Items[len(p.Items)-1].Text != "Ada answered." {
		t.Fatalf("after the answer = %+v", p)
	}

	// A Studio that stopped mid-draft leaves the plan for the user to send again.
	stuck, err := f.st.AddLeadPlan(ctx, store.LeadPlan{EnvironmentID: f.env, Title: "Stuck", CreatedAt: time.Now()})
	if err != nil {
		t.Fatal(err)
	}
	f.svc.recoverPlans(ctx)
	f.svc.plans.Wait()
	v, _ := f.svc.Plan(ctx, f.env, stuck.ID)
	if v.State != store.LeadPlanAsking || len(v.Items) != 1 || !strings.Contains(v.Items[0].Text, "Studio stopped") {
		t.Errorf("recovered = %+v", v)
	}
}

func TestRosterLeavesOutTheUsersOwnComputers(t *testing.T) {
	f, _ := planFixture(t)
	ctx := context.Background()
	own, err := f.st.CreateSandbox(ctx, store.Sandbox{EnvironmentID: f.env, Name: "mine"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.st.CreateChat(ctx, store.Chat{EnvironmentID: f.env, SandboxID: own.ID, Harness: "claude", Title: "My own"}); err != nil {
		t.Fatal(err)
	}
	if _, err := f.st.CreateChat(ctx, store.Chat{EnvironmentID: f.env, SandboxID: f.sb.ID, TeammateID: f.ada.ID, Harness: "claude", Heartbeat: true}); err != nil {
		t.Fatal(err)
	}
	r, err := f.svc.roster(ctx, f.env)
	if err != nil {
		t.Fatal(err)
	}
	if len(r.Teammates) != 1 || len(r.Computers) != 1 || r.Computers[0].Name != "ada-shop" || len(r.Threads) != 1 || r.Threads[0].Title != "Coupon import" {
		t.Errorf("roster = %+v", r)
	}
}
