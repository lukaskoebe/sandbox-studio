package agentmem

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/lukaskoebe/sandbox-studio/internal/integrations"
	"github.com/lukaskoebe/sandbox-studio/internal/memory"
	"github.com/lukaskoebe/sandbox-studio/internal/store"
)

func day(d int) time.Time { return time.Date(2026, 9, d, 9, 0, 0, 0, time.UTC) }

// mk writes a fact directly.
func (f *fixture) mk(scope, text, tier, author, entity, attr string, observed time.Time) memory.Fact {
	f.t.Helper()
	in := memory.FactInput{Scope: scope, Kind: memory.KindFact, Text: text, Attribute: attr, ObservedAt: &observed}
	if entity != "" {
		in.EntityIDs = []string{entity}
	}
	fact, err := f.mem.CreateFact(f.ctx, f.env, in, memory.Origin{Tier: tier, AuthorPersona: author, Evidence: "test"})
	if err != nil {
		f.t.Fatal(err)
	}
	return fact
}

func (f *fixture) get(id string) memory.Fact {
	f.t.Helper()
	fact, err := f.mem.Fact(f.ctx, f.env, id)
	if err != nil {
		f.t.Fatal(err)
	}
	return fact
}

func (f *fixture) dream(scope string) DreamRun {
	f.t.Helper()
	r, err := f.svc.Dream(f.ctx, f.env, scope, TriggerManual)
	if err != nil {
		f.t.Fatalf("dream %s: %v", scope, err)
	}
	return r
}

func (f *fixture) pending(kind string) []store.Approval {
	f.t.Helper()
	list, err := f.st.Approvals(f.ctx, f.env, store.StatusPending, 100)
	if err != nil {
		f.t.Fatal(err)
	}
	var out []store.Approval
	for _, a := range list {
		if a.Kind == kind {
			out = append(out, a)
		}
	}
	return out
}

// judge answers with verdict when the prompt has both texts, else no_conflict.
func judge(rules ...[3]string) func(Request) (string, error) {
	return func(r Request) (string, error) {
		for _, rule := range rules {
			if strings.Contains(r.User, rule[0]) && strings.Contains(r.User, rule[1]) {
				return fmt.Sprintf(`{"verdict": %q, "reason": "test rule"}`, rule[2]), nil
			}
		}
		return `{"verdict": "no_conflict", "reason": "unrelated"}`, nil
	}
}

