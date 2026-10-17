package tui

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/lukaskoebe/sandbox-studio/internal/acp"
	"github.com/lukaskoebe/sandbox-studio/internal/agentmem"
	"github.com/lukaskoebe/sandbox-studio/internal/api"
	"github.com/lukaskoebe/sandbox-studio/internal/browser"
	"github.com/lukaskoebe/sandbox-studio/internal/chat"
	"github.com/lukaskoebe/sandbox-studio/internal/store"
)

// request is one call a recording Studio received.
type request struct {
	method, path, body string
}

// recorder is a Studio that records the calls it gets and answers each with the JSON set for
// its method and path, or with {}.
type recorder struct {
	mu       sync.Mutex
	requests []request
	answers  map[string]string // by "METHOD /path"
}

// recordingClient is a client of a recording Studio, for checking what the keys send.
func recordingClient(t *testing.T) (*Client, *recorder) {
	t.Helper()
	rec := &recorder{answers: map[string]string{}}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		rec.mu.Lock()
		rec.requests = append(rec.requests, request{method: r.Method, path: r.URL.Path, body: string(body)})
		answer, ok := rec.answers[r.Method+" "+r.URL.Path]
		rec.mu.Unlock()
		if !ok {
			answer = "{}"
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, answer)
	}))
	t.Cleanup(srv.Close)
	return &Client{addr: strings.TrimPrefix(srv.URL, "http://"), http: srv.Client()}, rec
}

// last is the last call the recorder got.
func (r *recorder) last(t *testing.T) request {
	t.Helper()
	r.mu.Lock()
	defer r.mu.Unlock()
	if len(r.requests) == 0 {
		t.Fatal("no call reached Studio")
	}
	return r.requests[len(r.requests)-1]
}

// count is how many calls the recorder got.
func (r *recorder) count() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.requests)
}

// pressRun presses a key and runs the command it starts, the way the program does.
func pressRun(t *testing.T, m *app, k string) *app {
	t.Helper()
	m, cmd := pressCmd(m, k)
	if cmd == nil {
		t.Fatalf("%s started nothing", k)
	}
	return run(m, cmd)
}

// kindApprovals are one pending approval of each kind the web decides on its own card.
func kindApprovals(now time.Time) []api.ApprovalView {
	at := func(id, kind string, ago time.Duration) store.Approval {
		return store.Approval{ID: id, EnvironmentID: "env1", SandboxID: "sb1", Kind: kind, Status: "pending", CreatedAt: now.Add(-ago)}
	}
	return []api.ApprovalView{
		{Approval: at("ba", "browser.action", time.Minute), BrowserAction: &browser.ActionPayload{
			TeammateID: "t1", TeammateName: "Iris", SandboxName: "blub", Action: "click", Origin: "https://shop.example", Label: "Buy now",
		}},
		{Approval: at("bc", "browser.credential", 2*time.Minute), BrowserCredential: &browser.CredentialPayload{
			TeammateID: "t1", TeammateName: "Iris", SandboxName: "blub", Credential: "SHOP_PASSWORD", Origin: "https://shop.example", Label: "Password",
		}},
		{Approval: at("ms", "memory.share", 3*time.Minute), MemoryShare: &agentmem.SharePayload{
			TeammateName: "Iris", Text: "The staging database is reset every night.", Kind: "fact", Tier: "agent",
		}},
		{Approval: at("mc", "memory.conflict", 4*time.Minute), MemoryConflict: &agentmem.ConflictPayload{
			ConflictID: "c9", Verdict: "contradiction",
			FactA: agentmem.ConflictSide{Text: "Deploys go out on Fridays.", Tier: "user"},
			FactB: agentmem.ConflictSide{Text: "Deploys never go out on Fridays.", Tier: "agent"},
		}},
		{Approval: at("cp", "chat.permission", 5*time.Minute), ChatPermission: &chat.PermissionPayload{
			ChatID: "c1", ChatTitle: "Fix the login form", TeammateName: "Iris", SandboxName: "blub", Title: "npm test",
			Options: []acp.PermissionOption{
				{OptionID: "o1", Name: "Allow once", Kind: acp.AllowOnce},
				{OptionID: "o2", Name: "Always allow", Kind: acp.AllowAlways},
				{OptionID: "o3", Name: "Reject", Kind: acp.RejectOnce},
			},
		}},
	}
}

