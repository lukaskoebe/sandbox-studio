package policy

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/lukaskoebe/sandbox-studio/internal/store"
)

// fakeReviewer answers with the next of its answers, and records what it was asked.
type fakeReviewer struct {
	mu      sync.Mutex
	answers []Review
	err     error
	block   chan struct{} // when set, a question waits for it to be closed
	started chan struct{} // when set, each question is announced on it
	asked   []ReviewInput
}

func (f *fakeReviewer) Review(ctx context.Context, _ string, in ReviewInput) (Review, error) {
	f.mu.Lock()
	f.asked = append(f.asked, in)
	answer := Review{Decision: DecisionAsk, Reason: "no answer given"}
	if len(f.answers) > 0 {
		answer, f.answers = f.answers[0], f.answers[1:]
	}
	err := f.err
	block, started := f.block, f.started
	f.mu.Unlock()
	if started != nil {
		started <- struct{}{}
	}
	if block != nil {
		select {
		case <-block:
		case <-ctx.Done():
			return Review{}, ctx.Err()
		}
	}
	return answer, err
}

func (f *fakeReviewer) calls() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.asked)
}

// reviewerEngine is an engine at the assisted level with a reviewer, a chat in which the sandbox's
// user has sent a prompt, and a clock the test moves.
func reviewerEngine(t *testing.T, answers ...Review) (*Engine, store.Environment, store.Sandbox, *fakeReviewer, *time.Time) {
	t.Helper()
	ctx := context.Background()
	e, env, sb := newEngine(t)
	e.Hold = 30 * time.Millisecond
	now := time.Date(2026, 10, 13, 9, 0, 0, 0, time.UTC)
	e.Now = func() time.Time { return now }
	if err := e.Store.SetSecurityLevel(ctx, env.ID, store.LevelAssisted); err != nil {
		t.Fatal(err)
	}
	rv := &fakeReviewer{answers: answers}
	e.Reviewer = rv
	setPrompt(t, e.Store, env.ID, sb.ID, "install the project's dependencies")
	return e, env, sb, rv, &now
}

// setPrompt makes the sandbox's latest user prompt text.
func setPrompt(t *testing.T, st *store.Store, envID, sandboxID, text string) {
	t.Helper()
	ctx := context.Background()
	chat, err := st.CreateChat(ctx, store.Chat{EnvironmentID: envID, SandboxID: sandboxID, Harness: "claude", State: store.ChatIdle, CreatedAt: time.Now(), UpdatedAt: time.Now()})
	if err != nil {
		t.Fatal(err)
	}
	turn, err := st.StartChatTurn(ctx, chat.ID, store.OriginChat, "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.AddChatItem(ctx, store.ChatItem{ChatID: chat.ID, TurnID: turn.ID, Kind: "user", Body: []byte(`{"text":"` + text + `"}`)}); err != nil {
		t.Fatal(err)
	}
}

func decideAt(e *Engine, sb store.Sandbox, host string, port int) (store.Rule, error) {
	return e.Decide(context.Background(), Request{EnvironmentID: sb.EnvironmentID, SandboxID: sb.ID, SandboxName: sb.Name, Host: host, Port: port})
}

// An allow is a rule of the reviewer that lasts a day and is shown in Activity; the next connection
// of the same host is allowed by that rule, without asking the reviewer again.
func TestReviewerAllowMakesARevertibleRule(t *testing.T) {
	e, env, sb, rv, now := reviewerEngine(t, Review{Decision: DecisionAllow, Reason: "the package registry"})
	r, err := decideAt(e, sb, "packages.example.org", 443)
	if err != nil || r.Action != store.ActionAllow || r.ID == "" || r.Origin != store.OriginReviewer || r.SandboxID != sb.ID {
		t.Fatalf("the reviewer's allow: %+v, %v", r, err)
	}
	if r.ExpiresAt == nil || r.ExpiresAt.Sub(*now) != reviewRuleLife {
		t.Errorf("the rule expires %v after the decision, want %v", r.ExpiresAt, reviewRuleLife)
	}
	if rv.calls() != 1 || rv.asked[0].Host != "packages.example.org" || rv.asked[0].Task != "install the project's dependencies" {
		t.Fatalf("the question: %+v", rv.asked)
	}
	logged, _ := e.Store.Approvals(context.Background(), env.ID, store.StatusApproved, 10)
	if len(logged) != 1 || logged[0].RuleID != r.ID || !strings.Contains(string(logged[0].Payload), `"decided":"reviewer"`) {
		t.Fatalf("activity: %+v", logged)
	}

	again, err := decideAt(e, sb, "packages.example.org", 443)
	if err != nil || again.ID != r.ID || rv.calls() != 1 {
		t.Fatalf("the next connection: %+v, %v, %d questions", again, err, rv.calls())
	}

	// Reverting deletes the rule, and the host is asked about from then on, not reviewed again.
	reverted, err := e.Revert(context.Background(), env.ID, logged[0].ID)
	if err != nil || !reverted.Reverted {
		t.Fatalf("revert: %+v, %v", reverted, err)
	}
	if _, err := decideAt(e, sb, "packages.example.org", 443); !errors.Is(err, ErrUndecided) {
		t.Fatalf("after a revert: %v; want the user asked", err)
	}
	if rv.calls() != 1 {
		t.Errorf("a reverted host was reviewed again: %d questions", rv.calls())
	}
	if _, err := e.Revert(context.Background(), env.ID, logged[0].ID); !errors.Is(err, ErrNotReviewed) {
		t.Errorf("a second revert: %v", err)
	}
}