func TestDreamAcceptance(t *testing.T) {
	f := newFixture(t)
	ada := memory.PersonaScope(f.ada.ID)
	// Contradictory facts from two sources: ada's session and the user in shared memory.
	mine := f.mk(ada, "Deploy the api with make ship", memory.TierInferred, f.ada.ID, "api", "deploy.command", day(1))
	shared := f.mk(memory.SharedScope, "Deploy the api with make release", memory.TierUser, "", "api", "deploy.command", day(2))
	// A change over time from equally trusted sources.
	pg15 := f.mk(ada, "The api database is Postgres 15", memory.TierUser, "", "api", "db.version", day(3))
	pg16 := f.mk(ada, "The api database is Postgres 16", memory.TierUser, "", "api", "db.version", day(20))
	f.llm.respond = judge([3]string{"make ship", "make release", VerdictContradiction}, [3]string{"Postgres 15", "Postgres 16", VerdictSupersedes})

	r := f.dream(ada)
	if r.Status != DreamDone || r.Stats.Conflicts != 1 || r.Stats.Superseded != 1 || r.Stats.Pages < 1 || r.Stats.Model != "claude-haiku-4-5" {
		t.Fatalf("run %+v", r)
	}

	// The time-based change was applied with a timeline entry.
	if got := f.get(pg15.ID); got.Status != memory.StatusSuperseded || got.ValidUntil == nil || !got.ValidUntil.Equal(day(20)) {
		t.Fatalf("pg15 %+v", got)
	}
	page, err := f.mem.PageBySlug(f.ctx, f.env, ada, "topic/api")
	if err != nil {
		t.Fatal(err)
	}
	page, _ = f.mem.Page(f.ctx, f.env, page.ID)
	found := false
	for _, e := range page.Timeline {
		found = found || (strings.Contains(e.Text, "Postgres 16") && e.FactID == pg16.ID)
	}
	if !found || !strings.Contains(page.Compiled, "Postgres 16") || strings.Contains(page.Compiled, "Postgres 15") {
		t.Fatalf("page %q timeline %+v", page.Compiled, page.Timeline)
	}

	// The contradiction became a conflict with an approval in the inbox.
	conflicts, _ := f.mem.Conflicts(f.ctx, f.env, ada)
	if len(conflicts) != 1 || conflicts[0].Verdict != "contradiction" || conflicts[0].FactA.ID != mine.ID || conflicts[0].FactB.ID != shared.ID {
		t.Fatalf("conflicts %+v", conflicts)
	}
	c := conflicts[0]
	approvals := f.pending(ConflictApprovalKind)
	if len(approvals) != 1 || c.ApprovalID != approvals[0].ID {
		t.Fatalf("approvals %+v conflict %+v", approvals, c)
	}
	var payload ConflictPayload
	if err := json.Unmarshal(approvals[0].Payload, &payload); err != nil || payload.ConflictID != c.ID || payload.FactB.Scope != memory.SharedScope {
		t.Fatalf("payload %+v %v", payload, err)
	}

	// Until resolved, the shared fact wins at recall.
	hits, err := f.mem.Search(f.ctx, f.env, memory.SearchRequest{Query: "make ship", Persona: f.ada.ID})
	hits = factHits(hits)
	if err != nil || len(hits) != 2 || hits[0].ID != shared.ID || hits[1].ID != mine.ID || hits[1].OverriddenBy != shared.ID {
		t.Fatalf("recall before %+v %v", hits, err)
	}

	// Resolving changes recall and closes the approval.
	if _, err := f.svc.ResolveConflict(f.ctx, f.env, c.ID, memory.ConflictResolution{Resolution: "keep_a", Note: "ship is right"}); err != nil {
		t.Fatal(err)
	}
	hits, _ = f.mem.Search(f.ctx, f.env, memory.SearchRequest{Query: "make ship", Persona: f.ada.ID})
	hits = factHits(hits)
	if len(hits) == 0 || hits[0].ID != mine.ID || hits[0].Disputed || contains(hits, shared.ID) {
		t.Fatalf("recall after %+v", hits)
	}
	if a, _ := f.st.Approval(f.ctx, f.env, c.ApprovalID); a.Status != store.StatusApproved {
		t.Fatalf("approval %s", a.Status)
	}
	detail, err := f.svc.ConflictDetail(f.ctx, f.env, c.ID)
	if err != nil || len(detail.SourcesA) == 0 || len(detail.SourcesB) == 0 || len(detail.Timeline) < 2 {
		t.Fatalf("detail %+v %v", detail, err)
	}

	// A second run finds nothing new and asks the model nothing again.
	calls := f.llm.n()
	if r := f.dream(ada); r.Status != DreamDone || r.Stats.Conflicts != 0 || r.Stats.Superseded != 0 || f.llm.n() != calls {
		t.Fatalf("second run %+v, %d calls", r, f.llm.n()-calls)
	}
	runs, _ := f.svc.DreamRuns(f.ctx, f.env, ada, 10)
	if len(runs) != 2 {
		t.Fatalf("runs %+v", runs)
	}
}

func factHits(hits []memory.Hit) []memory.Hit {
	var out []memory.Hit
	for _, h := range hits {
		if h.Type == "fact" {
			out = append(out, h)
		}
	}
	return out
}

func contains(hits []memory.Hit, id string) bool {
	for _, h := range hits {
		if h.ID == id {
			return true
		}
	}
	return false
}

func TestParseVerdictIsStrict(t *testing.T) {
	a := memory.Fact{ObservedAt: day(1)}
	b := memory.Fact{ObservedAt: day(2)}
	ok := []string{
		`{"verdict": "contradiction", "reason": "x"}`,
		"```json\n{\"verdict\": \"supersedes\", \"reason\": \"newer\"}\n```",
	}
	for _, s := range ok {
		if _, err := parseVerdict(s, a, b); err != nil {
			t.Errorf("%q: %v", s, err)
		}
	}
	bad := []string{
		``,
		`I think they conflict.`,
		`{"verdict": "maybe", "reason": "x"}`,
		`{"verdict": "temporal_supersession", "reason": "x"}`,
		`{"verdict": "contradiction"}`,
		`{"verdict": "contradiction", "reason": ""}`,
		`{"verdict": "contradiction", "reason": "x", "confidence": 0.9}`,
		`{"verdict": "contradiction", "reason": "x"} {"verdict": "duplicate", "reason": "y"}`,
		`{"verdict": "contradiction", "reason": "x"} and more`,
		`["contradiction"]`,
		`{"verdict": "contradiction", "reason": "key sk-ant-api03-abcdefghijklmnopqrstuvwxyz0123456789"}`,
	}
	for _, s := range bad {
		if v, err := parseVerdict(s, a, b); err == nil {
			t.Errorf("%q accepted as %+v", s, v)
		}
	}
	// supersedes needs B observed after A.
	if _, err := parseVerdict(`{"verdict": "supersedes", "reason": "x"}`, b, b); err == nil {
		t.Error("supersedes accepted for facts observed together")
	}
}

