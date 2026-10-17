package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"flag"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/lukaskoebe/sandbox-studio/internal/api"
	"github.com/lukaskoebe/sandbox-studio/internal/chat"
	"github.com/lukaskoebe/sandbox-studio/internal/policy"
	"github.com/lukaskoebe/sandbox-studio/internal/runtime"
	"github.com/lukaskoebe/sandbox-studio/internal/sandboxes"
	"github.com/lukaskoebe/sandbox-studio/internal/store"
	"github.com/lukaskoebe/sandbox-studio/internal/tui"
	"github.com/lukaskoebe/sandbox-studio/internal/webauth"
)

// fakeStudio answers the API calls the commands make, from fixed data. It records the bodies
// of the calls that change something.
type fakeStudio struct {
	mu      sync.Mutex
	calls   []string         // "METHOD path" of every API call after the sign-in
	bodies  map[string]any   // the decoded body of each changing call, by "METHOD path"
	polls   int              // GET calls of the chat so far
	answers []string         // the agent's text at each poll of the chat
	marker  *chat.MarkerBody // how the turn ends
}

var (
	policyRequest  = policy.NetworkRequest{}
	browserRequest = policy.NetworkRequest{Host: "cdn.example.com", Port: 443, HTTPRequest: policy.HTTPRequest{Method: "GET", Path: "/app.js", Site: "example.com"}}
)

const (
	envID = "env1"
	sbID  = "sb1"
)

func (f *fakeStudio) handler(t *testing.T) http.Handler {
	mux := http.NewServeMux()
	reply := func(w http.ResponseWriter, v any) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(v)
	}
	mux.HandleFunc("GET /api/auth/status", func(w http.ResponseWriter, r *http.Request) {
		reply(w, map[string]bool{"authenticated": false})
	})
	mux.HandleFunc("POST /api/auth/launch", func(w http.ResponseWriter, r *http.Request) {
		reply(w, map[string]string{"token": "ticket"})
	})
	mux.HandleFunc("POST /api/auth/exchange", func(w http.ResponseWriter, r *http.Request) {
		http.SetCookie(w, &http.Cookie{Name: webauth.SessionCookie, Value: "session"})
		w.WriteHeader(http.StatusNoContent)
	})
	mux.HandleFunc("POST /api/auth/logout", func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusNoContent) })
	started := time.Now().Add(-90 * time.Minute)
	finn := sandboxes.View{Sandbox: store.Sandbox{ID: sbID, EnvironmentID: envID, Name: "finn", TeammateID: "tm1", CPUs: 2, MemoryMiB: 4096, WorkspaceMiB: 20480, StartedAt: &started}, Status: runtime.StatusRunning}
	mux.HandleFunc("/api/", func(w http.ResponseWriter, r *http.Request) {
		key := r.Method + " " + r.URL.Path
		f.mu.Lock()
		f.calls = append(f.calls, key)
		if r.Method != http.MethodGet {
			var body any
			_ = json.NewDecoder(r.Body).Decode(&body)
			f.bodies[key] = body
		}
		f.mu.Unlock()
		switch key {
		case "GET /api/environments":
			reply(w, []store.Environment{{ID: envID, Name: "Default"}, {ID: "env2", Name: "Other"}})
		case "GET /api/environments/env1/sandboxes":
			reply(w, []sandboxes.View{finn})
		case "GET /api/environments/env2/sandboxes":
			reply(w, []sandboxes.View{})
		case "GET /api/environments/env1/sandboxes/sb1":
			reply(w, finn)
		case "GET /api/environments/env1/teammates":
			reply(w, []store.Teammate{{ID: "tm1", Name: "Finn"}})
		case "POST /api/environments/env1/sandboxes/sb1/stop":
			stopped := finn
			stopped.Status = runtime.StatusStopped
			reply(w, stopped)
		case "DELETE /api/environments/env1/sandboxes/sb1":
			w.WriteHeader(http.StatusNoContent)
		case "GET /api/approvals":
			reply(w, []api.ApprovalView{{
				Approval: store.Approval{ID: "ap1", EnvironmentID: envID, SandboxID: sbID, Kind: "network", Subject: "registry.npmjs.org:443", Status: "pending", CreatedAt: time.Now()},
				Network:  &policyRequest,
				Teammate: &api.ApprovalTeammate{ID: "tm1", Name: "Finn", ApprovalScope: "teammate"},
			}, {
				Approval: store.Approval{ID: "ap2", EnvironmentID: envID, SandboxID: sbID, Kind: "network.request", Subject: "cdn.example.com:443 example.com", Status: "pending", CreatedAt: time.Now()},
				Network:  &browserRequest,
				Teammate: &api.ApprovalTeammate{ID: "tm1", Name: "Finn", ApprovalScope: "teammate"},
			}})
		case "POST /api/environments/env1/approvals/ap1/decide":
			reply(w, api.ApprovalView{Approval: store.Approval{ID: "ap1", Status: "approved", Subject: "registry.npmjs.org:443"}})
		case "POST /api/environments/env1/approvals/ap2/decide":
			reply(w, api.ApprovalView{Approval: store.Approval{ID: "ap2", Status: "approved", Subject: "cdn.example.com:443 example.com"}})
		case "POST /api/environments/env1/sandboxes/sb1/chats":
			w.WriteHeader(http.StatusCreated)
			reply(w, store.Chat{ID: "ch1", EnvironmentID: envID, SandboxID: sbID, State: store.ChatRunning})
		case "GET /api/environments/env1/chats/ch1":
			reply(w, f.chatLog())
		default:
			t.Errorf("unexpected call %s", key)
			w.WriteHeader(http.StatusNotFound)
		}
	})
	return mux
}