func TestGoldenApprovalsKinds(t *testing.T) {
	m := sampleApp()
	m.approvals = kindApprovals(m.now)
	golden(t, "approvals_kinds", press(m, "a"))
}

// Each kind's decision has the body its web card sends.
func TestKindDecisionBodies(t *testing.T) {
	list := kindApprovals(time.Now())
	body := func(d Decision) string {
		data, err := json.Marshal(d)
		if err != nil {
			t.Fatal(err)
		}
		return string(data)
	}
	cases := []struct {
		a        api.ApprovalView
		action   string
		remember bool
		want     string
	}{
		{list[0], "allow", true, `{"action":"allow","scope":"teammate","remember":true}`},
		{list[0], "dismiss", true, `{"action":"dismiss","scope":"teammate"}`},
		{list[1], "allow", true, `{"action":"allow","scope":"teammate"}`},
		{list[2], "deny", false, `{"action":"deny","scope":"environment"}`},
		{list[3], "dismiss", false, `{"action":"dismiss","scope":"environment"}`},
		{list[4], "allow", true, `{"action":"allow","scope":"environment","remember":true}`},
		{list[4], "dismiss", true, `{"action":"dismiss","scope":"environment"}`},
	}
	for _, c := range cases {
		if got := body(kindDecision(c.a, c.action, c.remember)); got != c.want {
			t.Errorf("%s %s: %s, want %s", c.a.Kind, c.action, got, c.want)
		}
	}
}

// A permission's options become the decisions Studio maps back to them.
func TestPermissionPicksFollowTheOptions(t *testing.T) {
	picks := permissionPicks([]acp.PermissionOption{
		{Name: "Always allow", Kind: acp.AllowAlways}, {Name: "Reject", Kind: acp.RejectOnce},
		{Name: "Never", Kind: acp.RejectAlways}, {Name: "Odd", Kind: "something"},
	})
	if len(picks) != 3 {
		t.Fatalf("picks: %+v", picks)
	}
	if p := picks[0]; p.action != "allow" || !p.remember {
		t.Errorf("always allow: %+v", p)
	}
	if p := picks[1]; p.action != "deny" || p.remember {
		t.Errorf("reject once: %+v", p)
	}
	if p := picks[2]; p.action != "deny" || !p.remember {
		t.Errorf("reject always: %+v", p)
	}
}

