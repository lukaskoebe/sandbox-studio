package login

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/lukaskoebe/sandbox-studio/internal/agentmem"
	"github.com/lukaskoebe/sandbox-studio/internal/agentproto"
	"github.com/lukaskoebe/sandbox-studio/internal/store"
	"github.com/lukaskoebe/sandbox-studio/internal/teammates"
)

// A Claude subscription's utility call runs `claude -p` in its utility VM. The token is a
// placeholder in the environment, and the prompt goes in on stdin.
func TestClaudeUtilityCallUsesThePlaceholder(t *testing.T) {
	ctx := context.Background()
	e := newTestEnv(t)
	p := e.provider(t, "Max", teammates.KindClaudeSubscription)
	if err := e.svc.storeKey(ctx, p, tokenText, []string{"api.anthropic.com"}); err != nil {
		t.Fatal(err)
	}
	p, _ = e.st.Provider(ctx, e.env, p.ID)
	e.guest.cli = agentproto.RunResult{Stdout: `{"type":"result","subtype":"success","is_error":false,"result":"{\"facts\":[]}","usage":{"input_tokens":12,"output_tokens":3}}`}

	llm, m, err := e.svc.Utility(p)
	if err != nil || m.Model != "haiku" {
		t.Fatalf("utility: %+v, %v", m, err)
	}
	resp, err := llm.Complete(ctx, agentmem.Request{System: "SYSTEM PROMPT", User: "USER TEXT"})
	if err != nil || resp.Text != `{"facts":[]}` || resp.InputTokens != 12 || resp.OutputTokens != 3 {
		t.Fatalf("answer %+v, %v", resp, err)
	}
	if len(e.guest.calls) != 1 {
		t.Fatalf("%d CLI calls", len(e.guest.calls))
	}
	call := e.guest.calls[0]
	if call.Args[0] != "claude" || !contains(call.Args, "haiku") {
		t.Fatalf("the call %v", call.Args)
	}
	if call.Env["CLAUDE_CODE_OAUTH_TOKEN"] != p.Placeholder {
		t.Fatalf("the token in the environment is %q; want the placeholder", call.Env["CLAUDE_CODE_OAUTH_TOKEN"])
	}
	if strings.Contains(call.Stdin, tokenText) || !strings.Contains(call.Stdin, "SYSTEM PROMPT") || !strings.Contains(call.Stdin, "USER TEXT") {
		t.Fatalf("the prompt on stdin: %q", call.Stdin)
	}
	if got := e.vms.ensured; len(got) != 1 || got[0] != "utility-"+p.ID {
		t.Fatalf("utility VMs started: %v", got)
	}
}

// A ChatGPT subscription's call writes its auth file of placeholders first, then runs
// `codex exec` and reads the last message.
func TestChatGPTUtilityCallWritesTheAuthFileFirst(t *testing.T) {
	ctx := context.Background()
	e := newTestEnv(t)
	p := chatGPTProvider(t, e, time.Now().Add(time.Hour))
	e.guest.cli = agentproto.RunResult{Stdout: strings.Join([]string{
		`{"type":"thread.started","thread_id":"t1"}`,
		`{"type":"item.completed","item":{"type":"agent_message","text":"{\"facts\":[]}"}}`,
		`{"type":"turn.completed","usage":{"input_tokens":5,"output_tokens":2}}`,
	}, "\n")}

	llm, m, err := e.svc.Utility(p)
	if err != nil || m.Model != "gpt-6-luna" {
		t.Fatalf("utility: %+v, %v", m, err)
	}
	resp, err := llm.Complete(ctx, agentmem.Request{System: "S", User: "U"})
	if err != nil || resp.Text != `{"facts":[]}` || resp.InputTokens != 5 || resp.OutputTokens != 2 {
		t.Fatalf("answer %+v, %v", resp, err)
	}
	e.guest.mu.Lock()
	written := e.guest.written
	e.guest.mu.Unlock()
	if len(written) != 1 || len(written[0]) != 1 || written[0][0].Path != ".codex/auth.json" {
		t.Fatalf("the auth file writes %+v", written)
	}
	if !strings.Contains(written[0][0].Content, p.Placeholder) || strings.Contains(written[0][0].Content, "rt_secretrefresh") {
		t.Fatalf("the auth file holds more than placeholders:\n%s", written[0][0].Content)
	}
	if len(e.guest.calls) != 1 || e.guest.calls[0].Args[0] != "codex" || !contains(e.guest.calls[0].Args, "gpt-6-luna") {
		t.Fatalf("the CLI calls %+v", e.guest.calls)
	}
}