// A deny refuses the connection with a rule of the reviewer, which can be reverted too.
func TestReviewerDenyRefuses(t *testing.T) {
	e, env, sb, rv, _ := reviewerEngine(t, Review{Decision: DecisionDeny, Reason: "a paste site"})
	r, err := decideAt(e, sb, "paste.example.org", 443)
	if err != nil || r.Action != store.ActionDeny || r.Origin != store.OriginReviewer {
		t.Fatalf("the reviewer's deny: %+v, %v", r, err)
	}
	denied, _ := e.Store.Approvals(context.Background(), env.ID, store.StatusDenied, 10)
	if len(denied) != 1 || denied[0].RuleID != r.ID {
		t.Fatalf("activity: %+v", denied)
	}
	if _, err := decideAt(e, sb, "paste.example.org", 443); err != nil || rv.calls() != 1 {
		t.Fatalf("the next connection: %v, %d questions", err, rv.calls())
	}
	if _, err := e.Revert(context.Background(), env.ID, denied[0].ID); err != nil {
		t.Fatalf("revert of a deny: %v", err)
	}
	if _, err := decideAt(e, sb, "paste.example.org", 443); !errors.Is(err, ErrUndecided) {
		t.Fatalf("after reverting a deny: %v; want the user asked", err)
	}
}

// An ask, a question that gets no answer, and a request the reviewer never sees.
func TestReviewerAsksWhenUnsure(t *testing.T) {
	e, env, sb, rv, _ := reviewerEngine(t, Review{Decision: DecisionAsk, Reason: "not sure"})
	if _, err := decideAt(e, sb, "maybe.example.org", 443); !errors.Is(err, ErrUndecided) {
		t.Fatalf("an ask: %v", err)
	}
	if _, err := decideAt(e, sb, "maybe.example.org", 443); !errors.Is(err, ErrUndecided) || rv.calls() != 1 {
		t.Fatalf("the same question again: %v, %d questions; an ask is kept for the task", err, rv.calls())
	}
	if list, _ := e.Store.Approvals(context.Background(), env.ID, store.StatusApproved, 10); len(list) != 0 {
		t.Errorf("an ask was recorded as a decision: %+v", list)
	}

	// No answer in time: the question is not kept, so the next connection asks the reviewer again.
	e2, _, sb2, rv2, _ := reviewerEngine(t)
	rv2.err = errors.New("the model timed out")
	if _, err := decideAt(e2, sb2, "slow.example.org", 443); !errors.Is(err, ErrUndecided) {
		t.Fatalf("no answer: %v", err)
	}
	if _, err := decideAt(e2, sb2, "slow.example.org", 443); !errors.Is(err, ErrUndecided) || rv2.calls() != 2 {
		t.Fatalf("no answer kept: %d questions", rv2.calls())
	}
}

// The reviewer never sees a placeholder, an address that isn't public, a host the curated list
// allows, or a request at a level that doesn't use it.
func TestReviewerIsAskedOnlyWhereItDecides(t *testing.T) {
	ctx := context.Background()
	e, env, sb, rv, _ := reviewerEngine(t, Review{Decision: DecisionAllow, Reason: "x"})
	if _, err := e.Decide(ctx, Request{EnvironmentID: env.ID, SandboxID: sb.ID, Host: "secret.example.org", Port: 443, Placeholder: true}); !errors.Is(err, ErrUndecided) {
		t.Errorf("a placeholder: %v", err)
	}
	if _, err := decideAt(e, sb, "10.1.2.3", 443); !errors.Is(err, ErrUndecided) {
		t.Errorf("a private address: %v", err)
	}
	if r, err := decideAt(e, sb, "registry.npmjs.org", 443); err != nil || r.ID != "" {
		t.Errorf("a curated host: %+v, %v; want the list to allow it", r, err)
	}
	if rv.calls() != 0 {
		t.Fatalf("the reviewer was asked %d times about what it must not see", rv.calls())
	}
	if err := e.Store.SetSecurityLevel(ctx, env.ID, store.LevelGuarded); err != nil {
		t.Fatal(err)
	}
	if _, err := decideAt(e, sb, "example.net", 443); !errors.Is(err, ErrUndecided) || rv.calls() != 0 {
		t.Errorf("guarded asked the reviewer: %v, %d", err, rv.calls())
	}
	if err := e.Store.SetSecurityLevel(ctx, env.ID, store.LevelAssisted); err != nil {
		t.Fatal(err)
	}
	e.Reviewer = nil
	if _, err := decideAt(e, sb, "example.net", 443); !errors.Is(err, ErrUndecided) {
		t.Errorf("assisted without a reviewer: %v; want the user asked", err)
	}
}