// The keys of each kind's card send what the web's card sends.
func TestKindKeysDecideOnTheirCards(t *testing.T) {
	cases := []struct {
		name   string
		cursor int
		keys   []string
		path   string
		body   string
		notice string
	}{
		{"browser action, always", 0, []string{"r", "y"}, "/api/environments/env1/approvals/ba/decide",
			`{"action":"allow","scope":"teammate","remember":true}`, "Allowed the click on https://shop.example."},
		{"browser action, ignored", 0, []string{"i"}, "/api/environments/env1/approvals/ba/decide",
			`{"action":"dismiss","scope":"teammate"}`, "Ignored the click on https://shop.example."},
		{"credential fill", 1, []string{"y"}, "/api/environments/env1/approvals/bc/decide",
			`{"action":"allow","scope":"teammate"}`, "Filling SHOP_PASSWORD on https://shop.example."},
		{"credential deny", 1, []string{"n"}, "/api/environments/env1/approvals/bc/decide",
			`{"action":"deny","scope":"teammate"}`, "Denied SHOP_PASSWORD on https://shop.example."},
		{"memory share", 2, []string{"y"}, "/api/environments/env1/approvals/ms/decide",
			`{"action":"allow","scope":"environment"}`, "Shared the fact with every teammate."},
		{"memory share declined", 2, []string{"n"}, "/api/environments/env1/approvals/ms/decide",
			`{"action":"deny","scope":"environment"}`, "Declined the share."},
		{"conflict keeps B", 3, []string{"2"}, "/api/environments/env1/memory/conflicts/c9/resolve",
			`{"resolution":"keep_b"}`, "Kept fact B; A is retracted."},
		{"conflict later", 3, []string{"i"}, "/api/environments/env1/approvals/mc/decide",
			`{"action":"dismiss","scope":"environment"}`, "Put off the conflict; it stays open on the memory page."},
		{"permission always", 4, []string{"2"}, "/api/environments/env1/approvals/cp/decide",
			`{"action":"allow","scope":"environment","remember":true}`, "Chose “Always allow” for npm test."},
		{"permission rejected", 4, []string{"3"}, "/api/environments/env1/approvals/cp/decide",
			`{"action":"deny","scope":"environment"}`, "Chose “Reject” for npm test."},
		{"permission ignored", 4, []string{"i"}, "/api/environments/env1/approvals/cp/decide",
			`{"action":"dismiss","scope":"environment"}`, "Ignored the permission request."},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			client, rec := recordingClient(t)
			m := sampleApp()
			m.client = client
			m.approvals = kindApprovals(m.now)
			m = press(m, "a")
			m.approvalCursor = c.cursor
			m = press(m, c.keys[:len(c.keys)-1]...)
			m = pressRun(t, m, c.keys[len(c.keys)-1])
			got := rec.last(t)
			if got.method != http.MethodPost || got.path != c.path || got.body != c.body {
				t.Errorf("sent %s %s %s, want POST %s %s", got.method, got.path, got.body, c.path, c.body)
			}
			if m.notice.text != c.notice || m.notice.failed {
				t.Errorf("notice %+v, want %q", m.notice, c.notice)
			}
		})
	}
}

// Enter on a memory conflict prints where its other resolutions are written; a key a card does
// not have sends nothing.
func TestKindCardsLeaveTheRestToTheBrowser(t *testing.T) {
	m := sampleApp()
	m.approvals = kindApprovals(m.now)
	m = press(m, "a")
	m.approvalCursor = 3
	m = press(m, "enter")
	if want := "open in browser: http://127.0.0.1:7878/e/env1/memory"; m.notice.text != want {
		t.Fatalf("enter on a conflict: %q", m.notice.text)
	}
	m.approvalCursor = 4
	if _, cmd := pressCmd(m, "7"); cmd != nil {
		t.Fatal("a number past the options decided")
	}
	m.approvalCursor = 1
	if _, cmd := pressCmd(m, "r"); cmd != nil {
		t.Fatal("r decided a credential")
	}
}

// pendingAsks puts a pending permission request of Iris, and after it a question, into the chat
// on show.
func pendingAsks(m *app, schema string) *app {
	perm := item("p1", 6, chat.KindPermission)
	perm.Status, perm.ApprovalID = chat.StatusPending, "ap1"
	perm.Permission = &chat.PermissionBody{
		ToolCall: acp.ToolCallUpdate{Title: "npm test"},
		Options: []acp.PermissionOption{
			{OptionID: "o1", Name: "Allow once", Kind: acp.AllowOnce},
			{OptionID: "o2", Name: "Reject", Kind: acp.RejectOnce},
		},
	}
	q := item("q1", 7, chat.KindQuestion)
	q.Status = chat.StatusPending
	q.Question = &chat.QuestionBody{Message: "Which port should the dev server use?", Schema: json.RawMessage(schema)}
	m.chats.items[perm.ID], m.chats.items[q.ID] = perm, q
	return m
}

func TestGoldenChatAsks(t *testing.T) {
	golden(t, "chat_asks", pendingAsks(sampleChat(sampleApp()), `{"type":"object","properties":{"port":{"type":"integer","enum":[3000,8080]}}}`))
}

