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
	if _, err := v.Create(ctx, env, "API_KEY", "sk-live", []string{"api.example.com"}, ""); err != nil {
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
	if b, err := v.Bindings(ctx, env); err != nil || len(b) != 1 || string(b[0].Value) != "sk-live" {
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
	if _, err := v.Create(ctx, env, "API_KEY", "sk-1", []string{"a.com"}, ""); err != nil {
		t.Fatal(err)
	}
	v, err = New(ctx, st, keychain, fallback, quiet)
	if err != nil {
		t.Fatal(err)
	}
	if b, err := v.Bindings(ctx, env); err != nil || len(b) != 1 || string(b[0].Value) != "sk-1" {
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
		if _, err := v.Create(ctx, env, name, "v", []string{"a.com"}, ""); err != nil {
			t.Errorf("%s rejected: %v", name, err)
		}
	}
	for _, name := range []string{
		"", "lower", "1ABC", "HAS-DASH", "HAS SPACE", "A=B", strings.Repeat("A", 65),
		"PATH", "HOME", "USER", "SHELL", "TERM", "LANG", "PWD", "TMPDIR", "HOSTNAME",
		"LD_PRELOAD", "DYLD_INSERT_LIBRARIES", "STUDIO_TOKEN",
	} {
		if _, err := v.Create(ctx, env, name, "v", []string{"a.com"}, ""); !errors.Is(err, ErrInvalid) {
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
		if _, err := v.Create(ctx, env, "KEY", "v", hosts, ""); !errors.Is(err, ErrInvalid) {
			t.Errorf("%s: got %v, want ErrInvalid", name, err)
		}
	}

	sec, err := v.Create(ctx, env, "KEY", "v", []string{"API.Example.com.", "*.example.org", "api.example.com", "10.0.0.1"}, "")
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
		if _, err := v.Create(ctx, env, "KEY", value, []string{"a.com"}, ""); !errors.Is(err, ErrInvalid) {
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
		if _, err := v.Create(ctx, env, tc.name, tc.input, []string{"a.com"}, ""); err != nil {
			t.Fatalf("%s: %v", tc.name, err)
		}
		want[tc.name] = tc.want
	}
	bindings, err := v.Bindings(ctx, env)
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

	first, err := v.Create(ctx, env, "API_KEY", "sk-one", []string{"api.example.com"}, "")
	if err != nil {
		t.Fatal(err)
	}
	b, err := v.Bindings(ctx, env)
	if err != nil || len(b) != 1 || b[0].Name != "API_KEY" || string(b[0].Value) != "sk-one" ||
		b[0].Placeholder != first.Placeholder || !slices.Equal(b[0].Hosts, []string{"api.example.com"}) {
		t.Fatalf("bindings: %+v %v", b, err)
	}
	if _, cached := v.bindings[env]; !cached {
		t.Fatal("bindings were not cached")
	}

	// Creating a secret drops the cache.
	if _, err := v.Create(ctx, env, "OTHER", "x", []string{"b.com"}, ""); err != nil {
		t.Fatal(err)
	}
	if _, cached := v.bindings[env]; cached {
		t.Fatal("create did not invalidate the cache")
	}
	if b, _ := v.Bindings(ctx, env); len(b) != 2 {
		t.Fatalf("after create: %+v", b)
	}

	newValue := "sk-two"
	if _, err := v.Update(ctx, env, first.ID, &newValue, []string{"api.example.net"}, ""); err != nil {
		t.Fatal(err)
	}
	if _, cached := v.bindings[env]; cached {
		t.Fatal("update did not invalidate the cache")
	}
	b, _ = v.Bindings(ctx, env)
	if string(b[0].Value) != "sk-two" || !slices.Equal(b[0].Hosts, []string{"api.example.net"}) {
		t.Fatalf("after update with value: %+v", b[0])
	}

	// Without a value the stored one stays, and the hosts still change.
	if _, err := v.Update(ctx, env, first.ID, nil, []string{"api.example.org"}, "moved"); err != nil {
		t.Fatal(err)
	}
	b, _ = v.Bindings(ctx, env)
	if string(b[0].Value) != "sk-two" || !slices.Equal(b[0].Hosts, []string{"api.example.org"}) {
		t.Fatalf("after update without value: %+v", b[0])
	}

	if err := v.Delete(ctx, env, first.ID); err != nil {
		t.Fatal(err)
	}
	if b, _ := v.Bindings(ctx, env); len(b) != 1 || b[0].Name != "OTHER" {
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
		sec, err := v.Create(ctx, env, fmt.Sprintf("KEY_%d", i), "v", []string{"a.com"}, "")
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

	env2, err := v.Env(ctx, env)
	if err != nil || len(env2) != 50 || env2["KEY_0"] != first.Placeholder {
		t.Fatalf("env: %d entries, %v", len(env2), err)
	}
	list, err := v.List(ctx, env)
	if err != nil || len(list) != 50 {
		t.Fatalf("list: %d %v", len(list), err)
	}
}
