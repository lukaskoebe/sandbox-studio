package harness

import (
	"encoding/json"
	"flag"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/lukaskoebe/sandbox-studio/internal/acp"
	"github.com/lukaskoebe/sandbox-studio/internal/agentproto"
	"github.com/lukaskoebe/sandbox-studio/internal/teammates"
)

var update = flag.Bool("update", false, "rewrite the golden files")

const placeholder = "studio-0123456789abcdef0123456789abcdef"

var teammate = Teammate{
	Name:     "Ada",
	Role:     "Backend engineer",
	Soul:     "Careful and terse.\n\nNever writes " + agentproto.BlockEnd + " twice.",
	GitName:  "Ada Lovelace",
	GitEmail: "ada@example.com",
}

var providers = map[string]Provider{
	teammates.KindAnthropicAPI: {Kind: teammates.KindAnthropicAPI, EnvVar: "ANTHROPIC_API_KEY", Placeholder: placeholder},
	teammates.KindOpenAIAPI:    {Kind: teammates.KindOpenAIAPI, EnvVar: "OPENAI_API_KEY", Placeholder: placeholder},
	teammates.KindOpenAICompatible: {Kind: teammates.KindOpenAICompatible, BaseURL: "https://llm.example.com/v1?x=1&y=2",
		Model: "qwen3-coder:30b", EnvVar: "OPENAI_API_KEY", Placeholder: placeholder},
}

// TestGolden renders every harness with every API-key provider kind it supports and
// compares the files and environment with testdata/<harness>-<kind>.golden. Run
// `go test ./internal/harness -update` to rewrite them after a deliberate change.
func TestGolden(t *testing.T) {
	for _, h := range all {
		for kind, provider := range providers {
			if !teammates.Supports(kind, h.Name()) {
				if _, err := Files(h, teammate, Sandbox{Name: "api"}, provider); err == nil {
					t.Errorf("%s rendered for %s", h.Name(), kind)
				}
				continue
			}
			t.Run(h.Name()+"-"+kind, func(t *testing.T) {
				p := teammate
				if kind != teammates.KindOpenAICompatible {
					p.Model = "model-1"
				}
				files, err := Files(h, p, Sandbox{Name: "api"}, provider)
				if err != nil {
					t.Fatal(err)
				}
				env := Env(provider)
				if !maps.Equal(env, map[string]string{provider.EnvVar: placeholder}) {
					t.Fatalf("env: %v", env)
				}
				acpEnv := ACPEnvironment(h, provider)
				checkACPEnv(t, acpEnv)
				got := dump(files, env, h.TUICommand(SessionOpts{Workdir: "/workspace"})) +
					dumpACP(h.ACPCommand(), acpEnv, h.ACPMCPServers())
				// No file carries a credential, not even the placeholder: the harness reads
				// it from the environment.
				for _, f := range files {
					if strings.Contains(f.Content, "studio-0123") || strings.Contains(f.Content, "sk-") {
						t.Errorf("%s carries a credential", f.Path)
					}
				}
				golden := filepath.Join("testdata", h.Name()+"-"+kind+".golden")
				if *update {
					if err := os.WriteFile(golden, []byte(got), 0o644); err != nil {
						t.Fatal(err)
					}
				}
				want, err := os.ReadFile(golden)
				if err != nil {
					t.Fatal(err)
				}
				if got != string(want) {
					t.Errorf("%s differs; run with -update and review the diff:\n%s", golden, got)
				}
			})
		}
	}
}

func dump(files []GuestFile, env map[string]string, cmd []string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "== command\n%q\n", cmd)
	for k, v := range env {
		fmt.Fprintf(&b, "== env %s=%s\n", k, v)
	}
	for _, f := range files {
		fmt.Fprintf(&b, "== %s mode=%o block=%t\n%s", f.Path, f.Mode, f.Block, f.Content)
	}
	return b.String()
}

// dumpACP is the chat side of a golden: the ACP command, its environment sorted, and the
// MCP servers of session/new.
func dumpACP(cmd []string, env map[string]string, servers []acp.MCPServer) string {
	var b strings.Builder
	fmt.Fprintf(&b, "== acp command\n%q\n", cmd)
	for _, k := range slices.Sorted(maps.Keys(env)) {
		fmt.Fprintf(&b, "== acp env %s=%s\n", k, env[k])
	}
	for _, m := range servers {
		fmt.Fprintf(&b, "== acp mcp %s: %s %q\n", m.Name, m.Command, m.Args)
	}
	return b.String()
}

// acpConstants are the only values besides placeholders an ACP agent's environment may
// hold: paths and modes, nothing secret.
var acpConstants = map[string]string{
	"CLAUDE_CODE_EXECUTABLE": "/usr/local/bin/claude",
	"CODEX_PATH":             "/usr/local/bin/codex",
	"NO_BROWSER":             "1",
	"INITIAL_AGENT_MODE":     "agent-full-access",
	"MODEL_PROVIDER":         "studio",
}

// checkACPEnv: an ACP agent sees placeholders and acpConstants, never anything else.
func checkACPEnv(t *testing.T, env map[string]string) {
	t.Helper()
	for k, v := range env {
		if v == placeholder {
			continue
		}
		if want, ok := acpConstants[k]; !ok || v != want {
			t.Errorf("ACP env %s=%s is neither a placeholder nor an allowed constant", k, v)
		}
	}
}