// A browser request goes to the reviewer with its method, path and page, and not with its headers.
func TestReviewerSeesBrowserRequestsWithTheirPage(t *testing.T) {
	ctx := context.Background()
	e, env, sb, rv, _ := reviewerEngine(t, Review{Decision: DecisionAsk, Reason: "x"})
	e.Browsers = fakeBrowsers{site: "docs.example.org"}
	_, err := e.Decide(ctx, Request{EnvironmentID: env.ID, SandboxID: sb.ID, Host: "cdn.example.net", Port: 443, Browser: true, Deferred: true,
		HTTP: &HTTPRequest{Method: "GET", Path: "/assets/app.js", Site: "docs.example.org"}})
	if !errors.Is(err, ErrUndecided) {
		t.Fatalf("the browser request: %v", err)
	}
	if rv.calls() != 1 {
		t.Fatalf("%d questions", rv.calls())
	}
	in := rv.asked[0]
	if in.Method != "GET" || in.Path != "/assets/app.js" || in.Site != "docs.example.org" || in.Port != 443 {
		t.Errorf("the question: %+v", in)
	}
}

// fakeBrowsers says which page a browser's requests belong to.
type fakeBrowsers struct{ site string }

func (f fakeBrowsers) TopLevel(context.Context, string, string) string { return f.site }
func (f fakeBrowsers) Allows(context.Context, string, string, HTTPRequest) bool {
	return false
}
func (f fakeBrowsers) Approve(context.Context, string, string, string) {}

// A new prompt is a new task: the same host is asked about again for it.
func TestReviewerAsksAgainForANewTask(t *testing.T) {
	e, _, sb, rv, _ := reviewerEngine(t, Review{Decision: DecisionAsk, Reason: "x"}, Review{Decision: DecisionAsk, Reason: "y"})
	if _, err := decideAt(e, sb, "maybe.example.org", 443); !errors.Is(err, ErrUndecided) {
		t.Fatal(err)
	}
	setPrompt(t, e.Store, sb.EnvironmentID, sb.ID, "now write the docs")
	if _, err := decideAt(e, sb, "maybe.example.org", 443); !errors.Is(err, ErrUndecided) || rv.calls() != 2 {
		t.Fatalf("a new task: %d questions", rv.calls())
	}
	if rv.asked[1].Task != "now write the docs" {
		t.Errorf("the new task is %q", rv.asked[1].Task)
	}
}

// A rule the reviewer made that has expired no longer decides; the host is asked about.
func TestReviewerRuleExpires(t *testing.T) {
	e, _, sb, rv, now := reviewerEngine(t, Review{Decision: DecisionAllow, Reason: "x"})
	if _, err := decideAt(e, sb, "packages.example.org", 443); err != nil {
		t.Fatal(err)
	}
	*now = now.Add(reviewRuleLife + time.Minute)
	if _, err := decideAt(e, sb, "packages.example.org", 443); !errors.Is(err, ErrUndecided) || rv.calls() != 1 {
		t.Fatalf("after the rule expired: %v, %d questions", err, rv.calls())
	}
}

// Connections that arrive while one question is open share its answer: the model is asked once.
func TestReviewerAsksOncePerHostWhileAnswering(t *testing.T) {
	e, _, sb, rv, _ := reviewerEngine(t, Review{Decision: DecisionAllow, Reason: "x"})
	rv.block = make(chan struct{})
	rv.started = make(chan struct{}, 4)
	type result struct {
		rule store.Rule
		err  error
	}
	first := make(chan result, 1)
	go func() {
		r, err := decideAt(e, sb, "packages.example.org", 443)
		first <- result{r, err}
	}()
	<-rv.started
	second := make(chan result, 1)
	go func() {
		r, err := decideAt(e, sb, "packages.example.org", 443)
		second <- result{r, err}
	}()
	close(rv.block)
	for _, ch := range []chan result{first, second} {
		res := <-ch
		if res.err != nil || res.rule.Action != store.ActionAllow || res.rule.ID == "" {
			t.Errorf("a connection during the question: %+v, %v", res.rule, res.err)
		}
	}
	if rv.calls() != 1 {
		t.Errorf("%d questions for one host", rv.calls())
	}
}