// chatLog is the chat as the next poll sees it: the agent's text grows with each poll, then
// the turn ends.
func (f *fakeStudio) chatLog() chat.Log {
	f.mu.Lock()
	defer f.mu.Unlock()
	i := min(f.polls, len(f.answers)-1)
	f.polls++
	turn := chat.Turn{ChatTurn: store.ChatTurn{ID: "tu1", ChatID: "ch1", Seq: 1}}
	items := []chat.Item{
		{ChatItem: store.ChatItem{ID: "i1", TurnID: "tu1", Kind: chat.KindUser}, User: &chat.UserBody{Text: "run the tests"}},
		{ChatItem: store.ChatItem{ID: "i2", TurnID: "tu1", Kind: chat.KindTool}, Tool: &chat.ToolBody{Title: "pnpm test"}},
		{ChatItem: store.ChatItem{ID: "i3", TurnID: "tu1", Kind: chat.KindAgent}, Text: &chat.TextBody{Text: f.answers[i]}},
	}
	state := store.ChatRunning
	if i == len(f.answers)-1 {
		now := time.Now()
		turn.EndedAt = &now
		state = store.ChatIdle
		if f.marker != nil {
			items = append(items, chat.Item{ChatItem: store.ChatItem{ID: "i4", TurnID: "tu1", Kind: chat.KindMarker}, Marker: f.marker})
		}
	}
	return chat.Log{Chat: store.Chat{ID: "ch1", State: state}, Turns: []chat.Turn{turn}, Items: items}
}

// run runs a command against a fake Studio and returns its status, stdout and stderr.
func (f *fakeStudio) run(t *testing.T, stdin string, interactive bool, args ...string) (int, string, string) {
	t.Helper()
	if f.bodies == nil {
		f.bodies = map[string]any{}
	}
	srv := httptest.NewServer(f.handler(t))
	t.Cleanup(srv.Close)
	var stdout, stderr bytes.Buffer
	opts := Options{
		Addr:        strings.TrimPrefix(srv.URL, "http://"),
		Connect:     func(ctx context.Context, addr string) (*tui.Client, error) { return tui.Connect(ctx, addr, "control") },
		Stdin:       strings.NewReader(stdin),
		Stdout:      &stdout,
		Stderr:      &stderr,
		Getenv:      func(string) string { return "" },
		Interactive: interactive,
	}
	status := Run(context.Background(), opts, args)
	return status, stdout.String(), stderr.String()
}