// The number keys answer the permission the chat waits on first, then its question.
func TestChatAnswersItsAsksInPlace(t *testing.T) {
	client, rec := recordingClient(t)
	m := pendingAsks(sampleChat(sampleApp()), `{"type":"object","properties":{"port":{"type":"integer","enum":[3000,8080]}}}`)
	m.client = client
	m = pressRun(t, m, "1")
	if got := rec.last(t); got.path != "/api/environments/env1/approvals/ap1/decide" || got.body != `{"action":"allow","scope":"environment"}` {
		t.Fatalf("permission: %+v", got)
	}
	if m.notice.text != "Chose “Allow once” for npm test." {
		t.Fatalf("permission notice: %q", m.notice.text)
	}

	// The stream settles the permission; the question is next.
	perm := m.chats.items["p1"]
	perm.Status = chat.StatusApproved
	m.chats.items["p1"] = perm
	m = pressRun(t, m, "2")
	if got := rec.last(t); got.path != "/api/environments/env1/chats/c1/items/q1/answer" || got.body != `{"action":"accept","content":{"port":8080}}` {
		t.Fatalf("answer: %+v", got)
	}
	pressRun(t, m, "i")
	if got := rec.last(t); got.body != `{"action":"decline"}` {
		t.Fatalf("skip: %+v", got)
	}
}

// A question that asks for text is answered on the notice line; one with several fields is left
// to the browser.
func TestChatQuestionsTakeTypedAnswers(t *testing.T) {
	client, rec := recordingClient(t)
	m := pendingAsks(sampleChat(sampleApp()), `{"type":"object","properties":{"name":{"type":"string"}}}`)
	m.client = client
	delete(m.chats.items, "p1")
	m = press(m, "r", "w", "e", "b")
	if m.typing == nil || !strings.Contains(stripANSI(m.noticeLine(80)), "Answer: web") {
		t.Fatalf("r opens no answer line: %q", stripANSI(m.noticeLine(80)))
	}
	m = pressRun(t, m, "enter")
	if got := rec.last(t); got.body != `{"action":"accept","content":{"name":"web"}}` || m.typing != nil {
		t.Fatalf("typed answer: %+v, typing %v", got, m.typing != nil)
	}

	m = pendingAsks(m, `{"type":"object","properties":{"a":{"type":"string"},"b":{"type":"string"}}}`)
	delete(m.chats.items, "p1")
	before := rec.count()
	m = press(m, "1")
	if rec.count() != before || !strings.HasPrefix(m.notice.text, "open in browser: ") {
		t.Fatalf("a form answered in the terminal: notice %q", m.notice.text)
	}
}

func TestQuestionFormReadsTheSchema(t *testing.T) {
	cases := []struct {
		schema string
		ok     bool
		kind   string
		picks  int
	}{
		{``, true, "", 1},
		{`{"type":"object","properties":{}}`, true, "", 1},
		{`{"type":"object","properties":{"go":{"type":"boolean"}}}`, true, "bool", 2},
		{`{"type":"object","properties":{"n":{"type":["null","number"]}}}`, true, "number", 0},
		{`{"type":"object","properties":{"tags":{"type":"array","items":{"enum":["a","b"]}}}}`, false, "", 0},
	}
	for _, c := range cases {
		f, ok := questionForm(json.RawMessage(c.schema))
		if ok != c.ok || f.kind != c.kind || len(f.picks) != c.picks {
			t.Errorf("%s: ok %v, kind %q, %d picks", c.schema, ok, f.kind, len(f.picks))
		}
	}
}

// The approvals screen's key bar follows the card's kind.
func TestKindBindingsFollowTheCard(t *testing.T) {
	m := sampleApp()
	m.approvals = kindApprovals(m.now)
	m = press(m, "a")
	var bars []string
	for i := range m.approvals {
		m.approvalCursor = i
		bars = append(bars, stripANSI(m.keyBar(m.width, 0)))
	}
	for i, want := range []string{"r always", "y fill", "y share", "1 keep A", "1-3 choose"} {
		if !strings.Contains(bars[i], want) {
			t.Errorf("card %d: %q, want %q", i, bars[i], want)
		}
	}
}
