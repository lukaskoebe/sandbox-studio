package secrets

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"slices"
	"strings"
	"testing"

	"github.com/lukaskoebe/sandbox-studio/internal/store"
)

// memKeys is a KeyStore in memory, so no test touches the real OS keychain.
type memKeys struct {
	name   string
	key    []byte
	err    error // returned by Get instead of the key, to simulate an unreachable backend
	setErr error
	calls  int
}

func (m *memKeys) Name() string { return m.name }

func (m *memKeys) Get() ([]byte, error) {
	m.calls++
	if m.err != nil {
		return nil, m.err
	}
	if m.key == nil {
		return nil, ErrNoKey
	}
	return m.key, nil
}

func (m *memKeys) Set(key []byte) error {
	m.calls++
	if m.setErr != nil {
		return m.setErr
	}
	m.key = slices.Clone(key)
	return nil
}

var quiet = slog.New(slog.NewTextHandler(io.Discard, nil))

func newStore(t *testing.T) *store.Store {
	t.Helper()
	st, err := store.Open(context.Background(), ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	return st
}

// newVault opens a vault whose keys live in memory.
func newVault(t *testing.T, st *store.Store) *Vault {
	t.Helper()
	v, err := New(context.Background(), st, &memKeys{name: backendKeychain}, &memKeys{name: backendFile}, quiet)
	if err != nil {
		t.Fatal(err)
	}
	return v
}

func newEnv(t *testing.T, st *store.Store, name string) string {
	t.Helper()
	env, err := st.CreateEnvironment(context.Background(), name)
	if err != nil {
		t.Fatal(err)
	}
	return env.ID
}

func TestSealRoundTrip(t *testing.T) {
	v := newVault(t, newStore(t))
	sealed := v.Seal([]byte("hunter2"), []byte("id-1"))
	if bytes.Contains(sealed, []byte("hunter2")) {
		t.Fatal("plaintext appears in the sealed data")
	}
	if bytes.Equal(v.Seal([]byte("hunter2"), []byte("id-1")), sealed) {
		t.Fatal("sealing twice gave the same output; the nonce is not random")
	}
	plain, err := v.Unseal(sealed, []byte("id-1"))
	if err != nil || string(plain) != "hunter2" {
		t.Fatalf("unseal: %q %v", plain, err)
	}
}

func TestUnsealRejectsWrongContextAndTampering(t *testing.T) {
	v := newVault(t, newStore(t))
	sealed := v.Seal([]byte("hunter2"), []byte("id-1"))

	if _, err := v.Unseal(sealed, []byte("id-2")); err == nil {
		t.Fatal("wrong AAD accepted")
	}
	for _, i := range []int{0, len(sealed) - 1} { // a nonce byte and a ciphertext byte
		tampered := bytes.Clone(sealed)
		tampered[i] ^= 1
		if _, err := v.Unseal(tampered, []byte("id-1")); err == nil {
			t.Fatalf("tampered byte %d accepted", i)
		}
	}
	if _, err := v.Unseal(sealed[:5], []byte("id-1")); err == nil {
		t.Fatal("truncated data accepted")
	}
}

func TestKeyMismatchOnReopen(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "studio.db")
	ok := &memKeys{name: backendKeychain}
	fallback := &memKeys{name: backendFile}

	st, err := store.Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	env := newEnv(t, st, "work")
	v, err := New(ctx, st, ok, fallback, quiet)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := v.Create(ctx, store.Secret{EnvironmentID: env, Name: "API_KEY", Hosts: []string{"api.example.com"}}, "sk-live"); err != nil {
		t.Fatal(err)
	}
	st.Close()

	st, err = store.Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	if _, err := New(ctx, st, &memKeys{name: backendKeychain, key: randomKey()}, fallback, quiet); err == nil || !strings.Contains(err.Error(), "does not match") {
		t.Fatalf("a different key opened the vault: %v", err)
	}
	v, err = New(ctx, st, ok, fallback, quiet)
	if err != nil {
		t.Fatalf("reopen with the same key: %v", err)
	}
	if b, err := v.Bindings(ctx, store.Sandbox{EnvironmentID: env}); err != nil || len(b) != 1 || string(b[0].Value) != "sk-live" {
		t.Fatalf("reopened bindings: %+v %v", b, err)
	}
}

