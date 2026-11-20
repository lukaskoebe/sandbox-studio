package lead

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/lukaskoebe/sandbox-studio/internal/integrations"
	"github.com/lukaskoebe/sandbox-studio/internal/store"
)

const followUpT1 = `{"report": "Ada says the tests pass, but she skipped the slow ones.", "followUps": [
 {"thread": "t1", "text": "Please run the slow tests too and say how they do."},
 {"thread": "t9", "text": "A thread the round never wrote in."}]}`

const startStep = `{"reply": "I'll ask Ada in a new thread.", "title": "Coupon tests", "steps": [
 {"kind": "start", "teammate": "tm1", "computer": "pc1", "title": "Coupon tests", "text": "Run the coupon tests and tell me how they do."}]}`

func pendingLeadPlans(t *testing.T, f *fixture) []store.Approval {
	t.Helper()
	all, err := f.st.Approvals(context.Background(), f.env, store.StatusPending, 100)
	if err != nil {
		t.Fatal(err)
	}
	var out []store.Approval
	for _, a := range all {
		if a.Kind == ApprovalKind {
			out = append(out, a)
		}
	}
	return out
}

func TestFollowUpInATeammatesThreadWaitsForApproval(t *testing.T) {
	f, m := planFixture(t)
	ctx := context.Background()
	f.turn(t, "Write the coupon importer", "Done.", 1)
	set, _ := f.st.LeadSettings(ctx, f.env)
	set.FollowUps = true // only threads the plan started go without asking
	if _, err := f.st.SetLeadSettings(ctx, f.env, set); err != nil {
		t.Fatal(err)
	}
	f.llm.plan = twoSteps
	f.llm.reports = []string{followUpT1}
	f.llm.report = "Ada ran the slow tests too; they pass."
	p := f.draft(t, "Have Ada check the coupon tests")
	first := p.ApprovalID
	if err := f.decide(t, first, integrations.Allow); err != nil {
		t.Fatal(err)
	}
	f.svc.plans.Wait()
	p, _ = f.svc.Plan(ctx, f.env, p.ID)
	if p.State != store.LeadPlanProposed || p.ApprovalID == "" || p.ApprovalID == first {
		t.Fatalf("after the report = %s, approval %q", p.State, p.ApprovalID)
	}
	if len(m.calls()) != 1 {
		t.Fatalf("sent before the follow-up was approved: %q", m.calls())
	}
	last := p.Steps[len(p.Steps)-1]
	if len(p.Steps) != 4 || last.State != StepProposed || last.Round != 1 || last.Auto || last.ChatID != f.chat.ID ||
		!strings.Contains(last.Text, "slow tests") {
		t.Fatalf("steps = %+v", p.Steps)
	}
	pending := pendingLeadPlans(t, f)
	if len(pending) != 1 || pending[0].ID != p.ApprovalID {
		t.Fatalf("pending = %+v", pending)
	}
	var payload PlanPayload
	_ = json.Unmarshal(pending[0].Payload, &payload)
	if len(payload.Steps) != 1 || payload.Steps[0].Text != last.Text {
		t.Fatalf("the approval shows %+v", payload.Steps)
	}

	if err := f.decide(t, p.ApprovalID, integrations.Allow); err != nil {
		t.Fatal(err)
	}
	f.svc.plans.Wait()
	p, _ = f.svc.Plan(ctx, f.env, p.ID)
	if p.State != store.LeadPlanDone || len(p.Steps) != 6 {
		t.Fatalf("after the follow-up = %s %+v", p.State, p.Steps)
	}
	sent := m.calls()
	if len(sent) != 2 || !strings.Contains(sent[1], "slow tests") {
		t.Fatalf("sent = %q", sent)
	}
	if m.from[1].Auto || m.from[1].ApprovedAt.IsZero() || !m.from[1].ApprovedAt.Equal(*p.Steps[3].ApprovedAt) {
		t.Errorf("the follow-up came as %+v", m.from[1])
	}
	var notes, reports []string
	for _, it := range p.Items {
		switch it.Kind {
		case ItemNote:
			notes = append(notes, it.Text)
		case ItemReport:
			reports = append(reports, it.Text)
		}
	}
	if !slicesContain(notes, "You approved the follow-up.") || len(reports) != 2 || reports[1] != f.llm.report {
		t.Errorf("notes %q, reports %q", notes, reports)
	}
}

func slicesContain(all []string, s string) bool {
	for _, v := range all {
		if v == s {
			return true
		}
	}
	return false
}

