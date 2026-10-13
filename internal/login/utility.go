package login

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/lukaskoebe/sandbox-studio/internal/agentmem"
	"github.com/lukaskoebe/sandbox-studio/internal/agentproto"
	"github.com/lukaskoebe/sandbox-studio/internal/store"
	"github.com/lukaskoebe/sandbox-studio/internal/teammates"
)

// utilityIdle is how long a utility VM may go without a call before it stops; the next call
// starts it again. utilityTimeout bounds one CLI call.
const (
	utilityIdle    = 10 * time.Minute
	utilityTimeout = 4 * time.Minute
)

// utilityUse is a utility VM and when it last ran a call.
type utilityUse struct {
	envID, kind, name string
	at                time.Time
}

// utilityKind and utilityVM name a subscription provider's utility VM: one per provider, in its
// environment, so each login's CLI and its token are kept apart.
func utilityKind(p store.Provider) string {
	if p.Kind == teammates.KindChatGPTSubscription {
		return store.SandboxKindCodexUtility
	}
	return store.SandboxKindClaudeUtility
}

func utilityVM(p store.Provider) string { return "utility-" + p.ID }

// Utility returns the utility model of a subscription provider as agentmem calls it: one-shot CLI
// runs in the provider's utility VM. Without a login the model is agentmem.ErrNoUtility.
func (s *Service) Utility(p store.Provider) (agentmem.LLM, agentmem.UtilityModel, error) {
	if k, _ := teammates.LookupKind(p.Kind); !k.Login {
		return nil, agentmem.UtilityModel{}, ErrNotSubscription
	}
	m, ok := agentmem.UtilityFor(p)
	if !ok {
		return nil, m, agentmem.ErrNoUtility
	}
	return cliLLM{s: s, p: p, model: m.Model}, m, nil
}

// cliLLM is a subscription's utility model (agentmem.LLM).
type cliLLM struct {
	s     *Service
	p     store.Provider
	model string
}

func (l cliLLM) Complete(ctx context.Context, r agentmem.Request) (agentmem.Response, error) {
	return l.s.complete(ctx, l.p, l.model, r)
}

// complete runs one call through the provider's CLI in its utility VM, which is started when
// needed. The CLI sees its token as a placeholder, which the gateway swaps in on the vendor's
// host; a ChatGPT login's auth file has placeholders too. The prompt goes in on stdin.
func (s *Service) complete(ctx context.Context, p store.Provider, model string, r agentmem.Request) (agentmem.Response, error) {
	if p.SecretID == "" || p.Placeholder == "" {
		return agentmem.Response{}, ErrNoLogin
	}
	sb, err := s.VMs.EnsureSystem(ctx, p.EnvironmentID, utilityKind(p), utilityVM(p))
	if err != nil {
		return agentmem.Response{}, fmt.Errorf("the utility VM didn't start: %w", err)
	}
	s.touchUtility(p)
	files, err := s.GuestFiles(ctx, p)
	if err != nil {
		return agentmem.Response{}, err
	}
	if len(files) > 0 {
		if err := s.Guest.WriteHomeFiles(ctx, sb.ID, files); err != nil {
			return agentmem.Response{}, fmt.Errorf("writing the login's auth file: %w", err)
		}
	}
	args, env := cliCall(p, model)
	res, err := s.Guest.Run(ctx, sb.ID, agentproto.RunRequest{
		Args: args, Env: env, Stdin: r.System + "\n\n" + r.User, TimeoutSec: int(utilityTimeout / time.Second),
	})
	if err != nil {
		return agentmem.Response{}, fmt.Errorf("the utility call failed: %w", err)
	}
	if res.ExitCode != 0 {
		return agentmem.Response{}, fmt.Errorf("the CLI exited with status %d: %s", res.ExitCode, cut(res.Stderr))
	}
	if p.Kind == teammates.KindChatGPTSubscription {
		return parseCodexAnswer(res.Stdout)
	}
	return parseClaudeAnswer(res.Stdout)
}

// cliCall is the command of a one-shot call and its environment: the prompt on stdin, the
// answer as JSON, and the token's placeholder. verify (S8): the flags of the pinned CLIs.
func cliCall(p store.Provider, model string) ([]string, map[string]string) {
	if p.Kind == teammates.KindChatGPTSubscription {
		return []string{"codex", "exec", "-m", model, "--json", "--skip-git-repo-check", "-"}, nil
	}
	return []string{"claude", "-p", "--model", model, "--output-format", "json"}, map[string]string{
		"CLAUDE_CODE_OAUTH_TOKEN":                  p.Placeholder,
		"DISABLE_AUTOUPDATER":                      "1",
		"CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC": "1",
	}
}