func TestACPEnvSubscription(t *testing.T) {
	// No placeholder, no credential variable: only the constants.
	for _, h := range all {
		env := ACPEnvironment(h, Provider{Kind: teammates.KindChatGPTSubscription, EnvVar: "OPENAI_API_KEY"})
		checkACPEnv(t, env)
		if _, ok := env["OPENAI_API_KEY"]; ok {
			t.Errorf("%s: credential variable without a placeholder", h.Name())
		}
	}
}

func TestInstructionsBlock(t *testing.T) {
	f := instructions(teammate, Sandbox{Name: "api"})
	if strings.Count(f.Content, agentproto.BlockBegin) != 1 || strings.Count(f.Content, agentproto.BlockEnd) != 1 ||
		!strings.HasPrefix(f.Content, agentproto.BlockBegin) || !strings.HasSuffix(f.Content, agentproto.BlockEnd+"\n") {
		t.Fatalf("markers: %q", f.Content)
	}
	// The block carries the soul and points to the memory tools.
	if !strings.Contains(f.Content, "## Your soul\n\nCareful and terse.") || !strings.Contains(f.Content, "`remember`") || !f.Block {
		t.Fatalf("block: %+v", f)
	}
}

func TestSubscriptionProviders(t *testing.T) {
	// Sessions refuse providers that need a sign-in (api.planSession), but the adapters render
	// without a credential variable.
	sub := Provider{Kind: teammates.KindClaudeSubscription, EnvVar: "CLAUDE_CODE_OAUTH_TOKEN"}
	if len(Env(sub)) != 0 {
		t.Fatal("env without a placeholder")
	}
	if _, err := Files(Claude{}, teammate, Sandbox{Name: "x"}, sub); err != nil {
		t.Fatal(err)
	}
	files, err := Files(Codex{}, teammate, Sandbox{Name: "x"}, Provider{Kind: teammates.KindChatGPTSubscription})
	if err != nil || strings.Contains(files[0].Content, "model_provider") {
		t.Fatalf("codex subscription: %v %q", err, files[0].Content)
	}
}

func TestTOMLString(t *testing.T) {
	if got := tomlString("a\"b\\c\x01"); got != `"a\"b\\c\u0001"` {
		t.Fatal(got)
	}
}

func TestResumeCommand(t *testing.T) {
	want := map[string]string{
		teammates.HarnessOpenCode: "opencode --session ses_1",
		teammates.HarnessClaude:   "claude --resume ses_1 --mcp-config " + Home + "/" + claudeMCPConfig,
		teammates.HarnessCodex:    "codex resume ses_1 --dangerously-bypass-hook-trust",
	}
	for name, cmd := range want {
		h, _ := Lookup(name)
		if got := strings.Join(h.TUICommand(SessionOpts{Workdir: "/workspace", Resume: "ses_1"}), " "); got != cmd {
			t.Errorf("%s: %q", name, got)
		}
	}
}

func TestLookup(t *testing.T) {
	for _, name := range []string{teammates.HarnessOpenCode, teammates.HarnessClaude, teammates.HarnessCodex} {
		h, ok := Lookup(name)
		if !ok || h.Name() != name || len(h.StateDirs()) == 0 || len(h.TUICommand(SessionOpts{})) == 0 {
			t.Fatalf("%s: %v", name, h)
		}
		if _, err := h.ParseHook("x", nil); err == nil {
			t.Fatalf("%s parsed a hook", name)
		}
	}
	if _, ok := Lookup("aider"); ok {
		t.Fatal("unknown harness found")
	}
}

// TestRecordedACPInitialize reads the initialize reply spikes/acp recorded from the image's
// `opencode acp`: Studio relies on session/resume (no replay) and image prompts.
func TestRecordedACPInitialize(t *testing.T) {
	b, err := os.ReadFile("testdata/acp/opencode.json")
	if err != nil {
		t.Fatal(err)
	}
	var res acp.InitializeResponse
	if err := json.Unmarshal(b, &res); err != nil {
		t.Fatal(err)
	}
	caps := res.AgentCapabilities
	if res.ProtocolVersion != acp.ProtocolVersion || !caps.LoadSession || !caps.CanResume() || !caps.CanClose() || !caps.PromptCapabilities.Image {
		t.Fatalf("capabilities: %+v", res)
	}
}

func TestGitConfig(t *testing.T) {
	rw := []GitRewrite{
		{URL: "https://git.studio.internal/gh/", InsteadOf: []string{"https://github.com/", "git@github.com:"}},
		{URL: "https://git.studio.internal/bad/", InsteadOf: []string{"a\"b", "x\ny", "ssh://git@host/"}},
	}
	want := "# Managed by Sandbox Studio: rewritten when a session starts.\n" +
		"[url \"https://git.studio.internal/gh/\"]\n\tinsteadOf = \"https://github.com/\"\n\tinsteadOf = \"git@github.com:\"\n" +
		"[url \"https://git.studio.internal/bad/\"]\n\tinsteadOf = \"ssh://git@host/\"\n"
	if got := GitConfig(Teammate{}, rw); got.Content != want || got.Path != ".config/git/config" {
		t.Fatalf("got:\n%s\nwant:\n%s", got.Content, want)
	}
	h, _ := Lookup("claude")
	files, err := Files(h, Teammate{Name: "a"}, Sandbox{Name: "s", GitRewrites: rw}, Provider{Kind: "anthropic_api"})
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, f := range files {
		found = found || f.Path == ".config/git/config"
	}
	if !found {
		t.Fatal("rewrites without an identity wrote no git config")
	}
}