func TestListShowsTheSandboxesWithTheirTeammates(t *testing.T) {
	f := &fakeStudio{}
	status, out, errOut := f.run(t, "", false, "ls")
	if status != ExitOK || !strings.Contains(out, "finn") || !strings.Contains(out, "Finn") || !strings.Contains(out, "1h 30m") {
		t.Fatalf("ls: %d\n%s\n%s", status, out, errOut)
	}
	status, out, _ = f.run(t, "", false, "ls", "--json")
	var views []sandboxes.View
	if status != ExitOK || json.Unmarshal([]byte(out), &views) != nil || len(views) != 1 || views[0].ID != sbID {
		t.Fatalf("ls --json: %d %s", status, out)
	}
	// --env picks an environment by name, after the arguments too.
	status, _, errOut = f.run(t, "", false, "ls", "--env", "other")
	if status != ExitOK || !strings.Contains(errOut, "No sandboxes yet") {
		t.Fatalf("ls --env other: %d %s", status, errOut)
	}
	if status, _, errOut = f.run(t, "", false, "ls", "--env", "nope"); status != ExitFailed || !strings.Contains(errOut, `no environment "nope"`) {
		t.Fatalf("ls --env nope: %d %s", status, errOut)
	}
}

func TestLifecycleFindsTheSandboxByName(t *testing.T) {
	f := &fakeStudio{}
	status, out, errOut := f.run(t, "", false, "stop", "finn")
	if status != ExitOK || out != "finn is stopped\n" {
		t.Fatalf("stop: %d %q %q", status, out, errOut)
	}
	if status, _, errOut = f.run(t, "", false, "stop", "nope"); status != ExitFailed || !strings.Contains(errOut, `no sandbox "nope"`) {
		t.Fatalf("stop nope: %d %s", status, errOut)
	}
	if status, _, _ = f.run(t, "", false, "stop"); status != ExitUsage {
		t.Fatalf("stop without a sandbox: %d", status)
	}
}

// Deleting asks for the sandbox's name in a terminal and needs --yes elsewhere.
func TestRemoveAsksFirst(t *testing.T) {
	f := &fakeStudio{}
	if status, _, errOut := f.run(t, "", false, "rm", "finn"); status != ExitUsage || !strings.Contains(errOut, "--yes") {
		t.Fatalf("rm in a script: %d %s", status, errOut)
	}
	if status, _, _ := f.run(t, "fin\n", true, "rm", "finn"); status != ExitFailed {
		t.Fatalf("rm with the wrong name typed: %d", status)
	}
	if slicesContain(f.calls, "DELETE /api/environments/env1/sandboxes/sb1") {
		t.Fatal("deleted without the confirmation")
	}
	if status, out, _ := f.run(t, "finn\n", true, "rm", "finn"); status != ExitOK || out != "Deleted finn\n" {
		t.Fatalf("rm confirmed: %d %q", status, out)
	}
	if status, _, _ := f.run(t, "", false, "rm", "--yes", "finn"); status != ExitOK {
		t.Fatalf("rm --yes: %d", status)
	}
}