func TestRecordedKeychainNeverFallsBackToFile(t *testing.T) {
	ctx := context.Background()
	st := newStore(t)
	keychain := &memKeys{name: backendKeychain}
	file := &memKeys{name: backendFile, key: randomKey()}
	if _, err := New(ctx, st, keychain, file, quiet); err != nil {
		t.Fatal(err)
	}
	file.calls = 0

	keychain.err = errors.New("the keychain is locked")
	_, err := New(ctx, st, keychain, file, quiet)
	if err == nil || !strings.Contains(err.Error(), "OS keychain, which is not reachable") {
		t.Fatalf("unreachable keychain: %v", err)
	}
	if file.calls != 0 {
		t.Fatal("the file store was consulted although the keychain holds the key")
	}

	keychain.err = nil
	keychain.key = nil
	if _, err := New(ctx, st, keychain, file, quiet); err == nil || !strings.Contains(err.Error(), "missing") {
		t.Fatalf("missing key: %v", err)
	}
	if file.calls != 0 {
		t.Fatal("the file store was consulted although the keychain holds the key")
	}
}

func TestFirstStartFallsBackToFile(t *testing.T) {
	ctx := context.Background()
	st := newStore(t)
	path := filepath.Join(t.TempDir(), "vault.key")
	keychain := &memKeys{name: backendKeychain, err: errors.New("no Secret Service")}
	fallback := keyFile{path: path}

	v, err := New(ctx, st, keychain, fallback, quiet)
	if err != nil {
		t.Fatal(err)
	}
	if runtime.GOOS != "windows" {
		info, err := os.Stat(path)
		if err != nil || info.Mode().Perm() != 0o600 {
			t.Fatalf("key file: %v %v", info, err)
		}
	}
	if rec, _ := st.Setting(ctx, settingBackend); string(rec) != backendFile {
		t.Fatalf("recorded backend %q", rec)
	}

	// A later start keeps using the file, even once the keychain is reachable again.
	keychain.err = nil
	env := newEnv(t, st, "work")
	if _, err := v.Create(ctx, store.Secret{EnvironmentID: env, Name: "API_KEY", Hosts: []string{"a.com"}}, "sk-1"); err != nil {
		t.Fatal(err)
	}
	v, err = New(ctx, st, keychain, fallback, quiet)
	if err != nil {
		t.Fatal(err)
	}
	if b, err := v.Bindings(ctx, store.Sandbox{EnvironmentID: env}); err != nil || len(b) != 1 || string(b[0].Value) != "sk-1" {
		t.Fatalf("bindings after restart: %+v %v", b, err)
	}
	if keychain.key != nil {
		t.Fatal("a key was written to the keychain after falling back to the file")
	}
}

func TestNameValidation(t *testing.T) {
	ctx := context.Background()
	st := newStore(t)
	env := newEnv(t, st, "work")
	v := newVault(t, st)

	for _, name := range []string{"API_KEY", "_TOKEN", "A1_B2", "MY_STUDIO_TOKEN", "PATHOLOGY", strings.Repeat("A", 64)} {
		if _, err := v.Create(ctx, store.Secret{EnvironmentID: env, Name: name, Hosts: []string{"a.com"}}, "v"); err != nil {
			t.Errorf("%s rejected: %v", name, err)
		}
	}
	for _, name := range []string{
		"", "lower", "1ABC", "HAS-DASH", "HAS SPACE", "A=B", strings.Repeat("A", 65),
		"PATH", "HOME", "USER", "SHELL", "TERM", "LANG", "PWD", "TMPDIR", "HOSTNAME",
		"LD_PRELOAD", "DYLD_INSERT_LIBRARIES", "STUDIO_TOKEN",
	} {
		if _, err := v.Create(ctx, store.Secret{EnvironmentID: env, Name: name, Hosts: []string{"a.com"}}, "v"); !errors.Is(err, ErrInvalid) {
			t.Errorf("%q: got %v, want ErrInvalid", name, err)
		}
	}
}