func TestMalformedVerdictChangesNothing(t *testing.T) {
	f := newFixture(t)
	ada := memory.PersonaScope(f.ada.ID)
	x := f.mk(ada, "CI runs on GitHub Actions", memory.TierUser, "", "ci", "ci.system", day(1))
	y := f.mk(ada, "CI runs on Buildkite", memory.TierUser, "", "ci", "ci.system", day(2))
	f.llm.reply = `{"verdict": "supersedes", "reason": "newer", "extra": true}`
	r := f.dream(ada)
	if r.Status != DreamDone || r.Stats.Judged != 1 || r.Stats.Rejected != 1 || r.Stats.Superseded != 0 || r.Stats.Conflicts != 0 {
		t.Fatalf("run %+v", r)
	}
	if f.get(x.ID).Status != memory.StatusActive || f.get(y.ID).Status != memory.StatusActive {
		t.Fatal("facts changed")
	}
	var n int
	f.st.DB().QueryRow("SELECT COUNT(*) FROM memory_judgments").Scan(&n)
	if n != 0 {
		t.Fatalf("a rejected verdict was cached")
	}
	if list, _ := f.mem.Conflicts(f.ctx, f.env, ""); len(list) != 0 {
		t.Fatalf("conflicts %+v", list)
	}
}

// sixFacts writes six related facts of one scope: 15 pairs.
func (f *fixture) sixFacts(scope string) []memory.Fact {
	var out []memory.Fact
	for i, s := range []string{"listens on port 80", "listens on port 81", "is written in Rust", "stores data in Redis", "deploys on Fridays", "has an on-call rotation"} {
		out = append(out, f.mk(scope, "The billing service "+s, memory.TierUser, "", "billing", "", day(i+1)))
	}
	return out
}

func TestBudgetStopsDreamCleanly(t *testing.T) {
	f := newFixture(t)
	ada := memory.PersonaScope(f.ada.ID)
	facts := f.sixFacts(ada)
	f.llm.respond = judge()
	f.svc.Budget = Budget{MaxCostMicros: 16_000, MaxCalls: 200} // three calls of $0.002 with headroom for the estimate
	r := f.dream(ada)
	if r.Status != DreamStopped || !strings.Contains(r.Note, "budget") || r.Stats.Pairs != 15 || r.Stats.Judged != 3 || f.llm.n() != 3 || r.FinishedAt == nil {
		t.Fatalf("run %+v, %d calls", r, f.llm.n())
	}
	if u, _ := f.svc.usage(f.ctx, f.env); u.Skipped != 1 || u.Calls != 3 {
		t.Fatalf("usage %+v", u)
	}
	for _, x := range facts {
		if f.get(x.ID).Status != memory.StatusActive {
			t.Fatal("a fact changed")
		}
	}
	// With more budget the next run covers the same window and reuses the verdicts.
	f.svc.Budget = DefaultBudget
	r = f.dream(ada)
	if r.Status != DreamDone || r.Stats.Cached != 3 || r.Stats.Judged != 12 || f.llm.n() != 15 || !r.WindowStart.Equal(time.Unix(0, 0).UTC()) {
		t.Fatalf("second run %+v", r)
	}
}