// approve finds the approval in any environment, and a network rule takes the teammate's usual
// scope unless --scope says otherwise; --once makes no rule.
func TestApproveDecidesWithTheTeammatesScope(t *testing.T) {
	f := &fakeStudio{}
	const decide = "POST /api/environments/env1/approvals/ap1/decide"
	if status, out, errOut := f.run(t, "", false, "approve", "ap1"); status != ExitOK || !strings.Contains(out, "approved") {
		t.Fatalf("approve: %d %s %s", status, out, errOut)
	}
	if got := f.bodies[decide]; !jsonEqual(got, map[string]any{"action": "allow", "scope": "teammate"}) {
		t.Fatalf("decide body %v", got)
	}
	f.run(t, "", false, "deny", "ap1", "--once")
	if got := f.bodies[decide]; !jsonEqual(got, map[string]any{"action": "deny_once"}) {
		t.Fatalf("deny --once body %v", got)
	}
	f.run(t, "", false, "approve", "--scope", "sandbox", "ap1")
	if got := f.bodies[decide]; !jsonEqual(got, map[string]any{"action": "allow", "scope": "sandbox"}) {
		t.Fatalf("approve --scope body %v", got)
	}
	if status, _, errOut := f.run(t, "", false, "approve", "nope"); status != ExitFailed || !strings.Contains(errOut, "no pending approval") {
		t.Fatalf("approve nope: %d %s", status, errOut)
	}
}

// A browser request's rule covers only the browsers, with the scope the web card starts with,
// unless --sandboxes; --scope still picks the scope.
func TestApproveBrowserRequestCoversTheBrowsers(t *testing.T) {
	f := &fakeStudio{}
	const decide = "POST /api/environments/env1/approvals/ap2/decide"
	if status, out, errOut := f.run(t, "", false, "approve", "ap2"); status != ExitOK || !strings.Contains(out, "approved") {
		t.Fatalf("approve: %d %s %s", status, out, errOut)
	}
	if got := f.bodies[decide]; !jsonEqual(got, map[string]any{"action": "allow", "scope": "teammate", "browserOnly": true}) {
		t.Fatalf("decide body %v", got)
	}
	f.run(t, "", false, "approve", "--sandboxes", "ap2")
	if got := f.bodies[decide]; !jsonEqual(got, map[string]any{"action": "allow", "scope": "environment"}) {
		t.Fatalf("approve --sandboxes body %v", got)
	}
	f.run(t, "", false, "deny", "--scope", "sandbox", "ap2")
	if got := f.bodies[decide]; !jsonEqual(got, map[string]any{"action": "deny", "scope": "sandbox", "browserOnly": true}) {
		t.Fatalf("deny --scope body %v", got)
	}
	f.run(t, "", false, "approve", "--once", "ap2")
	if got := f.bodies[decide]; !jsonEqual(got, map[string]any{"action": "allow_once"}) {
		t.Fatalf("approve --once body %v", got)
	}
	if status, _, errOut := f.run(t, "", false, "approve", "--sandboxes", "ap1"); status != ExitUsage || !strings.Contains(errOut, "--sandboxes") {
		t.Fatalf("approve --sandboxes of a sandbox's request: %d %s", status, errOut)
	}
}

// ask prints the answer as it grows, names tool calls on stderr, and exits 1 when the turn failed.
func TestAskStreamsTheAnswer(t *testing.T) {
	f := &fakeStudio{answers: []string{"", "All 12 ", "All 12 tests pass.", "All 12 tests pass."}}
	status, out, errOut := f.run(t, "", false, "ask", "finn", "run", "the", "tests")
	if status != ExitOK || out != "All 12 tests pass.\n" {
		t.Fatalf("ask: %d %q %q", status, out, errOut)
	}
	if !strings.Contains(errOut, "chat ch1") || !strings.Contains(errOut, "› pnpm test") {
		t.Fatalf("stderr %q", errOut)
	}
	if got := f.bodies["POST /api/environments/env1/sandboxes/sb1/chats"]; !jsonEqual(got, map[string]any{"text": "run the tests"}) {
		t.Fatalf("prompt %v", got)
	}

	f = &fakeStudio{answers: []string{"Hm"}, marker: &chat.MarkerBody{Kind: chat.MarkerError, Text: "the model is down"}}
	status, out, errOut = f.run(t, "fix it", false, "ask", "finn", "-", "--json")
	var res map[string]any
	if status != ExitFailed || json.Unmarshal([]byte(out), &res) != nil || res["answer"] != "Hm" || !strings.Contains(errOut, "the model is down") {
		t.Fatalf("ask that fails: %d %q %q", status, out, errOut)
	}
	if got := f.bodies["POST /api/environments/env1/sandboxes/sb1/chats"]; !jsonEqual(got, map[string]any{"text": "fix it"}) {
		t.Fatalf("prompt from stdin %v", got)
	}
}