func TestHostValidation(t *testing.T) {
	ctx := context.Background()
	st := newStore(t)
	env := newEnv(t, st, "work")
	v := newVault(t, st)

	for name, hosts := range map[string][]string{
		"no hosts":        {},
		"nil hosts":       nil,
		"bare wildcard":   {"*"},
		"padded wildcard": {" * "},
		"wildcard beside": {"example.com", "*"},
		"two wildcards":   {"*.*.com"},
		"space in name":   {"bad host"},
		"empty host":      {""},
	} {
		if _, err := v.Create(ctx, store.Secret{EnvironmentID: env, Name: "KEY", Hosts: hosts}, "v"); !errors.Is(err, ErrInvalid) {
			t.Errorf("%s: got %v, want ErrInvalid", name, err)
		}
	}

	sec, err := v.Create(ctx, store.Secret{EnvironmentID: env, Name: "KEY", Hosts: []string{"API.Example.com.", "*.example.org", "api.example.com", "10.0.0.1"}}, "v")
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"api.example.com", "*.example.org", "10.0.0.1"}; !slices.Equal(sec.Hosts, want) {
		t.Fatalf("hosts %v, want %v", sec.Hosts, want)
	}
	if _, err := v.Update(ctx, env, sec.ID, nil, nil, ""); !errors.Is(err, ErrInvalid) {
		t.Fatalf("update without hosts: %v", err)
	}
}

func TestValueValidationAndTrailingNewline(t *testing.T) {
	ctx := context.Background()
	st := newStore(t)
	env := newEnv(t, st, "work")
	v := newVault(t, st)

	for name, value := range map[string]string{
		"empty":        "",
		"only newline": "\n",
		"NUL byte":     "a\x00b",
		"too large":    strings.Repeat("x", 16<<10+1),
		"CRLF only":    "\r\n",
	} {
		if _, err := v.Create(ctx, store.Secret{EnvironmentID: env, Name: "KEY", Hosts: []string{"a.com"}}, value); !errors.Is(err, ErrInvalid) {
			t.Errorf("%s: got %v, want ErrInvalid", name, err)
		}
	}

	maximum := strings.Repeat("x", 16<<10)
	cases := []struct{ name, input, want string }{
		{"LF", "token\n", "token"},
		{"CRLF", "token\r\n", "token"},
		{"INTERIOR", "to\nken\n\n", "to\nken\n"},
		{"SPACES", "  padded  ", "  padded  "},
		{"MAXIMUM", maximum, maximum},
	}
	want := map[string]string{}
	for _, tc := range cases {
		if _, err := v.Create(ctx, store.Secret{EnvironmentID: env, Name: tc.name, Hosts: []string{"a.com"}}, tc.input); err != nil {
			t.Fatalf("%s: %v", tc.name, err)
		}
		want[tc.name] = tc.want
	}
	bindings, err := v.Bindings(ctx, store.Sandbox{EnvironmentID: env})
	if err != nil || len(bindings) != len(cases) {
		t.Fatalf("bindings: %d %v", len(bindings), err)
	}
	for _, b := range bindings {
		if string(b.Value) != want[b.Name] {
			t.Errorf("%s stored %q, want %q", b.Name, b.Value, want[b.Name])
		}
	}
}