// Failures reach agentmem as errors: a CLI error, a non-zero exit, a timeout and an answer that
// isn't one.
func TestUtilityFailuresAreErrors(t *testing.T) {
	ctx := context.Background()
	e := newTestEnv(t)
	p := e.provider(t, "Max", teammates.KindClaudeSubscription)
	if err := e.svc.storeKey(ctx, p, tokenText, []string{"api.anthropic.com"}); err != nil {
		t.Fatal(err)
	}
	p, _ = e.st.Provider(ctx, e.env, p.ID)
	llm, _, err := e.svc.Utility(p)
	if err != nil {
		t.Fatal(err)
	}
	for name, set := range map[string]func(){
		"exit status": func() { e.guest.cli = agentproto.RunResult{ExitCode: 1, Stderr: "boom"}; e.guest.clErr = nil },
		"timeout":     func() { e.guest.cli = agentproto.RunResult{}; e.guest.clErr = errors.New("claude timed out") },
		"error flag": func() {
			e.guest.cli = agentproto.RunResult{Stdout: `{"is_error":true,"result":"rate limited"}`}
			e.guest.clErr = nil
		},
		"not JSON": func() { e.guest.cli = agentproto.RunResult{Stdout: "hello"}; e.guest.clErr = nil },
		"empty":    func() { e.guest.cli = agentproto.RunResult{Stdout: `{"result":""}`}; e.guest.clErr = nil },
	} {
		set()
		if _, err := llm.Complete(ctx, agentmem.Request{System: "S", User: "U"}); err == nil {
			t.Errorf("%s: no error", name)
		}
	}
}

// A provider without a login has no utility model; a non-subscription is not a login's.
func TestUtilityNeedsALogin(t *testing.T) {
	ctx := context.Background()
	e := newTestEnv(t)
	p := e.provider(t, "Max", teammates.KindClaudeSubscription)
	if _, _, err := e.svc.Utility(p); !errors.Is(err, agentmem.ErrNoUtility) {
		t.Fatalf("a provider without a login: %v", err)
	}
	api, err := e.st.CreateProvider(ctx, store.Provider{ID: store.NewID(), EnvironmentID: e.env, Name: "Key", Kind: teammates.KindAnthropicAPI}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := e.svc.Utility(api); !errors.Is(err, ErrNotSubscription) {
		t.Fatalf("an API key provider: %v", err)
	}
}

// Utility VMs stop when they have made no call for ten minutes, and a recent call keeps them.
func TestIdleUtilityVMsStop(t *testing.T) {
	ctx := context.Background()
	e := newTestEnv(t)
	p := e.provider(t, "Max", teammates.KindClaudeSubscription)
	if err := e.svc.storeKey(ctx, p, tokenText, []string{"api.anthropic.com"}); err != nil {
		t.Fatal(err)
	}
	p, _ = e.st.Provider(ctx, e.env, p.ID)
	e.guest.cli = agentproto.RunResult{Stdout: `{"result":"ok"}`}
	llm, _, err := e.svc.Utility(p)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := llm.Complete(ctx, agentmem.Request{System: "S", User: "U"}); err != nil {
		t.Fatal(err)
	}
	start := e.svc.now()
	e.svc.Now = func() time.Time { return start.Add(9 * time.Minute) }
	e.svc.stopIdleUtility(ctx)
	if len(e.vms.stopped) != 0 {
		t.Fatalf("stopped after 9 minutes: %v", e.vms.stopped)
	}
	e.svc.Now = func() time.Time { return start.Add(11 * time.Minute) }
	e.svc.stopIdleUtility(ctx)
	if len(e.vms.stopped) != 1 || e.vms.stopped[0] != "utility-"+p.ID {
		t.Fatalf("stopped after 11 minutes: %v", e.vms.stopped)
	}
}

func contains(list []string, s string) bool {
	for _, x := range list {
		if x == s {
			return true
		}
	}
	return false
}