func TestParseTakesFlagsAnywhere(t *testing.T) {
	fs := flag.NewFlagSet("x", flag.ContinueOnError)
	env := fs.String("env", "", "")
	js := fs.Bool("json", false, "")
	rest, err := parse(fs, []string{"finn", "--json", "--env", "Other", "--", "--not-a-flag"})
	if err != nil || *env != "Other" || !*js || strings.Join(rest, " ") != "finn --not-a-flag" {
		t.Fatalf("parse: %v %q %v %v", rest, *env, *js, err)
	}
}

func TestCleanEscapesControlCharacters(t *testing.T) {
	if got := clean("ok\n\x1b]0;pwned\x07\tend"); got != "ok\n\\x1b]0;pwned\\x07\tend" {
		t.Fatalf("clean: %q", got)
	}
}

func TestCompletion(t *testing.T) {
	f := &fakeStudio{}
	if _, out, _ := f.run(t, "", false, "__complete", "st"); out != "start\nstop\n" {
		t.Fatalf("commands: %q", out)
	}
	if _, out, _ := f.run(t, "", false, "__complete", "show", "f"); out != "finn\n" {
		t.Fatalf("sandboxes: %q", out)
	}
	if _, out, _ := f.run(t, "", false, "__complete", "ls", "--env", ""); out != "Default\nOther\n" {
		t.Fatalf("environments: %q", out)
	}
	if _, out, _ := f.run(t, "", false, "__complete", "fork", "finn", ""); out != "" {
		t.Fatalf("a new name gets no suggestions: %q", out)
	}
	for _, shell := range []string{"bash", "zsh", "fish"} {
		if status, out, _ := f.run(t, "", false, "completion", shell); status != ExitOK || !strings.Contains(out, "__complete") {
			t.Fatalf("completion %s: %d", shell, status)
		}
	}
}

func jsonEqual(a, b any) bool {
	x, _ := json.Marshal(a)
	y, _ := json.Marshal(b)
	return bytes.Equal(x, y)
}

func slicesContain(list []string, s string) bool {
	for _, v := range list {
		if v == s {
			return true
		}
	}
	return false
}

func TestHelpCoversEveryCommand(t *testing.T) {
	var b strings.Builder
	server := []ServerCommand{{Name: "doctor", Summary: "Check the machine.", Detail: "It checks KVM."}}
	global := flag.NewFlagSet("studio", flag.ContinueOnError)
	global.String("addr", "127.0.0.1:7878", "listen address")
	if !Help(&b, "", server, global) {
		t.Fatal("no help")
	}
	for _, want := range []string{"doctor       Check the machine.", "ls ", "completion", "-addr string", `"studio <command> -h"`} {
		if !strings.Contains(b.String(), want) {
			t.Errorf("the help lacks %q:\n%s", want, b.String())
		}
	}
	b.Reset()
	if !Help(&b, "doctor", server, global) || !strings.Contains(b.String(), "Usage: studio doctor\n\nCheck the machine.\n\nIt checks KVM.") {
		t.Errorf("doctor's help: %q", b.String())
	}
	b.Reset()
	if !Help(&b, "fork", nil, nil) || !strings.Contains(b.String(), "Usage: studio fork <sandbox> <name>") {
		t.Errorf("fork's help: %q", b.String())
	}
	if Help(&b, "nope", server, global) {
		t.Error("an unknown topic had help")
	}
}