func TestInterruptedDreamResumesIdempotently(t *testing.T) {
	f := newFixture(t)
	ada := memory.PersonaScope(f.ada.ID)
	f.sixFacts(ada)
	ctx, cancel := context.WithCancel(f.ctx)
	defer cancel()
	rule := judge([3]string{"port 80", "port 81", VerdictContradiction})
	calls := 0
	f.llm.respond = func(r Request) (string, error) {
		calls++
		if calls == 4 {
			cancel() // Studio shuts down while the fourth pair is judged
		}
		return rule(r)
	}
	if _, err := f.svc.Dream(ctx, f.env, ada, TriggerManual); !errors.Is(err, context.Canceled) {
		t.Fatalf("interrupted: %v", err)
	}
	runs, _ := f.svc.DreamRuns(f.ctx, f.env, ada, 10)
	if len(runs) != 1 || runs[0].Status != DreamRunning {
		t.Fatalf("runs %+v", runs)
	}
	r := f.dream(ada)
	if r.ID != runs[0].ID || r.Resumes != 1 || r.Status != DreamDone {
		t.Fatalf("resumed %+v", r)
	}
	if f.llm.n() > 16 {
		t.Fatalf("%d calls for 15 pairs", f.llm.n())
	}
	conflicts, _ := f.mem.Conflicts(f.ctx, f.env, ada)
	if len(conflicts) != 1 || len(f.pending(ConflictApprovalKind)) != 1 {
		t.Fatalf("conflicts %d approvals %d", len(conflicts), len(f.pending(ConflictApprovalKind)))
	}
	page, err := f.mem.PageBySlug(f.ctx, f.env, ada, "topic/billing")
	if err != nil || strings.Count(page.Compiled, "<!-- studio:compiled") != 1 {
		t.Fatalf("page %q %v", page.Compiled, err)
	}
}

func TestDreamScopeIsolation(t *testing.T) {
	f := newFixture(t)
	ada, bob := memory.PersonaScope(f.ada.ID), memory.PersonaScope(f.bob.ID)
	a1 := f.mk(ada, "The api uses REST", memory.TierUser, "", "api", "api.style", day(1))
	a2 := f.mk(ada, "The api uses gRPC", memory.TierUser, "", "api", "api.style", day(2))
	b1 := f.mk(bob, "The api uses GraphQL", memory.TierUser, "", "api", "api.style", day(1))
	b2 := f.mk(bob, "The api uses SOAP", memory.TierUser, "", "api", "api.style", day(2))
	f.llm.respond = func(Request) (string, error) { return `{"verdict": "contradiction", "reason": "they differ"}`, nil }
	if r := f.dream(ada); r.Stats.Conflicts != 1 {
		t.Fatalf("run %+v", r)
	}
	for _, c := range f.llm.calls {
		if strings.Contains(c.User, "GraphQL") || strings.Contains(c.User, "SOAP") {
			t.Fatal("bob's facts reached ada's dream")
		}
	}
	for _, x := range []memory.Fact{b1, b2} {
		if got := f.get(x.ID); got.Status != memory.StatusActive || !got.UpdatedAt.Equal(x.UpdatedAt) {
			t.Fatalf("bob's fact changed: %+v", got)
		}
	}
	if list, _ := f.mem.Conflicts(f.ctx, f.env, bob); len(list) != 0 {
		t.Fatalf("bob conflicts %+v", list)
	}
	if pages, _ := f.mem.Pages(f.ctx, f.env, bob); len(pages) != 0 {
		t.Fatalf("bob pages %+v", pages)
	}
	if f.get(a1.ID).Status != memory.StatusDisputed || f.get(a2.ID).Status != memory.StatusDisputed {
		t.Fatal("ada's facts not disputed")
	}
}

func TestDreamWithoutUtilityModel(t *testing.T) {
	f := newFixture(t)
	bob := memory.PersonaScope(f.bob.ID)
	keep := f.mk(bob, "Bob prefers dark mode in the editor", memory.TierInferred, f.bob.ID, "editor", "", day(1))
	f.mk(bob, "Bob prefers dark mode in the editor.", memory.TierUser, "", "editor", "", day(2))
	f.mk(bob, "Bob uses vim keybindings in the editor", memory.TierUser, "", "editor", "", day(3))
	r := f.dream(bob)
	if r.Status != DreamDone || r.Stats.Merged != 1 || f.llm.n() != 0 || !strings.Contains(r.Note, "subscriptions") || r.Stats.Pages != 1 {
		t.Fatalf("run %+v", r)
	}
	if got := f.get(keep.ID); got.SupportCount != 2 || got.Tier != memory.TierUser {
		t.Fatalf("merged %+v", got)
	}
}

