package harness

import (
	"flag"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"strings"
	"testing"

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
				got := dump(files, env, h.TUICommand(SessionOpts{Workdir: "/workspace"}))
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
	// Sessions refuse login_required providers until S8, but the adapters render without
	// a credential variable.
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