func TestFollowUpWithoutAskingOnlyInThreadsThePlanStarted(t *testing.T) {
	f, m := planFixture(t)
	ctx := context.Background()
	f.turn(t, "Write the coupon importer", "Done.", 1)
	f.llm.plan = startStep

	// Off: even a thread the plan started waits for approval.
	f.llm.reports = []string{followUpT1}
	p := f.draft(t, "Have Ada check the coupon tests in a new thread")
	if err := f.decide(t, p.ApprovalID, integrations.Allow); err != nil {
		t.Fatal(err)
	}
	f.svc.plans.Wait()
	p, _ = f.svc.Plan(ctx, f.env, p.ID)
	if p.State != store.LeadPlanProposed || len(m.calls()) != 1 {
		t.Fatalf("with follow-ups off = %s, sent %q", p.State, m.calls())
	}
	if _, err := f.svc.CancelPlan(ctx, f.env, p.ID); err != nil {
		t.Fatal(err)
	}
	p, _ = f.svc.Plan(ctx, f.env, p.ID)
	if last := p.Steps[len(p.Steps)-1]; last.State != StepSkipped || p.Steps[0].State != StepDone || len(pendingLeadPlans(t, f)) != 0 {
		t.Fatalf("cancelled = %+v", p.Steps)
	}

	// On: Lead follows up in its own thread at once, at most maxFollowUps times.
	set, _ := f.st.LeadSettings(ctx, f.env)
	set.FollowUps = true
	if _, err := f.st.SetLeadSettings(ctx, f.env, set); err != nil {
		t.Fatal(err)
	}
	f.llm.reports = []string{followUpT1, followUpT1, followUpT1}
	f.llm.report = "Done."
	p = f.draft(t, "Have Ada check the coupon tests in a new thread")
	if err := f.decide(t, p.ApprovalID, integrations.Allow); err != nil {
		t.Fatal(err)
	}
	f.svc.plans.Wait()
	p, _ = f.svc.Plan(ctx, f.env, p.ID)
	if p.State != store.LeadPlanDone {
		t.Fatalf("with follow-ups on = %s %+v", p.State, p.Steps)
	}
	sent := m.calls()[1:]
	if len(sent) != 1+maxFollowUps {
		t.Fatalf("sent %q, want a start and %d follow-ups", sent, maxFollowUps)
	}
	for i, from := range m.from[2:] {
		if !from.Auto || !from.ApprovedAt.IsZero() {
			t.Errorf("follow-up %d came as %+v", i+1, from)
		}
	}
	rounds := map[int]int{}
	for _, st := range p.Steps {
		if st.Kind == StepMessage {
			rounds[st.Round]++
			if !st.Auto || st.ApprovedAt != nil || st.State != StepDone {
				t.Errorf("follow-up step = %+v", st)
			}
		}
	}
	if rounds[1] != 1 || rounds[2] != 1 || len(pendingLeadPlans(t, f)) != 0 {
		t.Errorf("rounds = %v, pending %d", rounds, len(pendingLeadPlans(t, f)))
	}
	// The last report was told no follow-ups may be sent.
	var lastIn string
	for _, c := range f.llm.calls {
		if c.System == reportPrompt {
			lastIn = c.User
		}
	}
	if !strings.Contains(lastIn, "No follow-ups may be sent") || !strings.Contains(lastIn, "Have Ada check the coupon tests") {
		t.Errorf("the last report read %q", lastIn)
	}
	var firstIn string
	for _, c := range f.llm.calls {
		if c.System == reportPrompt {
			firstIn = c.User
			break
		}
	}
	if !strings.Contains(firstIn, "this plan has 2 rounds of follow-ups left") {
		t.Errorf("the first report read %q", firstIn)
	}
}

func TestParseReport(t *testing.T) {
	refs := map[string]PlanStep{"t1": {ChatID: "c1", Teammate: "Ada", Thread: "Coupons"}}
	text, ups := parseReport("Ada says they pass.", refs, true)
	if text != "Ada says they pass." || ups != nil {
		t.Errorf("prose = %q %+v", text, ups)
	}
	text, ups = parseReport("```json\n"+followUpT1+"\n```", refs, true)
	if !strings.HasPrefix(text, "Ada says") || len(ups) != 1 || ups[0].ChatID != "c1" || ups[0].Kind != StepMessage {
		t.Errorf("json = %q %+v", text, ups)
	}
	if _, ups := parseReport(followUpT1, refs, false); ups != nil {
		t.Errorf("follow-ups when none may be sent: %+v", ups)
	}
	if text, _ := parseReport(`{"report": ""}`, refs, true); text != "" {
		t.Errorf("an empty report = %q", text)
	}
}