func TestSharedSupersedesPersonaInPersonaDream(t *testing.T) {
	f := newFixture(t)
	ada := memory.PersonaScope(f.ada.ID)
	old := f.mk(ada, "Staging is at staging.old.example", memory.TierInferred, f.ada.ID, "staging", "staging.url", day(1))
	f.dream(ada) // old is not new any more
	newer := f.mk(memory.SharedScope, "Staging is at staging.new.example", memory.TierUser, "", "staging", "staging.url", day(5))
	f.llm.respond = judge([3]string{"old.example", "new.example", VerdictSupersedes})
	r := f.dream(ada)
	if r.Stats.Superseded != 1 || f.get(old.ID).Status != memory.StatusSuperseded || f.get(newer.ID).Status != memory.StatusActive {
		t.Fatalf("run %+v", r)
	}
	// The other way round, a newer persona fact never replaces a shared one by itself.
	f2 := newFixture(t)
	ada2 := memory.PersonaScope(f2.ada.ID)
	s := f2.mk(memory.SharedScope, "Staging is at staging.old.example", memory.TierUser, "", "staging", "staging.url", day(1))
	p := f2.mk(ada2, "Staging is at staging.new.example", memory.TierUser, f2.ada.ID, "staging", "staging.url", day(5))
	f2.llm.respond = judge([3]string{"old.example", "new.example", VerdictSupersedes})
	r = f2.dream(ada2)
	if r.Stats.Superseded != 0 || r.Stats.Conflicts != 1 || f2.get(s.ID).Status != memory.StatusActive || f2.get(p.ID).Status != memory.StatusDisputed {
		t.Fatalf("persona over shared %+v", r)
	}
	if c, _ := f2.mem.Conflicts(f2.ctx, f2.env, ""); c[0].Verdict != "temporal_supersession" {
		t.Fatalf("verdict %s", c[0].Verdict)
	}
}

func TestLowerTierNewerFactOpensConflict(t *testing.T) {
	f := newFixture(t)
	ada := memory.PersonaScope(f.ada.ID)
	f.mk(ada, "Releases are tagged vMAJOR.MINOR", memory.TierUser, "", "release", "release.tag", day(1))
	f.mk(ada, "Releases are tagged with dates", memory.TierInferred, f.ada.ID, "release", "release.tag", day(9))
	f.llm.respond = judge([3]string{"MAJOR", "dates", VerdictSupersedes})
	r := f.dream(ada)
	if r.Stats.Superseded != 0 || r.Stats.Conflicts != 1 || len(f.pending(ConflictApprovalKind)) != 1 {
		t.Fatalf("run %+v", r)
	}
}

