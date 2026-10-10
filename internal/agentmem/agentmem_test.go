package agentmem

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/lukaskoebe/sandbox-studio/internal/agenthook"
	"github.com/lukaskoebe/sandbox-studio/internal/agentproto"
	"github.com/lukaskoebe/sandbox-studio/internal/memory"
	"github.com/lukaskoebe/sandbox-studio/internal/store"
)

type fixture struct {
	t        *testing.T
	ctx      context.Context
	st       *store.Store
	mem      *memory.Service
	emb      *memory.FakeEmbedder
	svc      *Service
	llm      *fakeLLM
	env      string
	ada, bob store.Persona
	sbAda    store.Sandbox
	sbBob    store.Sandbox
	sbPlain  store.Sandbox
	now      time.Time
}

type fakeKeys struct{}

func (fakeKeys) Value(context.Context, string, string) (string, error) { return "sk-test-secret", nil }

type fakeLLM struct {
	mu    sync.Mutex
	calls []Request
	reply string
	err   error
	// respond, when set, answers instead of reply and err.
	respond func(Request) (string, error)
}

func (f *fakeLLM) Complete(_ context.Context, r Request) (Response, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, r)
	if f.respond != nil {
		text, err := f.respond(r)
		return Response{Text: text, InputTokens: 1000, OutputTokens: 200}, err
	}
	return Response{Text: f.reply, InputTokens: 1000, OutputTokens: 200}, f.err
}

func (f *fakeLLM) n() int { f.mu.Lock(); defer f.mu.Unlock(); return len(f.calls) }