// parseClaudeAnswer reads the JSON `claude -p --output-format json` prints: the result object,
// which is the last one when the CLI prints a list of messages. verify (S8).
func parseClaudeAnswer(out string) (agentmem.Response, error) {
	var a struct {
		Type    string `json:"type"`
		IsError bool   `json:"is_error"`
		Result  string `json:"result"`
		Usage   struct {
			InputTokens  int `json:"input_tokens"`
			OutputTokens int `json:"output_tokens"`
		} `json:"usage"`
	}
	text := strings.TrimSpace(out)
	if strings.HasPrefix(text, "[") {
		var list []json.RawMessage
		if err := json.Unmarshal([]byte(text), &list); err != nil || len(list) == 0 {
			return agentmem.Response{}, errors.New("the CLI's answer is not a list of messages")
		}
		text = string(list[len(list)-1])
	}
	if err := json.Unmarshal([]byte(text), &a); err != nil {
		return agentmem.Response{}, fmt.Errorf("the CLI's answer is not JSON: %w", err)
	}
	if a.IsError {
		return agentmem.Response{}, fmt.Errorf("the CLI reported an error: %s", cut(a.Result))
	}
	if a.Result == "" {
		return agentmem.Response{}, errors.New("the CLI's answer is empty")
	}
	return agentmem.Response{Text: a.Result, InputTokens: a.Usage.InputTokens, OutputTokens: a.Usage.OutputTokens}, nil
}

// parseCodexAnswer reads the JSON lines `codex exec --json` prints: the last agent message is
// the answer, and the turn's usage is the call's. verify (S8): the event and item names of the
// pinned Codex.
func parseCodexAnswer(out string) (agentmem.Response, error) {
	var res agentmem.Response
	var answer string
	sc := bufio.NewScanner(strings.NewReader(out))
	sc.Buffer(make([]byte, 64<<10), 4<<20)
	for sc.Scan() {
		var ev struct {
			Type string `json:"type"`
			Item struct {
				Type string `json:"type"`
				Text string `json:"text"`
			} `json:"item"`
			Usage struct {
				InputTokens  int `json:"input_tokens"`
				OutputTokens int `json:"output_tokens"`
			} `json:"usage"`
		}
		if json.Unmarshal(sc.Bytes(), &ev) != nil {
			continue
		}
		switch {
		case ev.Type == "item.completed" && ev.Item.Type == "agent_message":
			answer = ev.Item.Text
		case ev.Type == "turn.completed":
			res.InputTokens, res.OutputTokens = ev.Usage.InputTokens, ev.Usage.OutputTokens
		case ev.Type == "turn.failed" || ev.Type == "error":
			return agentmem.Response{}, errors.New("the CLI reported an error")
		}
	}
	if err := sc.Err(); err != nil {
		return agentmem.Response{}, fmt.Errorf("reading the CLI's answer: %w", err)
	}
	if answer == "" {
		return agentmem.Response{}, errors.New("the CLI's answer has no message")
	}
	res.Text = answer
	return res, nil
}

// cut shortens a CLI's text for an error message.
func cut(s string) string {
	s = strings.TrimSpace(s)
	if len(s) > 200 {
		return s[:200] + "…"
	}
	return s
}

// touchUtility records a call on a provider's utility VM, so the idle stop counts from it.
func (s *Service) touchUtility(p store.Provider) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.idle[p.ID] = utilityUse{envID: p.EnvironmentID, kind: utilityKind(p), name: utilityVM(p), at: s.now()}
}

// stopIdleUtility stops the utility VMs that made no call for utilityIdle; the next call starts
// them again.
func (s *Service) stopIdleUtility(ctx context.Context) {
	now := s.now()
	var stale []utilityUse
	s.mu.Lock()
	for id, u := range s.idle {
		if now.Sub(u.at) >= utilityIdle {
			stale = append(stale, u)
			delete(s.idle, id)
		}
	}
	s.mu.Unlock()
	for _, u := range stale {
		if err := s.VMs.StopSystem(ctx, u.envID, u.kind, u.name); err != nil {
			s.Log.Warn("stopping an idle utility VM", "name", u.name, "err", err)
		}
	}
}
