package hostlogins

import (
	"encoding/base64"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func write(t *testing.T, path, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
}

func noEnv(string) string { return "" }

func TestFindChecksOnlyThatTheFilesExist(t *testing.T) {
	home := t.TempDir()
	f := Finder{Home: home, Getenv: noEnv}
	if got := f.Find(); got != (Found{}) {
		t.Fatalf("an empty home = %+v", got)
	}
	// Unreadable content still counts: Find doesn't read the files.
	write(t, filepath.Join(home, ".claude", ".credentials.json"), "not json")
	write(t, filepath.Join(home, ".codex", "auth.json"), "not json")
	write(t, filepath.Join(home, ".local", "share", "opencode", "auth.json"), "not json")
	if got := f.Find(); got != (Found{Claude: true, Codex: true, Opencode: true}) {
		t.Fatalf("Find = %+v", got)
	}

	// macOS keeps Claude's login in the keychain, with only ~/.claude.json on disk.
	home = t.TempDir()
	write(t, filepath.Join(home, ".claude.json"), "{}")
	if got := (Finder{Home: home, Getenv: noEnv}).Find(); !got.Claude {
		t.Fatalf("~/.claude.json alone = %+v", got)
	}
}

func TestFindHonoursTheCLIsVariables(t *testing.T) {
	home, other := t.TempDir(), t.TempDir()
	env := map[string]string{
		"CLAUDE_CONFIG_DIR": filepath.Join(other, "claude"),
		"CODEX_HOME":        filepath.Join(other, "codex"),
		"XDG_DATA_HOME":     filepath.Join(other, "data"),
	}
	f := Finder{Home: home, Getenv: func(k string) string { return env[k] }}
	// The default places don't count once a variable moves them.
	write(t, filepath.Join(home, ".codex", "auth.json"), "{}")
	if got := f.Find(); got != (Found{}) {
		t.Fatalf("Find = %+v", got)
	}
	write(t, filepath.Join(other, "claude", ".credentials.json"), "{}")
	write(t, filepath.Join(other, "codex", "auth.json"), "{}")
	write(t, filepath.Join(other, "data", "opencode", "auth.json"), "{}")
	if got := f.Find(); got != (Found{Claude: true, Codex: true, Opencode: true}) {
		t.Fatalf("Find = %+v", got)
	}
}

func jwt(payload string) string {
	enc := base64.RawURLEncoding.EncodeToString
	return enc([]byte(`{"alg":"RS256"}`)) + "." + enc([]byte(payload)) + ".c2ln"
}

func TestCodexAccountReadsTheIDTokensClaims(t *testing.T) {
	home := t.TempDir()
	f := Finder{Home: home, Getenv: noEnv}
	if _, err := f.CodexAccount(); !errors.Is(err, ErrNotFound) {
		t.Fatalf("no file: %v", err)
	}
	path := filepath.Join(home, ".codex", "auth.json")
	write(t, path, `{"OPENAI_API_KEY":"sk-test","tokens":null}`)
	if _, err := f.CodexAccount(); !errors.Is(err, ErrNotFound) {
		t.Fatalf("an API key login: %v", err)
	}
	id := jwt(`{"email":"ada@example.com","https://api.openai.com/auth":{"chatgpt_plan_type":"pro"}}`)
	write(t, path, `{"auth_mode":"chatgpt","tokens":{"id_token":"`+id+`","access_token":"a.b.c","refresh_token":"rt-secret"}}`)
	got, err := f.CodexAccount()
	if err != nil || got != (CodexAccount{Email: "ada@example.com", Plan: "pro"}) {
		t.Fatalf("CodexAccount = %+v, %v", got, err)
	}
	write(t, path, `{"tokens":{"id_token":"not-a-jwt"}}`)
	if _, err := f.CodexAccount(); err == nil {
		t.Fatal("a broken ID token was accepted")
	}
}

func TestOpencodeKeysImportsPlainKeysOnly(t *testing.T) {
	home := t.TempDir()
	f := Finder{Home: home, Getenv: noEnv}
	if _, err := f.OpencodeKeys(); !errors.Is(err, ErrNotFound) {
		t.Fatalf("no file: %v", err)
	}
	write(t, filepath.Join(home, ".local", "share", "opencode", "auth.json"), `{
		"openai": {"type": "oauth", "refresh": "rt", "access": "at", "expires": 1},
		"anthropic": {"type": "api", "key": " sk-ant-test "},
		"openrouter": {"type": "api", "key": "sk-or-test"},
		"broken": 3
	}`)
	keys, err := f.OpencodeKeys()
	if err != nil {
		t.Fatal(err)
	}
	want := []OpencodeKey{
		{Provider: "anthropic", Type: "api", Kind: "anthropic_api", Key: "sk-ant-test"},
		{Provider: "openai", Type: "oauth"},
		{Provider: "openrouter", Type: "api"},
	}
	if len(keys) != len(want) {
		t.Fatalf("keys = %+v", keys)
	}
	for i := range want {
		if keys[i] != want[i] {
			t.Fatalf("keys[%d] = %+v, want %+v", i, keys[i], want[i])
		}
	}
	if k, err := f.OpencodeKey("anthropic"); err != nil || k.Key != "sk-ant-test" {
		t.Fatalf("OpencodeKey(anthropic) = %+v, %v", k, err)
	}
	for _, name := range []string{"openai", "openrouter", "missing"} {
		if _, err := f.OpencodeKey(name); !errors.Is(err, ErrNotFound) {
			t.Fatalf("OpencodeKey(%s) = %v", name, err)
		}
	}
}
