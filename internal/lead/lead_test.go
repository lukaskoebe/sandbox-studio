package lead

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/lukaskoebe/sandbox-studio/internal/agentmem"
	"github.com/lukaskoebe/sandbox-studio/internal/events"
	"github.com/lukaskoebe/sandbox-studio/internal/store"
)

// fakeLLM answers folds with a note and the day call with its day answer, and records calls.
type fakeLLM struct {
	mu     sync.Mutex
	day    string
	ask    string
	plan   string        // the draft of a plan
	report string        // a plan's report
	gate   chan struct{} // an answer waits for it when set
	err    error
	calls  []agentmem.Request
}

func (l *fakeLLM) Complete(_ context.Context, r agentmem.Request) (agentmem.Response, error) {
	l.mu.Lock()
	gate := l.gate
	l.mu.Unlock()
	if r.System == askPrompt && gate != nil {
		<-gate
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	l.calls = append(l.calls, r)
	if l.err != nil {
		return agentmem.Response{}, l.err
	}
	if r.System == askPrompt {
		return agentmem.Response{Text: l.ask, InputTokens: 4000, OutputTokens: 120}, nil
	}
	if r.System == planPrompt {
		return agentmem.Response{Text: l.plan, InputTokens: 3000, OutputTokens: 200}, nil
	}
	if r.System == reportPrompt {
		return agentmem.Response{Text: l.report, InputTokens: 1500, OutputTokens: 80}, nil
	}
	if r.System == foldPrompt {
		return agentmem.Response{Text: "Ada finished the coupon importer and waits for the push review.", InputTokens: 900, OutputTokens: 40}, nil
	}
	return agentmem.Response{Text: l.day, InputTokens: 5000, OutputTokens: 600}, nil
}

func (l *fakeLLM) dayCalls() int {
	l.mu.Lock()
	defer l.mu.Unlock()
	n := 0
	for _, c := range l.calls {
		if c.System == dayPrompt {
			n++
		}
	}
	return n
}

type keys struct{}

func (keys) Value(context.Context, string, string) (string, error) { return "sk-test-key", nil }

type fixture struct {
	st   *store.Store
	svc  *Service
	llm  *fakeLLM
	env  string
	ada  store.Teammate
	sb   store.Sandbox
	chat store.Chat
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	ctx := context.Background()
	st, err := store.Open(ctx, ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	env, err := st.CreateEnvironment(ctx, "shop")
	if err != nil {
		t.Fatal(err)
	}
	sec := &store.Secret{ID: "sec-anthropic", EnvironmentID: env.ID, Name: "PROVIDER_ANTHROPIC_KEY", Sealed: []byte("sealed"),
		Hosts: []string{"api.anthropic.com"}, Placeholder: "studio-sec-anthropic"}
	if _, err := st.CreateProvider(ctx, store.Provider{ID: "p-anthropic", EnvironmentID: env.ID, Name: "anthropic", Kind: "anthropic_api"}, sec); err != nil {
		t.Fatal(err)
	}
	ada, err := st.CreateTeammate(ctx, store.Teammate{EnvironmentID: env.ID, Name: "Ada", Harness: "claude", ProviderID: "p-anthropic",
		Model: "claude-sonnet-5-5", GitName: "Ada", GitEmail: "ada@agents.invalid"})
	if err != nil {
		t.Fatal(err)
	}
	sb, err := st.CreateSandbox(ctx, store.Sandbox{EnvironmentID: env.ID, Name: "ada-shop", TeammateID: ada.ID})
	if err != nil {
		t.Fatal(err)
	}
	chat, err := st.CreateChat(ctx, store.Chat{EnvironmentID: env.ID, SandboxID: sb.ID, TeammateID: ada.ID, Harness: "claude", Title: "Coupon import"})
	if err != nil {
		t.Fatal(err)
	}
	llm := &fakeLLM{}
	f := &fixture{st: st, llm: llm, env: env.ID, ada: ada, sb: sb, chat: chat}
	f.svc = &Service{Store: st, Keys: keys{}, Bus: &events.Bus{},
		Utility: func(p store.Provider, key string) (agentmem.LLM, agentmem.UtilityModel, error) {
			if key != "sk-test-key" {
				return nil, agentmem.UtilityModel{}, errors.New("no key")
			}
			return llm, agentmem.UtilityModel{Kind: p.Kind, Model: "claude-haiku-4-5", PriceInPerM: 1, PriceOutPerM: 5, Priced: true}, nil
		}}
	return f
}

// turn runs a turn of the fixture's chat with a prompt and an answer; cost is the session's
// cost so far in dollars.
func (f *fixture) turn(t *testing.T, prompt, answer string, cost float64) {
	t.Helper()
	ctx := context.Background()
	turn, err := f.st.StartChatTurn(ctx, f.chat.ID, "chat", "")
	if err != nil {
		t.Fatal(err)
	}
	for _, it := range []store.ChatItem{
		{ChatID: f.chat.ID, TurnID: turn.ID, Kind: "user", Body: json.RawMessage(`{"text":` + quote(prompt) + `}`)},
		{ChatID: f.chat.ID, TurnID: turn.ID, Kind: "agent", Body: json.RawMessage(`{"text":` + quote(answer) + `}`)},
		{ChatID: f.chat.ID, TurnID: turn.ID, Kind: "tool", Body: json.RawMessage(`{"title":"go test ./coupons/...","kind":"execute","status":"completed"}`)},
	} {
		if _, err := f.st.AddChatItem(ctx, it); err != nil {
			t.Fatal(err)
		}
	}
	usage, _ := json.Marshal(map[string]any{"cost": map[string]any{"amount": cost, "currency": "USD"}})
	if _, err := f.st.EndChatTurn(ctx, f.chat.ID, turn.ID, "end_turn", usage); err != nil {
		t.Fatal(err)
	}
}

func quote(s string) string { b, _ := json.Marshal(s); return string(b) }

// push records a push of Ada's that opened pull request 214, and its pending approval.
func (f *fixture) push(t *testing.T) {
	t.Helper()
	ctx := context.Background()
	a, _, err := f.st.RequestApproval(ctx, store.Approval{EnvironmentID: f.env, SandboxID: f.sb.ID, Kind: "git.push", Subject: "ada/coupon-import → acme/shop-web",
		Payload: json.RawMessage(`{"forge":"github","repo":"acme/shop-web"}`)})
	if err != nil {
		t.Fatal(err)
	}
	p, err := f.st.CreateGitPush(ctx, store.GitPush{ID: store.NewID(), EnvironmentID: f.env, SandboxID: f.sb.ID, TeammateName: "Ada", ForgeName: "github",
		Owner: "acme", Repo: "shop-web", Ref: "refs/heads/ada/coupon-import", NewSHA: "abc", ApprovalID: a.ID})
	if err != nil {
		t.Fatal(err)
	}
	if err := f.st.SetGitPushPR(ctx, f.env, p.ID, "", "https://github.com/acme/shop-web/pull/214", ""); err != nil {
		t.Fatal(err)
	}
}

const goodDay = `{"heading": "a coupon day", "summary": "Ada finished the coupon importer [thread:1] and opened [push:1], which waits for you [approval:1]. Nothing else moved [push:9].",
 "teammates": [{"ref": "teammate:1", "lines": [{"state": "done", "text": "Wrote the coupon CSV importer with tests in [thread:1]."}, {"state": "maybe", "text": "dropped"}]},
  {"ref": "teammate:7", "lines": [{"state": "done", "text": "made up"}]}],
 "tomorrow": ["Review [push:1] first.", "Ask Ada about [thread:42]."]}`

func TestWriteDay(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	f.turn(t, "Write a CSV importer for coupons", "Done: the importer reads the CSV and has 14 tests.", 0.40)
	f.turn(t, "Push it for review", "Pushed ada/coupon-import.", 0.65)
	f.push(t)
	f.llm.day = goodDay

	if err := f.svc.write(ctx, f.env, "now", f.svc.now()); err != nil {
		t.Fatal(err)
	}
	today := time.Now().Format(time.DateOnly)
	v, err := f.svc.Day(ctx, f.env, today)
	if err != nil {
		t.Fatal(err)
	}
	if !v.Written || v.Prose == nil {
		t.Fatalf("day not written: %+v", v)
	}
	if got := v.Prose.Summary; strings.Contains(got, "[push:9]") || !strings.Contains(got, "[push:1]") || !strings.Contains(got, "[approval:1]") {
		t.Errorf("summary refs not checked: %q", got)
	}
	if len(v.Prose.Teammates) != 1 || v.Prose.Teammates[0].Ref != "teammate:1" || len(v.Prose.Teammates[0].Lines) != 1 {
		t.Errorf("teammates not checked: %+v", v.Prose.Teammates)
	}
	if got := v.Prose.Tomorrow; len(got) != 2 || got[1] != "Ask Ada about ." && got[1] != "Ask Ada about." {
		t.Errorf("tomorrow = %q", got)
	}
	fa := v.Facts
	if len(fa.Teammates) != 1 || fa.Teammates[0].Name != "Ada" || fa.Turns != 2 || fa.Threads != 1 {
		t.Fatalf("facts = %+v", fa)
	}
	if fa.Teammates[0].Note == "" {
		t.Error("the note wasn't folded in")
	}
	if fa.Cost != 650_000 || !fa.Costed {
		t.Errorf("cost = %d, want the session's 0.65 USD", fa.Cost)
	}
	if len(fa.Pushes) != 1 || fa.Pushes[0].PRNumber != 214 || fa.Pushes[0].Branch != "ada/coupon-import" {
		t.Errorf("pushes = %+v", fa.Pushes)
	}
	if len(fa.Waiting) != 1 || fa.Waiting[0].Kind != "git.push" {
		t.Errorf("waiting = %+v", fa.Waiting)
	}
	if !strings.Contains(v.Markdown, "#214") || !strings.Contains(v.Markdown, "“Coupon import”") || strings.Contains(v.Markdown, "[thread:1]") {
		t.Errorf("markdown:\n%s", v.Markdown)
	}
	u, err := f.st.LeadUsage(ctx, f.env, today)
	if err != nil {
		t.Fatal(err)
	}
	if u.Calls != 2 || u.Cost == 0 {
		t.Errorf("usage = %+v, want the fold and the day", u)
	}
	for _, c := range f.llm.calls {
		if strings.Contains(c.User, "sk-test-key") {
			t.Error("the key reached the model")
		}
	}

	// Nothing new: the sentences are kept without another call.
	if err := f.svc.write(ctx, f.env, "now", f.svc.now()); err != nil {
		t.Fatal(err)
	}
	if n := f.llm.dayCalls(); n != 1 {
		t.Errorf("day calls = %d after a write with nothing new, want 1", n)
	}
	// Something new: written again.
	f.turn(t, "Add a dry-run flag", "Added --dry-run.", 0.80)
	f.svc.folded = nil
	if err := f.svc.write(ctx, f.env, "now", f.svc.now()); err != nil {
		t.Fatal(err)
	}
	if n := f.llm.dayCalls(); n != 2 {
		t.Errorf("day calls = %d after a new turn, want 2", n)
	}

	view, err := f.svc.View(ctx, f.env)
	if err != nil {
		t.Fatal(err)
	}
	if view.Today.Summary == "" || view.Today.Waiting != 1 || view.Today.PRs != 1 || len(view.Days) != 1 || view.Model == nil {
		t.Errorf("view = %+v", view)
	}
}

func TestDayWithoutSentences(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	f.turn(t, "Fix the cart rounding", "Fixed.", 0)
	set := store.DefaultLeadSettings()
	set.DailyLimit = 0
	if _, err := f.st.SetLeadSettings(ctx, f.env, set); err != nil {
		t.Fatal(err)
	}
	f.llm.day = goodDay
	if err := f.svc.write(ctx, f.env, "now", f.svc.now()); err != nil {
		t.Fatal(err)
	}
	v, err := f.svc.Day(ctx, f.env, time.Now().Format(time.DateOnly))
	if err != nil {
		t.Fatal(err)
	}
	if v.Prose != nil || !strings.Contains(v.Note, "daily limit") || len(v.Facts.Teammates) != 1 {
		t.Errorf("over the limit: prose %+v, note %q", v.Prose, v.Note)
	}
	if len(f.llm.calls) != 0 {
		t.Errorf("%d calls over the limit", len(f.llm.calls))
	}
	if !strings.Contains(v.Markdown, "Fix the cart rounding") && !strings.Contains(v.Markdown, "Untitled thread") && !strings.Contains(v.Markdown, "Coupon import") {
		t.Errorf("markdown without sentences lists no threads:\n%s", v.Markdown)
	}

	// A model that answers nonsense leaves the facts too.
	set.DailyLimit = 1_000_000
	if _, err := f.st.SetLeadSettings(ctx, f.env, set); err != nil {
		t.Fatal(err)
	}
	f.llm.day = "Sure! Here is the day."
	f.turn(t, "Again", "Ok.", 0)
	if err := f.svc.write(ctx, f.env, "now", f.svc.now()); err != nil {
		t.Fatal(err)
	}
	if v, _ = f.svc.Day(ctx, f.env, time.Now().Format(time.DateOnly)); v.Prose != nil || !strings.Contains(v.Note, "JSON") {
		t.Errorf("bad answer: prose %+v, note %q", v.Prose, v.Note)
	}
}

func TestDue(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	set := store.DefaultLeadSettings()
	thursday := time.Date(2026, 11, 19, 17, 59, 0, 0, time.Local)
	put := func(day string, written time.Time) {
		t.Helper()
		if err := f.st.PutLeadDay(ctx, store.LeadDay{EnvironmentID: f.env, Day: day, WrittenAt: written, Trigger: "now", Facts: json.RawMessage(`{}`)}); err != nil {
			t.Fatal(err)
		}
	}
	due := func(now time.Time) string {
		t.Helper()
		day, ok := f.svc.due(ctx, f.env, set, now)
		if !ok {
			return ""
		}
		return day.Format(time.DateOnly)
	}
	put("2026-11-18", thursday.Add(-24*time.Hour+2*time.Minute)) // Wednesday, at its time
	for _, c := range []struct {
		now  time.Time
		want string
	}{
		{thursday, ""},
		{thursday.Add(time.Minute), "2026-11-19"},
		{thursday.Add(5 * time.Hour), "2026-11-19"},                       // Studio was off at 18:00
		{thursday.Add(14 * time.Hour), "2026-11-19"},                      // and until the next morning
		{time.Date(2026, 11, 21, 18, 30, 0, 0, time.Local), "2026-11-20"}, // Friday's, on Saturday
		{time.Date(2026, 11, 22, 18, 30, 0, 0, time.Local), ""},           // nothing at the weekend
	} {
		if got := due(c.now); got != c.want {
			t.Errorf("due at %s = %q, want %q", c.now.Format("Mon 15:04"), got, c.want)
		}
	}
	// Written by hand at 15:00: still written at 18:00. Written at 18:01: not again.
	put("2026-11-19", thursday.Add(-3*time.Hour))
	if due(thursday.Add(2*time.Minute)) != "2026-11-19" {
		t.Error("a day written before its time isn't written at its time")
	}
	put("2026-11-19", thursday.Add(2*time.Minute))
	if got := due(thursday.Add(2 * time.Minute)); got != "" {
		t.Errorf("a day written after its time is written again: %q", got)
	}
	// A quiet day is looked at once, while skipping quiet days is on.
	friday := time.Date(2026, 11, 20, 18, 0, 0, 0, time.Local)
	f.svc.Now = func() time.Time { return friday }
	if err := f.svc.write(ctx, f.env, "schedule", friday); err != nil {
		t.Fatal(err)
	}
	if _, err := f.st.LeadDay(ctx, f.env, "2026-11-20"); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("a day without turns was written: %v", err)
	}
	if got := due(friday); got != "" {
		t.Errorf("a quiet day is looked at again: %q", got)
	}
	set.SkipQuiet = false
	if got := due(friday); got != "2026-11-20" {
		t.Errorf("a quiet day isn't written once quiet days are: %q", got)
	}
}

func TestScheduledWriteRetries(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	set := store.DefaultLeadSettings()
	f.turn(t, "Hello", "Hi.", 0)
	now := time.Now()
	if now.Add((maxRetries+1)*retryEvery).Day() != now.Day() {
		t.Skip("the retries would run past midnight")
	}
	set.WriteAt = 0 // its time has come
	set.Days = 0x7f
	if _, err := f.st.SetLeadSettings(ctx, f.env, set); err != nil {
		t.Fatal(err)
	}
	f.llm.err = errors.New("529 overloaded")
	clock := now
	f.svc.Now = func() time.Time { return clock }
	today := now.Format(time.DateOnly)
	for try := 0; try <= maxRetries; try++ {
		if day, ok := f.svc.due(ctx, f.env, set, clock); !ok || day.Format(time.DateOnly) != today {
			t.Fatalf("try %d isn't due", try)
		}
		if err := f.svc.write(ctx, f.env, "schedule", clock); err != nil {
			t.Fatal(err)
		}
		clock = clock.Add(retryEvery)
	}
	if day, ok := f.svc.due(ctx, f.env, set, clock); ok && day.Format(time.DateOnly) == today {
		t.Errorf("tried again past %d retries", maxRetries)
	}
	d, err := f.st.LeadDay(ctx, f.env, now.Format(time.DateOnly))
	if err != nil || !strings.Contains(d.Note, "529 overloaded") {
		t.Fatalf("the failed day = %+v, %v", d, err)
	}
	// Over the limit isn't tried again.
	f.svc.retry = nil
	if retryable(limitError{"over"}) || retryable(errNoModel) || !retryable(errors.New("timeout")) {
		t.Error("retryable is wrong")
	}
}

func TestFoldsAndAnswersLeaveTheDayItsShare(t *testing.T) {
	set := store.DefaultLeadSettings()
	priced := agentmem.UtilityModel{Priced: true, PriceInPerM: 1, PriceOutPerM: 5}
	u := store.LeadUsage{Calls: 10, Cost: set.DailyLimit * foldSharePercent / 100}
	if overLimit(u, set, priced, 1000, 200, callFold) == "" {
		t.Error("a fold went past the notes' share of the limit")
	}
	if overLimit(u, set, priced, 1000, 200, callAsk) != "" || overLimit(u, set, priced, 1000, 200, callDay) != "" {
		t.Error("an answer or the day's call was stopped within its share")
	}
	u.Cost = set.DailyLimit * askSharePercent / 100
	if overLimit(u, set, priced, 1000, 200, callAsk) == "" || overLimit(u, set, priced, 1000, 200, callDay) != "" {
		t.Error("an answer went past its share, or the day was stopped within the limit")
	}
	unpriced := agentmem.UtilityModel{}
	u = store.LeadUsage{Calls: maxUnpricedFoldCalls}
	if overLimit(u, set, unpriced, 0, 0, callFold) == "" || overLimit(u, set, unpriced, 0, 0, callAsk) != "" ||
		overLimit(u, set, unpriced, 0, 0, callDay) != "" {
		t.Error("the unpriced fold share is wrong")
	}
	u = store.LeadUsage{Calls: maxUnpricedAskCalls}
	if overLimit(u, set, unpriced, 0, 0, callAsk) == "" || overLimit(u, set, unpriced, 0, 0, callDay) != "" {
		t.Error("the unpriced answer share is wrong")
	}
}

func TestSummarizeOnce(t *testing.T) {
	f := newFixture(t)
	f.llm.day = goodDay
	f.turn(t, "Hello", "Hi.", 0)
	ch, cancel := f.svc.Bus.Subscribe()
	defer cancel()
	if err := f.svc.Summarize(f.env); err != nil {
		t.Fatal(err)
	}
	if err := f.svc.Summarize(f.env); !errors.Is(err, ErrWriting) && f.svc.Writing(f.env) {
		t.Errorf("a second Summarize while writing = %v", err)
	}
	deadline := time.After(10 * time.Second)
	for f.svc.Writing(f.env) {
		select {
		case <-ch:
		case <-deadline:
			t.Fatal("the day was never written")
		}
	}
	if _, err := f.st.LeadDay(context.Background(), f.env, time.Now().Format(time.DateOnly)); err != nil {
		t.Errorf("no day after Summarize: %v", err)
	}
}

func TestOwnComputersLeftOut(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	mine, err := f.st.CreateSandbox(ctx, store.Sandbox{EnvironmentID: f.env, Name: "dev-box"})
	if err != nil {
		t.Fatal(err)
	}
	c, err := f.st.CreateChat(ctx, store.Chat{EnvironmentID: f.env, SandboxID: mine.ID, Harness: "claude", Title: "My own thing"})
	if err != nil {
		t.Fatal(err)
	}
	turn, err := f.st.StartChatTurn(ctx, c.ID, "terminal", "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.st.EndChatTurn(ctx, c.ID, turn.ID, "end_turn", nil); err != nil {
		t.Fatal(err)
	}
	set := store.DefaultLeadSettings()
	fa, err := f.svc.gather(ctx, f.env, time.Now(), set)
	if err != nil {
		t.Fatal(err)
	}
	if len(fa.Teammates) != 0 {
		t.Errorf("own computers are in the day by default: %+v", fa.Teammates)
	}
	set.OwnComputers = true
	if fa, err = f.svc.gather(ctx, f.env, time.Now(), set); err != nil {
		t.Fatal(err)
	}
	if len(fa.Teammates) != 1 || fa.Teammates[0].ID != "" || fa.Teammates[0].Name != "Your computers" {
		t.Errorf("own computers switched on: %+v", fa.Teammates)
	}
}

func TestParseProseDropsUnknownRefs(t *testing.T) {
	f := LeadFacts{Teammates: []LeadTeammate{{Ref: "teammate:1", Threads: []LeadThread{{Ref: "thread:1"}}}}}
	p, err := parseProse("```json\n"+`{"summary": "See [thread:1] and [thread:2], not [secret:1]."}`+"\n```", f)
	if err != nil {
		t.Fatal(err)
	}
	if p.Summary != "See [thread:1] and, not." {
		t.Errorf("summary = %q", p.Summary)
	}
}

func TestWaitingSubjectTitlesAgentPermissions(t *testing.T) {
	a := store.Approval{Kind: "chat.permission", Subject: "chat/abc/def", Payload: json.RawMessage(`{"title":" Run make test "}`)}
	if got := waitingSubject(a); got != "Run make test" {
		t.Fatalf("got %q", got)
	}
	a.Payload = json.RawMessage(`{}`)
	if got := waitingSubject(a); got != "A tool call" {
		t.Fatalf("got %q", got)
	}
	if got := waitingSubject(store.Approval{Kind: "secret.refused", Subject: "api.example.com refused the secret KEY"}); got != "api.example.com refused the secret KEY" {
		t.Fatalf("got %q", got)
	}
}

func TestMarkdownEscapesWhatIsNotStudios(t *testing.T) {
	v := LeadDayView{Day: "2026-11-20", Facts: LeadFacts{Teammates: []LeadTeammate{{Ref: "teammate:1", Name: "Ada", Note: "see <https://evil.example>"}}},
		Prose: &LeadProse{Heading: "*bold*", Summary: "[Open the build](https://evil.example) ![x](https://evil.example/p.png)"}}
	md := Markdown(v, "Lead", time.Local)
	for _, bad := range []string{"\n[Open the build](", "![x](", " <https://", "· *bold*"} {
		if strings.Contains(md, bad) {
			t.Errorf("the Markdown has %q:\n%s", bad, md)
		}
	}
	if !strings.Contains(md, `\[Open the build\]`) {
		t.Errorf("the summary isn't escaped:\n%s", md)
	}
}

func (l *fakeLLM) askCalls() []agentmem.Request {
	l.mu.Lock()
	defer l.mu.Unlock()
	var out []agentmem.Request
	for _, c := range l.calls {
		if c.System == askPrompt {
			out = append(out, c)
		}
	}
	return out
}

func TestAskAnswersFromTheDay(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	f.turn(t, "Write the coupon importer", "Done; the tests pass.", 1.25)
	f.push(t)
	today := f.svc.now().Format(time.DateOnly)
	day, err := f.svc.Day(ctx, f.env, today)
	if err != nil {
		t.Fatal(err)
	}
	thread, push := day.Facts.Teammates[0].Threads[0].Ref, day.Facts.Pushes[0].Ref
	f.llm.ask = fmt.Sprintf("Ada wrote the importer in [%s].\n\nShe pushed [%s] and [push:9] is made up. Her key is sk-ant-api03-%s.",
		thread, push, strings.Repeat("x", 40))
	gate := make(chan struct{})
	f.llm.gate = gate

	a, err := f.svc.Ask(ctx, f.env, today, "  What did Ada get done?  ")
	if err != nil || a.State != store.LeadAskAnswering || a.Question != "What did Ada get done?" {
		t.Fatalf("ask = %+v, %v", a, err)
	}
	if _, err := f.svc.Ask(ctx, f.env, today, "And Juno?"); !errors.Is(err, ErrAnswering) {
		t.Errorf("a second question while answering: %v", err)
	}
	close(gate)
	f.svc.asks.Wait()

	asks, err := f.svc.Asks(ctx, f.env, today)
	if err != nil || len(asks) != 1 {
		t.Fatalf("asks = %+v, %v", asks, err)
	}
	got := asks[0]
	if got.State != store.LeadAskAnswered || got.AnsweredAt == nil || got.Cost == 0 {
		t.Fatalf("answer = %+v", got)
	}
	if !strings.Contains(got.Answer, "["+thread+"]") || !strings.Contains(got.Answer, "["+push+"]") ||
		strings.Contains(got.Answer, "push:9") || strings.Contains(got.Answer, "sk-ant") || !strings.Contains(got.Answer, "\n\n") {
		t.Errorf("answer text = %q", got.Answer)
	}
	if len(got.Refs.Teammates) != 1 || len(got.Refs.Teammates[0].Threads) != 1 || len(got.Refs.Pushes) != 1 || len(got.Refs.Waiting) != 0 {
		t.Errorf("refs = %+v", got.Refs)
	}
	calls := f.llm.askCalls()
	if len(calls) != 1 || !strings.Contains(calls[0].User, "<facts>") || !strings.Contains(calls[0].User, "<question>\n\"What did Ada get done?\"") {
		t.Fatalf("ask call = %+v", calls)
	}

	// A follow-up reads the question before it, its refs written out as names.
	f.llm.gate = nil
	if _, err := f.svc.Ask(ctx, f.env, today, "Is the push through?"); err != nil {
		t.Fatal(err)
	}
	f.svc.asks.Wait()
	calls = f.llm.askCalls()
	if len(calls) != 2 || !strings.Contains(calls[1].User, "<asked_before>") || !strings.Contains(calls[1].User, "Coupon import") ||
		strings.Contains(strings.SplitN(calls[1].User, "<asked_before>", 2)[1], "["+thread+"]") {
		t.Errorf("follow-up call = %s", calls[len(calls)-1].User)
	}

	if _, err := f.svc.Ask(ctx, f.env, f.svc.now().AddDate(0, 0, 2).Format(time.DateOnly), "Tomorrow?"); !errors.Is(err, ErrFutureDay) {
		t.Errorf("a day that hasn't come: %v", err)
	}
}

func TestAskSaysWhyItCantAnswer(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	f.turn(t, "Write the coupon importer", "Done.", 0)
	today := f.svc.now().Format(time.DateOnly)
	set := store.DefaultLeadSettings()
	set.DailyLimit = 1 // a micro-dollar: nothing fits
	if _, err := f.st.SetLeadSettings(ctx, f.env, set); err != nil {
		t.Fatal(err)
	}
	if _, err := f.svc.Ask(ctx, f.env, today, "What happened?"); err != nil {
		t.Fatal(err)
	}
	f.svc.asks.Wait()
	asks, _ := f.svc.Asks(ctx, f.env, today)
	if len(asks) != 1 || asks[0].State != store.LeadAskFailed || !strings.Contains(asks[0].Note, "share of Lead's daily limit") {
		t.Errorf("asks = %+v", asks)
	}
	if len(f.llm.askCalls()) != 0 {
		t.Error("the model was called over the limit")
	}

	// A Studio that stopped while answering leaves no answer waiting forever.
	if _, err := f.st.AddLeadAsk(ctx, f.env, store.LeadAsk{Day: today, Question: "Still there?", AskedAt: time.Now()}); err != nil {
		t.Fatal(err)
	}
	if err := f.st.FailLeadAsksAnswering(ctx, "Studio stopped."); err != nil {
		t.Fatal(err)
	}
	asks, _ = f.svc.Asks(ctx, f.env, today)
	if asks[1].State != store.LeadAskFailed || asks[1].Note != "Studio stopped." {
		t.Errorf("the stopped answer = %+v", asks[1])
	}
}

func TestSinceYouLooked(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	now := f.svc.now()
	if now.Hour() == 0 && now.Minute() < 2 {
		t.Skip("too close to midnight")
	}
	v, err := f.svc.View(ctx, f.env)
	if err != nil || v.Today.Since != nil {
		t.Fatalf("never looked: %+v, %v", v.Today.Since, err)
	}
	if err := f.st.SetLeadSeen(ctx, f.env, now.Add(-time.Minute)); err != nil {
		t.Fatal(err)
	}
	f.turn(t, "Write the coupon importer", "Done.", 0)
	f.push(t)
	v, _ = f.svc.View(ctx, f.env)
	s := v.Today.Since
	if s == nil || s.Threads != 1 || s.Pushes != 1 || s.PRs != 1 || len(s.Teammates) != 1 || s.Teammates[0] != "Ada" {
		t.Errorf("since = %+v", s)
	}
	if err := f.svc.Seen(ctx, f.env); err != nil {
		t.Fatal(err)
	}
	if err := f.st.SetLeadSeen(ctx, f.env, now.AddDate(0, 0, -1)); err != nil {
		t.Fatal(err)
	}
	if v, _ = f.svc.View(ctx, f.env); v.Today.Since != nil {
		t.Errorf("looked yesterday: %+v", v.Today.Since)
	}
}
