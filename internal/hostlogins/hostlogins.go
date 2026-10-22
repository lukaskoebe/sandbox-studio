// Package hostlogins finds the logins of Claude Code, Codex and opencode on the computer Studio
// runs on, so that the setup can offer the matching provider (docs/host-logins.md).
//
// Studio never copies a subscription's refresh token: it rotates, and a copy that refreshes
// first signs the computer out. Find only checks that the files exist. A file's content is
// read only by the user's choice, one provider at a time: the account a Codex login belongs
// to (CodexAccount), and opencode's API keys (OpencodeKeys).
package hostlogins

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/lukaskoebe/sandbox-studio/internal/teammates"
)

// ErrNotFound is returned when the computer has no such login.
var ErrNotFound = errors.New("no such login on this computer")

// maxFile bounds what is read of a login file.
const maxFile = 1 << 20

// Finder looks in a home directory, honouring the variables the CLIs honour.
type Finder struct {
	Home string
	// Getenv reads the environment; nil reads Studio's own.
	Getenv func(string) string
}

// Found says which logins the computer has.
type Found struct {
	Claude   bool `json:"claude" doc:"Claude Code is set up on this computer"`
	Codex    bool `json:"codex" doc:"Codex has a login file on this computer"`
	Opencode bool `json:"opencode" doc:"opencode has stored credentials on this computer"`
}

// Find checks which login files exist, without reading them.
func (f Finder) Find() Found {
	if f.Home == "" {
		return Found{}
	}
	return Found{
		// Claude Code keeps its login in .credentials.json, or in the macOS keychain with only
		// ~/.claude.json on disk.
		Claude:   exists(filepath.Join(f.claudeDir(), ".credentials.json")) || exists(filepath.Join(f.Home, ".claude.json")),
		Codex:    exists(f.codexAuth()),
		Opencode: exists(f.opencodeAuth()),
	}
}

func (f Finder) getenv(key string) string {
	if f.Getenv != nil {
		return f.Getenv(key)
	}
	return os.Getenv(key)
}

// claudeDir is CLAUDE_CONFIG_DIR, or ~/.claude.
func (f Finder) claudeDir() string {
	if dir := f.getenv("CLAUDE_CONFIG_DIR"); dir != "" {
		return dir
	}
	return filepath.Join(f.Home, ".claude")
}

// codexAuth is auth.json in CODEX_HOME, or in ~/.codex.
func (f Finder) codexAuth() string {
	dir := f.getenv("CODEX_HOME")
	if dir == "" {
		dir = filepath.Join(f.Home, ".codex")
	}
	return filepath.Join(dir, "auth.json")
}

// opencodeAuth is opencode's auth.json in its XDG data directory, which opencode uses on every
// OS (xdg-basedir), Windows and macOS included.
func (f Finder) opencodeAuth() string {
	dir := f.getenv("XDG_DATA_HOME")
	if dir == "" {
		dir = filepath.Join(f.Home, ".local", "share")
	}
	return filepath.Join(dir, "opencode", "auth.json")
}

func exists(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.Mode().IsRegular()
}

func readFile(path string) ([]byte, error) {
	file, err := os.Open(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	defer file.Close()
	data, err := io.ReadAll(io.LimitReader(file, maxFile+1))
	if err != nil {
		return nil, err
	}
	if len(data) > maxFile {
		return nil, fmt.Errorf("%s is larger than %d bytes", path, maxFile)
	}
	return data, nil
}

// CodexAccount is who the computer's Codex login belongs to.
type CodexAccount struct {
	Email string `json:"email,omitempty" doc:"The ChatGPT account's email"`
	Plan  string `json:"plan,omitempty" doc:"The ChatGPT plan, as Codex records it: plus, pro, team…"`
}

// CodexAccount reads the account of the computer's ChatGPT login from the claims of its ID
// token. Nothing else of the file is kept, and the token's signature isn't checked: the claims
// only label the login for the user. A Codex that uses an API key has no account.
func (f Finder) CodexAccount() (CodexAccount, error) {
	data, err := readFile(f.codexAuth())
	if err != nil {
		return CodexAccount{}, err
	}
	var auth struct {
		Tokens *struct {
			IDToken string `json:"id_token"`
		} `json:"tokens"`
	}
	if err := json.Unmarshal(data, &auth); err != nil {
		return CodexAccount{}, errors.New("Codex's login file is not JSON")
	}
	if auth.Tokens == nil || auth.Tokens.IDToken == "" {
		return CodexAccount{}, ErrNotFound
	}
	parts := strings.Split(auth.Tokens.IDToken, ".")
	if len(parts) != 3 {
		return CodexAccount{}, errors.New("Codex's ID token is not a JWT")
	}
	payload, err := base64.RawURLEncoding.DecodeString(strings.TrimRight(parts[1], "="))
	if err != nil {
		return CodexAccount{}, errors.New("Codex's ID token is not a JWT")
	}
	var claims struct {
		Email string `json:"email"`
		Auth  struct {
			Plan string `json:"chatgpt_plan_type"`
		} `json:"https://api.openai.com/auth"`
	}
	if err := json.Unmarshal(payload, &claims); err != nil {
		return CodexAccount{}, errors.New("Codex's ID token is not a JWT")
	}
	return CodexAccount{Email: claims.Email, Plan: claims.Auth.Plan}, nil
}

// OpencodeKey is one credential opencode stores.
type OpencodeKey struct {
	// Provider is opencode's name of the provider: anthropic, openai, openrouter…
	Provider string `json:"provider"`
	// Type is how opencode signs in: api for a plain key, oauth for a subscription.
	Type string `json:"type"`
	// Kind is the Studio provider kind the key becomes; empty when Studio can't import it.
	Kind string `json:"kind,omitempty" enum:"anthropic_api,openai_api"`
	// Key is the API key; it never leaves Studio.
	Key string `json:"-"`
}

// importable maps opencode's providers to the Studio kinds their plain keys become.
var importable = map[string]string{
	"anthropic": teammates.KindAnthropicAPI,
	"openai":    teammates.KindOpenAIAPI,
}

// OpencodeKeys reads opencode's stored credentials, sorted by provider. Only plain API keys
// of the providers Studio has a kind for can be imported; the others are listed without
// their content. OAuth entries are subscriptions, which Studio signs in to itself.
func (f Finder) OpencodeKeys() ([]OpencodeKey, error) {
	data, err := readFile(f.opencodeAuth())
	if err != nil {
		return nil, err
	}
	var entries map[string]json.RawMessage
	if err := json.Unmarshal(data, &entries); err != nil {
		return nil, errors.New("opencode's auth.json is not JSON")
	}
	out := make([]OpencodeKey, 0, len(entries))
	for name, raw := range entries {
		var e struct {
			Type string `json:"type"`
			Key  string `json:"key"`
		}
		if err := json.Unmarshal(raw, &e); err != nil {
			continue
		}
		k := OpencodeKey{Provider: name, Type: e.Type}
		if kind, ok := importable[name]; ok && e.Type == "api" && strings.TrimSpace(e.Key) != "" {
			k.Kind, k.Key = kind, strings.TrimSpace(e.Key)
		}
		out = append(out, k)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Provider < out[j].Provider })
	return out, nil
}

// OpencodeKey returns the importable key opencode stores for provider.
func (f Finder) OpencodeKey(provider string) (OpencodeKey, error) {
	keys, err := f.OpencodeKeys()
	if err != nil {
		return OpencodeKey{}, err
	}
	for _, k := range keys {
		if k.Provider == provider && k.Kind != "" {
			return k, nil
		}
	}
	return OpencodeKey{}, ErrNotFound
}