func TestBindingsAndCacheInvalidation(t *testing.T) {
	ctx := context.Background()
	st := newStore(t)
	env := newEnv(t, st, "work")
	v := newVault(t, st)

	first, err := v.Create(ctx, store.Secret{EnvironmentID: env, Name: "API_KEY", Hosts: []string{"api.example.com"}}, "sk-one")
	if err != nil {
		t.Fatal(err)
	}
	b, err := v.Bindings(ctx, store.Sandbox{EnvironmentID: env})
	if err != nil || len(b) != 1 || b[0].Name != "API_KEY" || string(b[0].Value) != "sk-one" ||
		b[0].Placeholder != first.Placeholder || !slices.Equal(b[0].Hosts, []string{"api.example.com"}) {
		t.Fatalf("bindings: %+v %v", b, err)
	}
	if _, cached := v.bindings[env]; !cached {
		t.Fatal("bindings were not cached")
	}

	// Creating a secret drops the cache.
	if _, err := v.Create(ctx, store.Secret{EnvironmentID: env, Name: "OTHER", Hosts: []string{"b.com"}}, "x"); err != nil {
		t.Fatal(err)
	}
	if _, cached := v.bindings[env]; cached {
		t.Fatal("create did not invalidate the cache")
	}
	if b, _ := v.Bindings(ctx, store.Sandbox{EnvironmentID: env}); len(b) != 2 {
		t.Fatalf("after create: %+v", b)
	}

	newValue := "sk-two"
	if _, err := v.Update(ctx, env, first.ID, &newValue, []string{"api.example.net"}, ""); err != nil {
		t.Fatal(err)
	}
	if _, cached := v.bindings[env]; cached {
		t.Fatal("update did not invalidate the cache")
	}
	b, _ = v.Bindings(ctx, store.Sandbox{EnvironmentID: env})
	if string(b[0].Value) != "sk-two" || !slices.Equal(b[0].Hosts, []string{"api.example.net"}) {
		t.Fatalf("after update with value: %+v", b[0])
	}

	// Without a value the stored one stays, and the hosts still change.
	if _, err := v.Update(ctx, env, first.ID, nil, []string{"api.example.org"}, "moved"); err != nil {
		t.Fatal(err)
	}
	b, _ = v.Bindings(ctx, store.Sandbox{EnvironmentID: env})
	if string(b[0].Value) != "sk-two" || !slices.Equal(b[0].Hosts, []string{"api.example.org"}) {
		t.Fatalf("after update without value: %+v", b[0])
	}

	if err := v.Delete(ctx, env, first.ID); err != nil {
		t.Fatal(err)
	}
	if b, _ := v.Bindings(ctx, store.Sandbox{EnvironmentID: env}); len(b) != 1 || b[0].Name != "OTHER" {
		t.Fatalf("after delete: %+v", b)
	}
	if err := v.Delete(ctx, env, first.ID); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("delete twice: %v", err)
	}
}

func TestPlaceholdersAreUniqueAndStable(t *testing.T) {
	ctx := context.Background()
	st := newStore(t)
	env := newEnv(t, st, "work")
	v := newVault(t, st)
	shape := regexp.MustCompile(`^studio-[a-z2-7]{32}$`)

	seen := map[string]bool{}
	var first store.Secret
	for i := range 50 {
		sec, err := v.Create(ctx, store.Secret{EnvironmentID: env, Name: fmt.Sprintf("KEY_%d", i), Hosts: []string{"a.com"}}, "v")
		if err != nil {
			t.Fatal(err)
		}
		if !shape.MatchString(sec.Placeholder) || seen[sec.Placeholder] {
			t.Fatalf("placeholder %q: malformed or repeated", sec.Placeholder)
		}
		seen[sec.Placeholder] = true
		if i == 0 {
			first = sec
		}
	}

	value := "new"
	updated, err := v.Update(ctx, env, first.ID, &value, []string{"a.com"}, "")
	if err != nil || updated.Placeholder != first.Placeholder {
		t.Fatalf("placeholder changed by update: %q -> %q (%v)", first.Placeholder, updated.Placeholder, err)
	}

	env2, err := v.Env(ctx, store.Sandbox{EnvironmentID: env})
	if err != nil || len(env2) != 50 || env2["KEY_0"] != first.Placeholder {
		t.Fatalf("env: %d entries, %v", len(env2), err)
	}
	list, err := v.List(ctx, env)
	if err != nil || len(list) != 50 {
		t.Fatalf("list: %d %v", len(list), err)
	}
}