func TestDreamLockAndManualStart(t *testing.T) {
	f := newFixture(t)
	ada := memory.PersonaScope(f.ada.ID)
	if !f.svc.lockScope(f.env, ada) {
		t.Fatal("lock")
	}
	if _, err := f.svc.Dream(f.ctx, f.env, ada, TriggerManual); !errors.Is(err, ErrDreamRunning) {
		t.Fatalf("second dream: %v", err)
	}
	if _, err := f.svc.StartDream(f.ctx, f.env, ada); !errors.Is(err, ErrDreamRunning) {
		t.Fatalf("start: %v", err)
	}
	f.svc.unlockScope(f.env, ada)
	if _, err := f.svc.Dream(f.ctx, f.env, "persona:nobody", TriggerManual); !errors.Is(err, memory.ErrInvalid) {
		t.Fatalf("unknown persona: %v", err)
	}
	r, err := f.svc.StartDream(f.ctx, f.env, ada)
	if err != nil || r.Status != DreamRunning || r.Trigger != TriggerManual {
		t.Fatalf("start %+v %v", r, err)
	}
	deadline := time.Now().Add(5 * time.Second)
	for {
		runs, _ := f.svc.DreamRuns(f.ctx, f.env, ada, 1)
		if len(runs) == 1 && runs[0].Status == DreamDone {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("runs %+v", runs)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func TestDueDreams(t *testing.T) {
	f := newFixture(t)
	ada := memory.PersonaScope(f.ada.ID)
	for i := range DreamAfterFacts {
		f.mk(ada, fmt.Sprintf("Fact number %d", i), memory.TierUser, "", "", "", day(1))
	}
	f.mk(memory.SharedScope, "One shared fact", memory.TierUser, "", "", "", day(1))
	due := func() map[string]string {
		list, err := f.svc.dueDreams(f.ctx)
		if err != nil {
			t.Fatal(err)
		}
		out := map[string]string{}
		for _, d := range list {
			out[d.scope] = d.trigger
		}
		return out
	}
	f.now = time.Date(2026, 10, 10, 1, 0, 0, 0, time.Local) // before the nightly hour
	if got := due(); len(got) != 1 || got[ada] != TriggerFacts {
		t.Fatalf("due at 01:00: %v", got)
	}
	f.now = time.Date(2026, 10, 10, 4, 0, 0, 0, time.Local)
	if got := due(); len(got) != 2 || got[ada] != TriggerFacts || got[memory.SharedScope] != TriggerNightly {
		t.Fatalf("due at 04:00: %v", got)
	}
	f.svc.DreamDue(f.ctx)
	if got := due(); len(got) != 0 {
		t.Fatalf("due after running: %v", got)
	}
	runs, _ := f.svc.DreamRuns(f.ctx, f.env, "", 10)
	if len(runs) != 2 {
		t.Fatalf("runs %+v", runs)
	}
	// An interrupted run is due again at once.
	if _, err := f.st.DB().Exec("UPDATE memory_dream_runs SET status = 'running', finished_at = NULL WHERE scope = ?", ada); err != nil {
		t.Fatal(err)
	}
	if got := due(); got[ada] != "" || len(got) != 1 {
		t.Fatalf("resume due: %v", got)
	}
}

func TestConflictApprovalDecisions(t *testing.T) {
	f := newFixture(t)
	ada := memory.PersonaScope(f.ada.ID)
	f.mk(ada, "Use tabs", memory.TierUser, "", "style", "indent", day(1))
	f.mk(ada, "Use spaces", memory.TierUser, "", "style", "indent", day(2))
	f.llm.respond = judge([3]string{"tabs", "spaces", VerdictContradiction})
	f.dream(ada)
	a := f.pending(ConflictApprovalKind)[0]
	var decide func(context.Context, store.Approval, integrations.Decision) error
	for _, k := range f.svc.ApprovalKinds() {
		if k.Kind == ConflictApprovalKind {
			decide = k.Decide
		}
	}
	if err := decide(f.ctx, a, integrations.Decision{Action: integrations.Allow}); !errors.Is(err, integrations.ErrAction) {
		t.Fatalf("allow: %v", err)
	}
	if err := decide(f.ctx, a, integrations.Decision{Action: integrations.Dismiss}); err != nil {
		t.Fatal(err)
	}
	if got, _ := f.st.Approval(f.ctx, f.env, a.ID); got.Status != store.StatusDismissed {
		t.Fatalf("status %s", got.Status)
	}
	c, _ := f.mem.Conflicts(f.ctx, f.env, ada)
	if c[0].Status != "open" {
		t.Fatal("dismissing resolved the conflict")
	}
	// Resolving after a dismiss still works.
	if _, err := f.svc.ResolveConflict(f.ctx, f.env, c[0].ID, memory.ConflictResolution{Resolution: "keep_b"}); err != nil {
		t.Fatal(err)
	}
}

func TestPromote(t *testing.T) {
	f := newFixture(t)
	mine := f.mk(memory.PersonaScope(f.ada.ID), "The api has rate limits of 100 rps", memory.TierVerified, f.ada.ID, "api", "", day(1))
	a, err := f.svc.Promote(f.ctx, f.env, mine.ID)
	if err != nil || a.Kind != ApprovalKind || a.Status != store.StatusPending || a.SandboxID != "" {
		t.Fatalf("promote %+v %v", a, err)
	}
	var p SharePayload
	if err := json.Unmarshal(a.Payload, &p); err != nil || p.FactID != mine.ID || p.PersonaID != f.ada.ID || p.Tier != memory.TierVerified {
		t.Fatalf("payload %+v", p)
	}
	fact, err := f.svc.DecideShare(f.ctx, a, true)
	if err != nil || fact.Scope != memory.SharedScope || fact.Text != mine.Text {
		t.Fatalf("approved %+v %v", fact, err)
	}
	if _, err := f.svc.Promote(f.ctx, f.env, fact.ID); !errors.Is(err, memory.ErrInvalid) {
		t.Fatalf("promote shared: %v", err)
	}
}