func TestStandingOrderComesDueOnceADay(t *testing.T) {
	f, _ := planFixture(t)
	ctx := context.Background()
	f.turn(t, "Write the coupon importer", "Done.", 1)
	f.llm.plan = twoSteps
	monday := time.Date(2026, 11, 16, 8, 0, 0, 0, time.Local)
	now := monday
	f.svc.Now = func() time.Time { return now }

	if _, err := f.svc.AddOrder(ctx, f.env, OrderInput{Text: "Check the coupon tests", At: 9 * 60, Days: 0}); !errors.Is(err, ErrNoDays) {
		t.Errorf("no days = %v", err)
	}
	if _, err := f.svc.AddOrder(ctx, f.env, OrderInput{Text: "  ", At: 9 * 60, Days: 31}); !errors.Is(err, ErrNoMessage) {
		t.Errorf("no words = %v", err)
	}
	o, err := f.svc.AddOrder(ctx, f.env, OrderInput{Text: "Check the coupon tests", At: 9 * 60, Days: 0b0011111, Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	if o.Next == nil || !o.Next.Equal(monday.Add(time.Hour)) || o.LastDay != "" {
		t.Fatalf("new order = %+v", o)
	}
	plans := func() []LeadPlanView {
		f.svc.plans.Wait()
		ps, err := f.svc.Plans(ctx, f.env)
		if err != nil {
			t.Fatal(err)
		}
		return ps
	}

	f.svc.orders(ctx, f.env, now)
	if n := len(plans()); n != 0 {
		t.Fatalf("%d plans before its time", n)
	}
	now = monday.Add(65 * time.Minute)
	f.svc.orders(ctx, f.env, now)
	f.svc.orders(ctx, f.env, now.Add(time.Minute))
	ps := plans()
	if len(ps) != 1 || ps[0].OrderID != o.ID || ps[0].State != store.LeadPlanProposed {
		t.Fatalf("plans at 09:05 = %+v", ps)
	}
	// The draft was told it is a standing order's.
	var in string
	for _, c := range f.llm.calls {
		if c.System == planPrompt {
			in = c.User
		}
	}
	if !strings.Contains(in, "standing order") {
		t.Errorf("the draft read %q", in)
	}
	views, _ := f.svc.Orders(ctx, f.env)
	if views[0].LastPlan == nil || views[0].LastPlan.ID != ps[0].ID || views[0].LastDay != "2026-11-16" ||
		views[0].Next == nil || !views[0].Next.Equal(monday.AddDate(0, 0, 1).Add(time.Hour)) {
		t.Fatalf("after it came due = %+v", views[0])
	}

	// Tuesday: the plan from Monday still waits, so it drafts none and says so.
	now = monday.AddDate(0, 0, 1).Add(2 * time.Hour)
	f.svc.orders(ctx, f.env, now)
	views, _ = f.svc.Orders(ctx, f.env)
	if n := len(plans()); n != 1 || !strings.Contains(views[0].Note, "still open") {
		t.Fatalf("Tuesday: %d plans, note %q", n, views[0].Note)
	}

	// Wednesday, with Monday's plan cancelled: a new plan.
	if _, err := f.svc.CancelPlan(ctx, f.env, ps[0].ID); err != nil {
		t.Fatal(err)
	}
	now = monday.AddDate(0, 0, 2).Add(2 * time.Hour)
	f.svc.orders(ctx, f.env, now)
	views, _ = f.svc.Orders(ctx, f.env)
	if n := len(plans()); n != 2 || views[0].Note != "" {
		t.Fatalf("Wednesday: %d plans, note %q", n, views[0].Note)
	}

	// Saturday isn't one of its days, and an order that is off never comes due.
	now = monday.AddDate(0, 0, 5).Add(2 * time.Hour)
	f.svc.orders(ctx, f.env, now)
	if n := len(plans()); n != 2 {
		t.Fatalf("Saturday: %d plans", n)
	}
	if _, err := f.svc.SetOrder(ctx, f.env, o.ID, OrderInput{Text: o.Text, At: o.At, Days: 127, Enabled: false}); err != nil {
		t.Fatal(err)
	}
	f.svc.orders(ctx, f.env, now)
	if n := len(plans()); n != 2 {
		t.Fatalf("off: %d plans", n)
	}

	// Turned on past its time today, it waits for its next time; Run now drafts at once.
	v, err := f.svc.SetOrder(ctx, f.env, o.ID, OrderInput{Text: o.Text, At: o.At, Days: 127, Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	if v.LastDay != "2026-11-21" || v.Next == nil || v.Next.Day() != 22 {
		t.Errorf("turned on again = %+v", v)
	}
	f.svc.orders(ctx, f.env, now)
	if n := len(plans()); n != 2 {
		t.Fatalf("turned on past its time: %d plans", n)
	}
	if _, err := f.svc.RunOrder(ctx, f.env, o.ID); err != nil {
		t.Fatal(err)
	}
	if n := len(plans()); n != 3 {
		t.Fatalf("run now: %d plans", n)
	}
	if err := f.svc.DeleteOrder(ctx, f.env, o.ID); err != nil {
		t.Fatal(err)
	}
	if views, _ := f.svc.Orders(ctx, f.env); len(views) != 0 || len(plans()) != 3 {
		t.Errorf("after delete: %d orders", len(views))
	}
}

func TestStandingOrderWithNothingToDoIsDone(t *testing.T) {
	f, m := planFixture(t)
	ctx := context.Background()
	f.llm.plan = `{"reply": "No pull request has failing checks this morning.", "title": "Failing checks", "steps": []}`
	o, err := f.svc.AddOrder(ctx, f.env, OrderInput{Text: "Hand failing checks to whoever is idle", At: 9 * 60, Days: 31})
	if err != nil {
		t.Fatal(err)
	}
	p, err := f.svc.RunOrder(ctx, f.env, o.ID)
	if err != nil {
		t.Fatal(err)
	}
	f.svc.plans.Wait()
	p, _ = f.svc.Plan(ctx, f.env, p.ID)
	if p.State != store.LeadPlanDone || len(m.calls()) != 0 || p.Title != "Failing checks" {
		t.Errorf("nothing to do = %+v", p)
	}
	// The same reply to a plan the user asked for leaves it to the user.
	p = f.draft(t, "Hand failing checks to whoever is idle")
	if p.State != store.LeadPlanAsking {
		t.Errorf("asked by the user = %s", p.State)
	}
}

func TestRosterHasOpenPullRequestsAndBusyTeammates(t *testing.T) {
	f, _ := planFixture(t)
	ctx := context.Background()
	push := func(branch string) store.GitPush {
		a, _, err := f.st.RequestApproval(ctx, store.Approval{EnvironmentID: f.env, SandboxID: f.sb.ID, Kind: "git.push", Subject: branch,
			Payload: json.RawMessage(`{"branch":` + quote(branch) + `}`)})
		if err != nil {
			t.Fatal(err)
		}
		p, err := f.st.CreateGitPush(ctx, store.GitPush{ID: store.NewID(), EnvironmentID: f.env, SandboxID: f.sb.ID, TeammateName: "Ada",
			ForgeID: "gh", ForgeName: "github", Owner: "acme", Repo: "shop", Ref: "refs/heads/" + branch, NewSHA: "abc", ApprovalID: a.ID})
		if err != nil {
			t.Fatal(err)
		}
		if err := f.st.SettleGitPush(ctx, f.env, p.ID, store.PushPending, store.PushPushed, "", ""); err != nil {
			t.Fatal(err)
		}
		return p
	}
	push("ada/red")
	merged := push("ada/merged")
	push("ada/red") // the same branch again: one row, one read
	asked := map[string]int{}
	var mu sync.Mutex // the roster reads them side by side
	f.svc.PRs = func(_ context.Context, _ string, approval string) (PRState, error) {
		mu.Lock()
		defer mu.Unlock()
		asked[approval]++
		if approval == merged.ApprovalID {
			return PRState{Found: true, Number: 7, State: "merged", Checks: "success"}, nil
		}
		return PRState{Found: true, Number: 12, State: "open", Checks: "failure"}, nil
	}
	if _, err := f.st.SetChatState(ctx, f.env, f.chat.ID, store.ChatRunning); err != nil {
		t.Fatal(err)
	}
	r, err := f.svc.roster(ctx, f.env)
	if err != nil {
		t.Fatal(err)
	}
	if len(r.PullRequests) != 1 || r.PullRequests[0].Branch != "ada/red" || r.PullRequests[0].Checks != "failure" ||
		r.PullRequests[0].Teammate != "tm1" || r.PullRequests[0].Computer != "pc1" || r.PullRequests[0].Number != 12 {
		t.Errorf("pull requests = %+v", r.PullRequests)
	}
	if len(asked) != 2 {
		t.Errorf("asked %v; a branch's pull request is read once", asked)
	}
	if !r.Teammates[0].Busy {
		t.Errorf("teammates = %+v", r.Teammates)
	}
}