func newFixture(t *testing.T) *fixture {
	t.Helper()
	ctx := context.Background()
	st, err := store.Open(ctx, ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	env, err := st.CreateEnvironment(ctx, "work")
	if err != nil {
		t.Fatal(err)
	}
	f := &fixture{t: t, ctx: ctx, st: st, env: env.ID, llm: &fakeLLM{reply: `{"facts": []}`}, now: time.Date(2026, 10, 10, 12, 0, 0, 0, time.UTC)}
	if _, err := st.CreateProvider(ctx, store.Provider{ID: "anth", EnvironmentID: env.ID, Name: "anthropic", Kind: "anthropic_api"},
		&store.Secret{ID: "sec", Name: "anthropic-key", Sealed: []byte("x"), Hosts: []string{"api.anthropic.com"}, Placeholder: "studio-0"}); err != nil {
		t.Fatal(err)
	}
	if _, err := st.CreateProvider(ctx, store.Provider{ID: "sub", EnvironmentID: env.ID, Name: "sub", Kind: "claude_subscription"}, nil); err != nil {
		t.Fatal(err)
	}
	mk := func(name, provider string) store.Persona {
		p, err := st.CreatePersona(ctx, store.Persona{EnvironmentID: env.ID, Name: name, Harness: "claude", ProviderID: provider,
			Soul: name + " is careful and terse.", GitName: name, GitEmail: name + "@agents.invalid"})
		if err != nil {
			t.Fatal(err)
		}
		return p
	}
	f.ada, f.bob = mk("ada", "anth"), mk("bob", "sub")
	sb := func(name, persona string) store.Sandbox {
		s, err := st.CreateSandbox(ctx, store.Sandbox{EnvironmentID: env.ID, Name: name, CPUs: 1, PersonaID: persona})
		if err != nil {
			t.Fatal(err)
		}
		return s
	}
	f.sbAda, f.sbBob, f.sbPlain = sb("ada-dev", f.ada.ID), sb("bob-dev", f.bob.ID), sb("plain", "")
	f.emb = &memory.FakeEmbedder{}
	f.mem = memory.New(st.DB(), f.emb, nil)
	f.svc = New(st, f.mem, fakeKeys{}, nil)
	f.svc.Now = func() time.Time { return f.now }
	f.svc.Utility = func(p store.Provider, key string) (LLM, UtilityModel, error) {
		if key != "sk-test-secret" {
			t.Errorf("utility got key %q", key)
		}
		m, ok := UtilityFor(p)
		if !ok {
			return nil, m, ErrNoUtility
		}
		return f.llm, m, nil
	}
	return f
}

// call is HandleCall as the hub makes it: the sandbox comes from the socket.
func (f *fixture) call(sandbox, method string, params any) (json.RawMessage, error) {
	f.t.Helper()
	b, _ := json.Marshal(params)
	res, err := f.svc.HandleCall(f.ctx, sandbox, method, b)
	if err != nil {
		return nil, err
	}
	out, _ := json.Marshal(res)
	return out, nil
}

func (f *fixture) mustCall(sandbox, method string, params any) json.RawMessage {
	f.t.Helper()
	out, err := f.call(sandbox, method, params)
	if err != nil {
		f.t.Fatalf("%s: %v", method, err)
	}
	return out
}

func (f *fixture) hook(sandbox string, ev map[string]any) agenthook.Result {
	f.t.Helper()
	var r agenthook.Result
	if err := json.Unmarshal(f.mustCall(sandbox, agentproto.MethodHook, ev), &r); err != nil {
		f.t.Fatal(err)
	}
	return r
}

func (f *fixture) remember(sandbox, text string) memory.Fact {
	f.t.Helper()
	var out struct{ Fact memory.Fact }
	if err := json.Unmarshal(f.mustCall(sandbox, agentproto.MethodRemember, map[string]any{"text": text, "kind": "fact", "source": "user"}), &out); err != nil {
		f.t.Fatal(err)
	}
	return out.Fact
}

func (f *fixture) logOf(sessionRef string) []LogEntry {
	f.t.Helper()
	ss, err := f.svc.Sessions(f.ctx, f.env, "", 0)
	if err != nil {
		f.t.Fatal(err)
	}
	for _, s := range ss {
		if s.SessionRef == sessionRef {
			l, err := f.svc.SessionLog(f.ctx, f.env, s.ID)
			if err != nil {
				f.t.Fatal(err)
			}
			return l
		}
	}
	f.t.Fatalf("no session %q", sessionRef)
	return nil
}

func TestScopeEnforcement(t *testing.T) {
	f := newFixture(t)
	secret := f.remember(f.sbAda.ID, "Ada's deploy target is the staging cluster")
	if secret.Scope != memory.PersonaScope(f.ada.ID) || secret.AuthorPersona != f.ada.ID || secret.Tier != memory.TierUser {
		t.Fatalf("remembered %+v", secret)
	}

	// Bob can't see, read, correct or forget Ada's fact.
	out := f.mustCall(f.sbBob.ID, agentproto.MethodMemorySearch, map[string]any{"query": "deploy target staging cluster"})
	if strings.Contains(string(out), secret.ID) {
		t.Fatalf("bob found ada's fact: %s", out)
	}
	for _, m := range []string{agentproto.MethodMemoryGet, agentproto.MethodForget, agentproto.MethodCorrect, agentproto.MethodShare} {
		p := map[string]any{"fact_id": secret.ID, "text": "changed"}
		if m == agentproto.MethodMemoryGet {
			p = map[string]any{"id": secret.ID}
		}
		if _, err := f.call(f.sbBob.ID, m, p); err == nil {
			t.Errorf("%s on another persona's fact succeeded", m)
		}
	}
	if got, _ := f.mem.Fact(f.ctx, f.env, secret.ID); got.Status != memory.StatusActive || got.Text != secret.Text {
		t.Fatalf("ada's fact changed: %+v", got)
	}

	// Forged payloads: unknown fields are refused, other scopes too.
	for _, p := range []map[string]any{
		{"text": "x y", "kind": "fact", "persona": f.ada.ID},
		{"text": "x y", "kind": "fact", "scope": memory.PersonaScope(f.ada.ID)},
		{"text": "x y", "kind": "fact", "sandboxId": f.sbAda.ID},
	} {
		if _, err := f.call(f.sbBob.ID, agentproto.MethodRemember, p); err == nil {
			t.Errorf("forged remember %v accepted", p)
		}
	}
	// A hook naming another persona and sandbox is Bob's anyway.
	f.hook(f.sbBob.ID, map[string]any{"harness": "claude", "event": agenthook.SessionStart, "sessionId": "s-bob",
		"persona": f.ada.ID, "personaId": f.ada.ID, "sandboxId": f.sbAda.ID, "environmentId": "other"})
	ss, _ := f.svc.Sessions(f.ctx, f.env, f.ada.ID, 0)
	for _, s := range ss {
		if s.SessionRef == "s-bob" {
			t.Fatalf("forged hook landed in ada's sessions: %+v", s)
		}
	}
	ss, _ = f.svc.Sessions(f.ctx, f.env, f.bob.ID, 0)
	found := false
	for _, s := range ss {
		found = found || (s.SessionRef == "s-bob" && s.SandboxID == f.sbBob.ID)
	}
	if !found {
		t.Fatalf("bob's sessions %+v", ss)
	}

	// No persona, no memory: hooks are neutral, tools fail.
	if r := f.hook(f.sbPlain.ID, map[string]any{"event": agenthook.SessionStart, "sessionId": "s"}); r.Context != "" {
		t.Fatalf("plain sandbox got context %q", r.Context)
	}
	if _, err := f.call(f.sbPlain.ID, agentproto.MethodRemember, map[string]any{"text": "a b", "kind": "fact"}); !errors.Is(err, ErrNoPersona) {
		t.Fatalf("plain remember: %v", err)
	}
	if _, err := f.call("nope", agentproto.MethodMemorySearch, map[string]any{"query": "a"}); err == nil {
		t.Fatal("unknown sandbox searched")
	}

	// Ada forgets her own fact.
	f.mustCall(f.sbAda.ID, agentproto.MethodForget, map[string]any{"fact_id": secret.ID})
	if got, _ := f.mem.Fact(f.ctx, f.env, secret.ID); got.Status != memory.StatusRetracted {
		t.Fatalf("forget: %+v", got)
	}
	if _, err := f.call(f.sbAda.ID, agentproto.MethodRemember, map[string]any{"text": "the key is sk-abcdefghijklmnopqrstuv", "kind": "fact"}); err == nil {
		t.Fatal("remembered a credential")
	}
}

func TestShareNeedsApproval(t *testing.T) {
	f := newFixture(t)
	fact := f.remember(f.sbAda.ID, "The api repository runs tests with make check")
	var res struct{ ApprovalID string }
	json.Unmarshal(f.mustCall(f.sbAda.ID, agentproto.MethodShare, map[string]any{"fact_id": fact.ID}), &res)
	a, err := f.st.Approval(f.ctx, f.env, res.ApprovalID)
	if err != nil || a.Kind != ApprovalKind || a.Status != "pending" || a.SandboxID != f.sbAda.ID {
		t.Fatalf("approval %+v %v", a, err)
	}
	var p SharePayload
	json.Unmarshal(a.Payload, &p)
	if p.PersonaID != f.ada.ID || p.FactID != fact.ID || p.Text != fact.Text {
		t.Fatalf("payload %+v", p)
	}
	shared, _ := f.mem.Facts(f.ctx, f.env, memory.FactFilter{Scope: memory.SharedScope})
	if len(shared) != 0 {
		t.Fatalf("shared before approval: %+v", shared)
	}
	// remember with scope shared is a share request too.
	json.Unmarshal(f.mustCall(f.sbAda.ID, agentproto.MethodRemember, map[string]any{"text": "Deploys freeze on Fridays", "kind": "decision", "scope": "shared"}), &res)
	if res.ApprovalID == "" {
		t.Fatal("remember scope=shared made no approval")
	}

	sf, err := f.svc.DecideShare(f.ctx, a, true)
	if err != nil || sf == nil || sf.Scope != memory.SharedScope || sf.AuthorPersona != f.ada.ID || sf.Text != fact.Text {
		t.Fatalf("decide: %+v %v", sf, err)
	}
	if again, _ := f.svc.DecideShare(f.ctx, a, true); again.ID != sf.ID || again.SupportCount != 2 {
		t.Fatalf("second share should reinforce: %+v", again)
	}

	// Bob's next session hears about it.
	r := f.hook(f.sbBob.ID, map[string]any{"event": agenthook.SessionStart, "sessionId": "b1"})
	if !strings.Contains(r.Context, "Shared memory changed") || !strings.Contains(r.Context, sf.ID) || !strings.Contains(r.Context, "bob is careful") {
		t.Fatalf("bob's pack: %s", r.Context)
	}
	// Ada's own share is not news to her.
	if r := f.hook(f.sbAda.ID, map[string]any{"event": agenthook.SessionStart, "sessionId": "a1"}); strings.Contains(r.Context, sf.ID) {
		t.Fatalf("ada told about her own share: %s", r.Context)
	}
}

func TestSessionStartPack(t *testing.T) {
	f := newFixture(t)
	scope := memory.PersonaScope(f.ada.ID)
	mk := func(in memory.PageInput) memory.Page {
		p, err := f.mem.CreatePage(f.ctx, f.env, in, memory.Origin{Tier: memory.TierUser})
		if err != nil {
			t.Fatal(err)
		}
		return p
	}
	core := mk(memory.PageInput{Scope: scope, Slug: "me", Title: "Me", Kind: "persona-self", Compiled: "I prefer small commits.", AlwaysLoad: true})
	proj := mk(memory.PageInput{Scope: memory.SharedScope, Slug: "project/github.com/acme/api", Title: "Acme API", Kind: "project", Compiled: "Go service; make check runs tests."})
	other := mk(memory.PageInput{Scope: memory.PersonaScope(f.bob.ID), Slug: "bob-core", Title: "Bob", Kind: "topic", Compiled: "bob private", AlwaysLoad: true})

	r := f.hook(f.sbAda.ID, map[string]any{"event": agenthook.SessionStart, "sessionId": "s1", "source": "resume",
		"gitRemote": "github.com/Acme/api"})
	for _, want := range []string{"<studio-memory>", "</studio-memory>", "ada is careful", "I prefer small commits", "make check runs tests"} {
		if !strings.Contains(r.Context, want) {
			t.Errorf("pack lacks %q:\n%s", want, r.Context)
		}
	}
	if strings.Contains(r.Context, "bob private") || len([]rune(r.Context)) > PackBudget+len(closeTag)+1 {
		t.Fatalf("pack leaked or too long:\n%s", r.Context)
	}
	l := f.logOf("s1")
	got := map[string]bool{}
	for _, e := range l {
		got[e.ItemID] = true
		if e.Kind != LogContext || !strings.Contains(e.Summary, "resume") {
			t.Errorf("log entry %+v", e)
		}
	}
	if !got[core.ID] || !got[proj.ID] || got[other.ID] {
		t.Fatalf("logged %v", got)
	}

	// A huge soul is cut to its budget.
	f.ada.Soul = strings.Repeat("soul ", 5000)
	if _, err := f.st.UpdatePersona(f.ctx, f.ada); err != nil {
		t.Fatal(err)
	}
	r = f.hook(f.sbAda.ID, map[string]any{"event": agenthook.SessionStart, "sessionId": "s2"})
	if n := strings.Count(r.Context, "soul "); n > soulBudget/5 || !strings.HasSuffix(r.Context, closeTag+"\n") {
		t.Fatalf("soul not cut (%d) or tag missing", n)
	}
}

func TestRecallThresholdAndBudget(t *testing.T) {
	f := newFixture(t)
	relevant := f.remember(f.sbAda.ID, "The billing service deploys with make release-billing")
	f.remember(f.sbAda.ID, "Lunch is usually at noon on Thursdays")
	for i := range 12 {
		f.remember(f.sbAda.ID, "Billing service note "+string(rune('a'+i))+": "+strings.Repeat("billing service detail ", 30))
	}
	f.hook(f.sbAda.ID, map[string]any{"event": agenthook.SessionStart, "sessionId": "s1"})

	r := f.hook(f.sbAda.ID, map[string]any{"event": agenthook.UserPrompt, "sessionId": "s1", "prompt": "How do I deploy the billing service?"})
	if !strings.Contains(r.Context, relevant.ID) || strings.Contains(r.Context, "Lunch") {
		t.Fatalf("recall:\n%s", r.Context)
	}
	body := strings.TrimSuffix(strings.TrimPrefix(r.Context, openTag+"\n"), closeTag+"\n")
	if n := len([]rune(body)); n > RecallBudget+100 || strings.Count(body, "\n- ") > maxRecall {
		t.Fatalf("recall over budget: %d chars\n%s", n, body)
	}
	var recalled int
	for _, e := range f.logOf("s1") {
		if e.Kind != LogRecall {
			continue
		}
		recalled++
		var why recallWhy
		if err := json.Unmarshal(e.Why, &why); err != nil || why.Passed == "" || e.Score == nil {
			t.Errorf("recall log without reason: %+v", e)
		}
	}
	if recalled == 0 || recalled != strings.Count(r.Context, "[fact ") {
		t.Fatalf("logged %d recalls for:\n%s", recalled, r.Context)
	}
	// The same prompt again injects nothing new from what was given.
	r2 := f.hook(f.sbAda.ID, map[string]any{"event": agenthook.UserPrompt, "sessionId": "s1", "prompt": "How do I deploy the billing service?"})
	if strings.Contains(r2.Context, relevant.ID) {
		t.Fatalf("re-injected: %s", r2.Context)
	}
	// One shared word is below the threshold.
	if r := f.hook(f.sbAda.ID, map[string]any{"event": agenthook.UserPrompt, "sessionId": "s9", "prompt": "what about thursdays"}); r.Context != "" {
		t.Fatalf("weak match injected: %s", r.Context)
	}
}

const transcript = "user: Please always run the linter with golangci-lint before committing.\nassistant: Will do. The project uses Go 1.26.\n"

func (f *fixture) stop(sandbox, session, text string) {
	f.t.Helper()
	f.hook(sandbox, map[string]any{"event": agenthook.Stop, "sessionId": session, "transcript": text})
	select {
	case j := <-f.svc.jobs:
		if err := f.svc.extract(f.ctx, j); err != nil {
			f.t.Fatal(err)
		}
	default:
		f.t.Fatal("nothing queued")
	}
}

func TestExtraction(t *testing.T) {
	f := newFixture(t)
	existing := f.remember(f.sbAda.ID, "The project uses Go 1.26")
	f.llm.reply = "```json\n" + `{"facts": [
		{"text": "Run golangci-lint before committing", "kind": "preference", "tier": "user", "entities": ["Go Lint"], "attribute": "", "evidence": "always run the linter with golangci-lint"},
		{"text": "The project uses Go 1.26", "kind": "fact", "tier": "user", "entities": [], "attribute": "go_version", "evidence": "The project uses Go 1.26."}
	]}` + "\n```"
	f.stop(f.sbAda.ID, "s1", transcript)
	if f.llm.n() != 1 || !strings.Contains(f.llm.calls[0].User, "golangci-lint") {
		t.Fatalf("llm calls %+v", f.llm.calls)
	}
	facts, _ := f.mem.Facts(f.ctx, f.env, memory.FactFilter{Scope: memory.PersonaScope(f.ada.ID)})
	if len(facts) != 2 {
		t.Fatalf("facts %+v", facts)
	}
	for _, x := range facts {
		switch x.ID {
		case existing.ID:
			if x.SupportCount != 2 {
				t.Errorf("duplicate not reinforced: %+v", x)
			}
		default:
			if x.Tier != memory.TierUser || x.AuthorPersona != f.ada.ID || len(x.EntityIDs) != 1 || x.EntityIDs[0] != "go-lint" {
				t.Errorf("extracted %+v", x)
			}
		}
	}
	u, _ := f.svc.usage(f.ctx, f.env)
	if u.Calls != 1 || u.CostMicros != 1000+1000 { // 1000 in × $1/M + 200 out × $5/M
		t.Fatalf("usage %+v", u)
	}

	// Assistant-only evidence can't claim the user said it.
	f.llm.reply = `{"facts": [{"text": "Go 1.26 is the toolchain", "kind": "fact", "tier": "user", "entities": [], "attribute": "", "evidence": "The project uses Go 1.26"}]}`
	f.stop(f.sbAda.ID, "s2", transcript)
	facts, _ = f.mem.Facts(f.ctx, f.env, memory.FactFilter{Scope: memory.PersonaScope(f.ada.ID)})
	for _, x := range facts {
		if x.Text == "Go 1.26 is the toolchain" && x.Tier != memory.TierInferred {
			t.Fatalf("tier not downgraded: %+v", x)
		}
	}
}

func TestExtractionRejects(t *testing.T) {
	for name, reply := range map[string]string{
		"prose":          "Here are the facts: none",
		"unknown field":  `{"facts": [{"text": "a b", "kind": "fact", "tier": "user", "entities": [], "attribute": "", "evidence": "golangci-lint", "scope": "shared"}]}`,
		"bad kind":       `{"facts": [{"text": "a b", "kind": "gossip", "tier": "user", "entities": [], "attribute": "", "evidence": "golangci-lint"}]}`,
		"bad tier":       `{"facts": [{"text": "a b", "kind": "fact", "tier": "verified", "entities": [], "attribute": "", "evidence": "golangci-lint"}]}`,
		"made-up quote":  `{"facts": [{"text": "a b", "kind": "fact", "tier": "user", "entities": [], "attribute": "", "evidence": "the user loves tabs"}]}`,
		"secret":         `{"facts": [{"text": "key sk-abcdefghijklmnopqrstuvwx", "kind": "fact", "tier": "user", "entities": [], "attribute": "", "evidence": "golangci-lint"}]}`,
		"bad attribute":  `{"facts": [{"text": "a b", "kind": "fact", "tier": "user", "entities": [], "attribute": "Go Version", "evidence": "golangci-lint"}]}`,
		"no facts array": `{}`,
		"one good one bad": `{"facts": [{"text": "lint first", "kind": "fact", "tier": "user", "entities": [], "attribute": "", "evidence": "golangci-lint"},
			{"text": "", "kind": "fact", "tier": "user", "entities": [], "attribute": "", "evidence": "golangci-lint"}]}`,
	} {
		t.Run(name, func(t *testing.T) {
			f := newFixture(t)
			f.llm.reply = reply
			f.stop(f.sbAda.ID, "s1", transcript)
			facts, _ := f.mem.Facts(f.ctx, f.env, memory.FactFilter{Scope: memory.PersonaScope(f.ada.ID)})
			if len(facts) != 0 {
				t.Fatalf("wrote %+v", facts)
			}
			var rejected bool
			for _, e := range f.logOf("s1") {
				var w extractWhy
				json.Unmarshal(e.Why, &w)
				rejected = rejected || (e.Kind == LogExtraction && w.Rejected != "")
			}
			if !rejected {
				t.Fatal("rejection not logged")
			}
		})
	}
}

func TestExtractionBudget(t *testing.T) {
	f := newFixture(t)
	f.svc.Budget = Budget{MaxCostMicros: 5000, MaxCalls: 1}
	f.stop(f.sbAda.ID, "s1", transcript) // ≈ $0.011 of output alone: over $0.005
	if f.llm.n() != 0 {
		t.Fatal("called over budget")
	}
	f.svc.Budget.MaxCostMicros = 1_000_000
	f.stop(f.sbAda.ID, "s1", transcript)
	if f.llm.n() != 1 {
		t.Fatal("not called within budget")
	}
	u, _ := f.svc.usage(f.ctx, f.env)
	if u.Calls != 1 || u.Skipped != 1 {
		t.Fatalf("usage %+v", u)
	}
	// A new day resets it.
	f.now = f.now.Add(24 * time.Hour)
	if u, _ := f.svc.usage(f.ctx, f.env); u.Calls != 0 {
		t.Fatalf("next day %+v", u)
	}
	// Subscriptions don't extract.
	f.stop(f.sbBob.ID, "b1", transcript)
	if f.llm.n() != 1 {
		t.Fatal("subscription extracted")
	}
	rep, err := f.svc.UsageReport(f.ctx, f.env)
	if err != nil || rep.Today.Skipped != 1 || len(rep.Models) != 2 {
		t.Fatalf("report %+v %v", rep, err)
	}
	// Call cap for unpriced models.
	f.svc.Budget = Budget{MaxCostMicros: 1, MaxCalls: 1}
	m := UtilityModel{Model: "local"}
	f.svc.spend(f.ctx, f.env, 1, 1, 0)
	if why, _ := f.svc.overBudget(f.ctx, f.env, m, 10); why == "" {
		t.Fatal("call cap not enforced")
	}
}

func TestQueueFull(t *testing.T) {
	f := newFixture(t)
	for range queueSize {
		f.hook(f.sbAda.ID, map[string]any{"event": agenthook.Stop, "sessionId": "s", "transcript": "user: hi there friend"})
	}
	if _, err := f.call(f.sbAda.ID, agentproto.MethodHook, map[string]any{"event": agenthook.Stop, "sessionId": "s", "transcript": "user: x"}); err == nil {
		t.Fatal("full queue accepted a delta")
	}
	// Empty deltas are not queued.
	f.hook(f.sbAda.ID, map[string]any{"event": agenthook.PreCompact, "sessionId": "s"})
}

func TestCorrect(t *testing.T) {
	f := newFixture(t)
	old := f.remember(f.sbAda.ID, "Staging lives at staging.acme.test")
	var out struct{ Fact memory.Fact }
	json.Unmarshal(f.mustCall(f.sbAda.ID, agentproto.MethodCorrect, map[string]any{"fact_id": old.ID, "text": "Staging lives at stage.acme.test"}), &out)
	if out.Fact.Supersedes != old.ID {
		t.Fatalf("correct: %+v", out.Fact)
	}
	if got, _ := f.mem.Fact(f.ctx, f.env, old.ID); got.Status != memory.StatusSuperseded {
		t.Fatalf("old %+v", got)
	}
	var page struct{ ID string }
	if err := json.Unmarshal(f.mustCall(f.sbAda.ID, agentproto.MethodMemoryGet, map[string]any{"id": out.Fact.ID}), &page); err != nil || page.ID != out.Fact.ID {
		t.Fatalf("get %+v %v", page, err)
	}
}

func TestDefaultUtilityNeverLogsKey(t *testing.T) {
	if got := scrub("401: invalid key sk-real", "sk-real"); strings.Contains(got, "sk-real") {
		t.Fatal(got)
	}
	if _, m, err := DefaultUtility(store.Provider{Kind: "chatgpt_subscription"}, ""); !errors.Is(err, ErrNoUtility) || m.Model != "" {
		t.Fatal("subscription has a utility model")
	}
	if m, _ := UtilityFor(store.Provider{Kind: "openai_compatible", BaseURL: "http://x/v1", Model: "qwen"}); m.Model != "qwen" || m.Priced {
		t.Fatalf("%+v", m)
	}
}