func TestScopedBindingsAndEnv(t *testing.T) {
	ctx := context.Background()
	st := newStore(t)
	env := newEnv(t, st, "work")
	v := newVault(t, st)
	// A teammate needs a provider; its key is an environment secret like any other.
	key, err := v.Prepare(env, "PROVIDER_A_KEY", "sk-provider", []string{"api.anthropic.com"}, "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.CreateProvider(ctx, store.Provider{ID: "p", EnvironmentID: env, Name: "a", Kind: "anthropic_api"}, &key); err != nil {
		t.Fatal(err)
	}
	v.Changed(env)
	if _, err := st.CreateTeammate(ctx, store.Teammate{ID: "ada", EnvironmentID: env, Name: "Ada", Harness: "claude", ProviderID: "p", GitName: "Ada", GitEmail: "ada@agents.invalid"}); err != nil {
		t.Fatal(err)
	}
	adaBox, err := st.CreateSandbox(ctx, store.Sandbox{EnvironmentID: env, Name: "ada-box", TeammateID: "ada"})
	if err != nil {
		t.Fatal(err)
	}
	ada2, err := st.CreateSandbox(ctx, store.Sandbox{EnvironmentID: env, Name: "ada-2", TeammateID: "ada"})
	if err != nil {
		t.Fatal(err)
	}
	loose, err := st.CreateSandbox(ctx, store.Sandbox{EnvironmentID: env, Name: "loose"})
	if err != nil {
		t.Fatal(err)
	}

	envToken, err := v.Create(ctx, store.Secret{EnvironmentID: env, Name: "GH_TOKEN", Hosts: []string{"api.github.com"}}, "env-token")
	if err != nil {
		t.Fatal(err)
	}
	adaToken, err := v.Create(ctx, store.Secret{EnvironmentID: env, TeammateID: "ada", Name: "GH_TOKEN", Hosts: []string{"api.github.com"}}, "ada-token")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := v.Bindings(ctx, adaBox); err != nil { // fills the cache
		t.Fatal(err)
	}
	boxToken, err := v.Create(ctx, store.Secret{EnvironmentID: env, SandboxID: adaBox.ID, Name: "GH_TOKEN", Hosts: []string{"api.github.com"}}, "box-token")
	if err != nil {
		t.Fatal(err)
	}
	if _, cached := v.bindings[env]; cached {
		t.Fatal("creating a sandbox secret did not invalidate the cache")
	}

	// Each sandbox gets the placeholder of the most specific GH_TOKEN, and the gateway
	// substitutes only that one; the others are foreign.
	for _, tc := range []struct {
		sb    store.Sandbox
		own   store.Secret
		value string
	}{
		{adaBox, boxToken, "box-token"},
		{ada2, adaToken, "ada-token"},
		{loose, envToken, "env-token"},
	} {
		e, err := v.Env(ctx, tc.sb)
		if err != nil || len(e) != 2 || e["GH_TOKEN"] != tc.own.Placeholder || e["PROVIDER_A_KEY"] != key.Placeholder {
			t.Fatalf("%s env: %v %v", tc.sb.Name, e, err)
		}
		b, err := v.Bindings(ctx, tc.sb)
		if err != nil || len(b) != 4 {
			t.Fatalf("%s bindings: %+v %v", tc.sb.Name, b, err)
		}
		for _, x := range b {
			switch {
			case x.Placeholder == tc.own.Placeholder:
				if x.Foreign || string(x.Value) != tc.value || !slices.Equal(x.Hosts, []string{"api.github.com"}) {
					t.Errorf("%s: own binding %+v", tc.sb.Name, x)
				}
			case x.Name == "GH_TOKEN":
				if !x.Foreign || x.Value != nil || x.Hosts != nil {
					t.Errorf("%s: another scope's binding is usable: %+v", tc.sb.Name, x)
				}
			case x.Placeholder != key.Placeholder || x.Foreign || string(x.Value) != "sk-provider":
				t.Errorf("%s: provider binding %+v", tc.sb.Name, x)
			}
		}
	}

	// Updating and deleting invalidate the cache too.
	value := "box-token-2"
	if _, err := v.Update(ctx, env, boxToken.ID, &value, []string{"api.github.com"}, ""); err != nil {
		t.Fatal(err)
	}
	if b, _ := v.Bindings(ctx, adaBox); !slices.ContainsFunc(b, func(x Binding) bool { return string(x.Value) == "box-token-2" }) {
		t.Fatalf("after update: %+v", b)
	}
	if err := v.Delete(ctx, env, boxToken.ID); err != nil {
		t.Fatal(err)
	}
	if e, _ := v.Env(ctx, adaBox); e["GH_TOKEN"] != adaToken.Placeholder {
		t.Fatalf("after deleting the sandbox's own secret: %v", e)
	}
	b, _ := v.Bindings(ctx, adaBox)
	if i := slices.IndexFunc(b, func(x Binding) bool { return x.Placeholder == adaToken.Placeholder }); i < 0 || string(b[i].Value) != "ada-token" || b[i].Foreign {
		t.Fatalf("after delete: %+v", b)
	}
}
