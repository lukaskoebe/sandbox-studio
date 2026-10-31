// Package secrets stores the values of secrets that must never enter a sandbox. Values are
// sealed with a master key kept outside the database. Sandboxes see placeholders instead,
// and the gateway swaps them for the values only in requests to the bound hosts.
package secrets

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base32"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"sync"

	"github.com/lukaskoebe/sandbox-studio/internal/policy"
	"github.com/lukaskoebe/sandbox-studio/internal/store"
)

// ErrInvalid is wrapped by errors describing a secret the vault refuses to store.
var ErrInvalid = errors.New("invalid secret")

const (
	keySize       = 32
	maxValueBytes = 16 << 10

	settingBackend = "vault-key-store" // backend holding the key, so only that one is used
	settingCheck   = "vault-check"     // a sealed value that proves the key is the right one
	checkText      = "sandbox-studio vault"
	checkAAD       = "vault-check"

	fileWarning = "secrets are protected by file permissions only; the vault key is kept in a 0600 file"
)

// Vault seals secret values and hands the gateway their decrypted bindings.
type Vault struct {
	st   *store.Store
	aead cipher.AEAD

	// mu serializes writes with cache fills, so a fill cannot outlive the write that
	// invalidates it.
	mu       sync.Mutex
	bindings map[string][]unsealed // per environment, every scope
}

// unsealed is a secret with its decrypted value.
type unsealed struct {
	sec   store.Secret
	value []byte
}

// Binding is a decrypted secret as the gateway uses it: the placeholder to look for and the
// value to substitute, but only in requests to Hosts.
type Binding struct {
	Name        string
	Placeholder string
	Hosts       []string
	Value       []byte
	// Foreign marks a secret of the environment that the sandbox does not see: one scoped
	// to another sandbox or teammate, or one a more specific secret of the same name
	// overrides. Only Name and Placeholder are set, so the gateway can refuse it.
	Foreign bool
}

// Open returns the vault of the Studio data directory dataDir. The master key lives in the
// OS keychain when that is reachable and in a 0600 file under dataDir otherwise. The keychain
// entry is named after the install, so data directories never share or clobber a key.
func Open(ctx context.Context, st *store.Store, dataDir string, log *slog.Logger) (*Vault, error) {
	install, err := st.Secret(ctx, "install-id", 16)
	if err != nil {
		return nil, err
	}
	primary := keychain{account: "master-key-" + hex.EncodeToString(install)}
	fallback := keyFile{path: filepath.Join(dataDir, "vault.key")}
	return New(ctx, st, primary, fallback, log)
}

// New opens the vault with the key from primary, or from fallback if primary was unreachable
// on the first start. The backend that holds the key is recorded, and only that backend is
// used afterwards. If it becomes unreachable New fails instead of generating a new key,
// because a new key would make every stored secret undecryptable.
func New(ctx context.Context, st *store.Store, primary, fallback KeyStore, log *slog.Logger) (*Vault, error) {
	key, ks, err := loadKey(ctx, st, primary, fallback, log)
	if err != nil {
		return nil, err
	}
	if len(key) != keySize {
		return nil, fmt.Errorf("the vault key in the %s is %d bytes, want %d", describe(ks.Name()), len(key), keySize)
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	v := &Vault{st: st, aead: aead, bindings: map[string][]unsealed{}}
	if err := st.SetSetting(ctx, settingBackend, []byte(ks.Name())); err != nil {
		return nil, err
	}
	if err := v.checkKey(ctx); err != nil {
		return nil, err
	}
	return v, nil
}

// loadKey returns the master key and the backend holding it.
func loadKey(ctx context.Context, st *store.Store, primary, fallback KeyStore, log *slog.Logger) ([]byte, KeyStore, error) {
	recorded, err := st.Setting(ctx, settingBackend)
	if errors.Is(err, store.ErrNotFound) {
		return firstKey(primary, fallback, log)
	}
	if err != nil {
		return nil, nil, err
	}
	var ks KeyStore
	switch string(recorded) {
	case primary.Name():
		ks = primary
	case fallback.Name():
		ks = fallback
		log.Warn(fileWarning)
	default:
		return nil, nil, fmt.Errorf("the vault key is recorded in an unknown store %q", recorded)
	}
	key, err := ks.Get()
	if errors.Is(err, ErrNoKey) {
		return nil, nil, fmt.Errorf("the vault key is kept in the %s, but it is missing; the secrets cannot be decrypted", describe(ks.Name()))
	}
	if err != nil {
		return nil, nil, fmt.Errorf("the vault key is kept in the %s, which is not reachable (%w); unlock it and restart", describe(ks.Name()), err)
	}
	return key, ks, nil
}

// firstKey creates the master key on the first start. A key already present in primary is
// adopted, which covers a start interrupted before the backend was recorded.
func firstKey(primary, fallback KeyStore, log *slog.Logger) ([]byte, KeyStore, error) {
	key, err := primary.Get()
	if err == nil {
		return key, primary, nil
	}
	if errors.Is(err, ErrNoKey) {
		key = randomKey()
		if err = primary.Set(key); err == nil {
			return key, primary, nil
		}
	}
	log.Warn("the OS keychain is not reachable; "+fileWarning, "err", err)
	key, err = fallback.Get()
	if errors.Is(err, ErrNoKey) {
		key = randomKey()
		err = fallback.Set(key)
	}
	if err != nil {
		return nil, nil, err
	}
	return key, fallback, nil
}

func describe(backend string) string {
	if backend == backendKeychain {
		return "OS keychain"
	}
	return "vault key file"
}

func randomKey() []byte {
	key := make([]byte, keySize)
	rand.Read(key)
	return key
}

// checkKey verifies that the key opens the check value, which is written when the vault is
// first created. Without it a wrong key would only show up later as undecryptable secrets.
func (v *Vault) checkKey(ctx context.Context) error {
	sealed, err := v.st.Setting(ctx, settingCheck)
	if errors.Is(err, store.ErrNotFound) {
		return v.st.SetSetting(ctx, settingCheck, v.Seal([]byte(checkText), []byte(checkAAD)))
	}
	if err != nil {
		return err
	}
	if plain, err := v.Unseal(sealed, []byte(checkAAD)); err != nil || string(plain) != checkText {
		return errors.New("the vault key does not match the secrets in this database; the key was replaced or the database belongs to another install")
	}
	return nil
}

// Seal encrypts plaintext and authenticates aad, which is not stored. The result is a random
// nonce followed by the ciphertext.
func (v *Vault) Seal(plaintext, aad []byte) []byte {
	nonce := make([]byte, v.aead.NonceSize())
	rand.Read(nonce)
	return v.aead.Seal(nonce, nonce, plaintext, aad)
}

// Unseal reverses Seal. It fails if the data was altered or aad is not the value it was
// sealed with.
func (v *Vault) Unseal(sealed, aad []byte) ([]byte, error) {
	n := v.aead.NonceSize()
	if len(sealed) < n {
		return nil, errors.New("sealed data is too short")
	}
	plain, err := v.aead.Open(nil, sealed[:n], sealed[n:], aad)
	if err != nil {
		return nil, errors.New("sealed data does not authenticate: wrong key, wrong context or tampering")
	}
	return plain, nil
}

// Create seals value and stores sec, a secret of sec.EnvironmentID covering the whole
// environment, the sandboxes of sec.TeammateID or the sandbox sec.SandboxID. Its
// placeholder is generated here; it and the scope never change.
func (v *Vault) Create(ctx context.Context, sec store.Secret, value string) (store.Secret, error) {
	if err := checkName(sec.Name); err != nil {
		return store.Secret{}, err
	}
	value, err := normalizeValue(value)
	if err != nil {
		return store.Secret{}, err
	}
	if sec.BrowserOnly && (sec.TeammateID == "" || sec.SandboxID != "" || sec.StudioOnly) {
		return store.Secret{}, fmt.Errorf("%w: a browser login belongs to a teammate", ErrInvalid)
	}
	if err := checkUsername(sec.Username, sec.BrowserOnly); err != nil {
		return store.Secret{}, err
	}
	// A studio-only secret never reaches a sandbox, so it is bound to no host.
	hosts := []string{}
	if !sec.StudioOnly {
		if hosts, err = normalizeHosts(sec.Hosts); err != nil {
			return store.Secret{}, err
		}
	}
	// The ID is the AAD of the sealed value, so it is chosen before the row is inserted.
	id := store.NewID()
	if sec.Kind != "" && (sec.StudioOnly || sec.BrowserOnly) {
		return store.Secret{}, fmt.Errorf("%w: a credential kind is for sandboxes", ErrInvalid)
	}
	sec = store.Secret{
		ID: id, EnvironmentID: sec.EnvironmentID, TeammateID: sec.TeammateID, SandboxID: sec.SandboxID,
		Name: sec.Name, Sealed: v.Seal([]byte(value), []byte(id)), Hosts: hosts, Placeholder: newPlaceholder(), Note: sec.Note,
		StudioOnly: sec.StudioOnly, BrowserOnly: sec.BrowserOnly, Username: sec.Username, AgentMayUse: sec.AgentMayUse && sec.BrowserOnly,
		Kind: sec.Kind, Fields: sec.Fields,
	}
	v.mu.Lock()
	defer v.mu.Unlock()
	out, err := v.st.CreateSecret(ctx, sec)
	delete(v.bindings, sec.EnvironmentID)
	return out, err
}

// Update changes the hosts and note of a secret, and a browser login's username. A nil value
// or username keeps the stored one. The name never changes.
func (v *Vault) Update(ctx context.Context, envID, id string, value *string, hosts []string, note string, username *string) (store.Secret, error) {
	cur, err := v.st.SecretByID(ctx, envID, id)
	if err != nil {
		return store.Secret{}, err
	}
	user := cur.Username
	if username != nil {
		user = *username
	}
	if err := checkUsername(user, cur.BrowserOnly); err != nil {
		return store.Secret{}, err
	}
	// A studio-only secret is bound to no host, so it has none to check.
	if cur.StudioOnly {
		hosts = nil
	} else if hosts, err = normalizeHosts(hosts); err != nil {
		return store.Secret{}, err
	}
	sec := store.Secret{ID: id, EnvironmentID: envID, Hosts: hosts, Note: note, Username: user, Fields: cur.Fields}
	if value != nil {
		val, err := normalizeValue(*value)
		if err != nil {
			return store.Secret{}, err
		}
		sec.Sealed = v.Seal([]byte(val), []byte(id))
	}
	v.mu.Lock()
	defer v.mu.Unlock()
	out, err := v.st.UpdateSecret(ctx, sec)
	delete(v.bindings, envID)
	return out, err
}

// SetFields replaces the fields of a secret with a kind, other than its secret one.
func (v *Vault) SetFields(ctx context.Context, envID, id string, fields map[string]string) (store.Secret, error) {
	cur, err := v.st.SecretByID(ctx, envID, id)
	if err != nil {
		return store.Secret{}, err
	}
	if cur.Kind == "" {
		return store.Secret{}, fmt.Errorf("%w: only a secret with a credential kind has fields", ErrInvalid)
	}
	cur.Sealed, cur.Fields = nil, fields
	v.mu.Lock()
	defer v.mu.Unlock()
	out, err := v.st.UpdateSecret(ctx, cur)
	delete(v.bindings, envID)
	return out, err
}

// SetAgentMayUse sets whether the agents of a browser login's teammate fill it, and send it as
// Basic credentials, without asking.
func (v *Vault) SetAgentMayUse(ctx context.Context, envID, id string, on bool) (store.Secret, error) {
	v.mu.Lock()
	defer v.mu.Unlock()
	out, err := v.st.SetAgentMayUse(ctx, envID, id, on)
	delete(v.bindings, envID)
	return out, err
}

// Delete removes a secret.
func (v *Vault) Delete(ctx context.Context, envID, id string) error {
	v.mu.Lock()
	defer v.mu.Unlock()
	err := v.st.DeleteSecret(ctx, envID, id)
	delete(v.bindings, envID)
	return err
}

// List returns the secrets of an environment, of every scope. The values are sealed and
// never leave the vault.
func (v *Vault) List(ctx context.Context, envID string) ([]store.Secret, error) {
	return v.st.Secrets(ctx, envID)
}

// Bindings returns the decrypted secrets sandbox sb sees: its environment's, those of the
// teammate owning it and its own, the most specific one of each name. The other secrets
// of the environment come back as Foreign. The gateway calls it for every intercepted
// connection, so the environment's secrets are cached until its next write. The caller
// must not modify the result.
func (v *Vault) Bindings(ctx context.Context, sb store.Sandbox) ([]Binding, error) {
	all, err := v.unsealed(ctx, sb.EnvironmentID)
	if err != nil {
		return nil, err
	}
	list := make([]store.Secret, len(all))
	for i, u := range all {
		list[i] = u.sec
	}
	// unsealed leaves out secrets with a kind: a sandbox gets credentials made from them,
	// never their value or a placeholder (internal/credentials).
	visible := map[string]bool{}
	for _, sec := range store.VisibleSecrets(list, sb.ID, sb.TeammateID) {
		visible[sec.ID] = true
	}
	out := make([]Binding, 0, len(all))
	for _, u := range all {
		if visible[u.sec.ID] {
			out = append(out, Binding{Name: u.sec.Name, Placeholder: u.sec.Placeholder, Hosts: u.sec.Hosts, Value: u.value})
		} else {
			out = append(out, Binding{Name: u.sec.Name, Placeholder: u.sec.Placeholder, Foreign: true})
		}
	}
	return out, nil
}

// BrowserLogin is a decrypted browser login of a teammate, for its browser alone.
type BrowserLogin struct {
	Name     string
	Hosts    []string
	Username string
	Value    []byte
	// AgentMayUse: the teammate's agents use it without asking.
	AgentMayUse bool
}

// BrowserLogins returns the decrypted browser logins of teammateID, from the cache the
// gateway's bindings use. The caller must not modify the result.
func (v *Vault) BrowserLogins(ctx context.Context, envID, teammateID string) ([]BrowserLogin, error) {
	all, err := v.unsealed(ctx, envID)
	if err != nil {
		return nil, err
	}
	out := []BrowserLogin{}
	for _, u := range all {
		if u.sec.BrowserOnly && teammateID != "" && u.sec.TeammateID == teammateID {
			out = append(out, BrowserLogin{Name: u.sec.Name, Hosts: u.sec.Hosts, Username: u.sec.Username, Value: u.value, AgentMayUse: u.sec.AgentMayUse})
		}
	}
	return out, nil
}

// unsealed returns the decrypted secrets of an environment, studio-only ones and those with a
// credential kind excluded, from the cache if it holds them.
func (v *Vault) unsealed(ctx context.Context, envID string) ([]unsealed, error) {
	v.mu.Lock()
	defer v.mu.Unlock()
	if all, ok := v.bindings[envID]; ok {
		return all, nil
	}
	list, err := v.st.Secrets(ctx, envID)
	if err != nil {
		return nil, err
	}
	out := make([]unsealed, 0, len(list))
	for _, s := range list {
		if s.StudioOnly || s.Kind != "" {
			continue
		}
		value, err := v.Unseal(s.Sealed, []byte(s.ID))
		if err != nil {
			return nil, fmt.Errorf("secret %s: %w", s.Name, err)
		}
		s.Sealed = nil
		out = append(out, unsealed{sec: s, value: value})
	}
	v.bindings[envID] = out
	return out, nil
}

// Env maps the name of each secret sandbox sb sees to its placeholder. These are the
// environment variables the sandbox gets. Secrets with a credential kind have none: their names
// are a namespace of their own, which shadows no plain secret.
func (v *Vault) Env(ctx context.Context, sb store.Sandbox) (map[string]string, error) {
	list, err := v.st.Secrets(ctx, sb.EnvironmentID)
	if err != nil {
		return nil, err
	}
	list = slices.DeleteFunc(list, func(s store.Secret) bool { return s.Kind != "" })
	visible := store.VisibleSecrets(list, sb.ID, sb.TeammateID)
	env := make(map[string]string, len(visible))
	for _, s := range visible {
		env[s.Name] = s.Placeholder
	}
	return env, nil
}

var namePattern = regexp.MustCompile(`^[A-Z_][A-Z0-9_]{0,63}$`)

// reservedNames are read by shells and loaders in every sandbox, or point TLS clients at the
// Studio CA, so a secret must not shadow them.
var reservedNames = []string{
	"PATH", "HOME", "USER", "SHELL", "TERM", "LANG", "PWD", "TMPDIR", "HOSTNAME", "IFS", "ENV", "BASH_ENV",
	"SSL_CERT_FILE", "SSL_CERT_DIR", "NODE_EXTRA_CA_CERTS", "REQUESTS_CA_BUNDLE", "CURL_CA_BUNDLE", "GIT_SSL_CAINFO", "AWS_CA_BUNDLE",
}

// reservedPrefixes belong to the dynamic loaders and to Studio.
var reservedPrefixes = []string{"LD_", "DYLD_", "STUDIO_"}

func checkName(name string) error {
	if !namePattern.MatchString(name) {
		return fmt.Errorf("%w: name %q must be upper-case letters, digits and underscores, start with a letter or underscore, and be at most 64 characters", ErrInvalid, name)
	}
	if slices.Contains(reservedNames, name) {
		return fmt.Errorf("%w: %s is read by the shell in every sandbox", ErrInvalid, name)
	}
	for _, p := range reservedPrefixes {
		if strings.HasPrefix(name, p) {
			return fmt.Errorf("%w: names starting with %s are reserved", ErrInvalid, p)
		}
	}
	return nil
}

// checkUsername validates a browser login's username, which HTTP Basic credentials carry
// before a colon; other secrets have none.
func checkUsername(name string, login bool) error {
	switch {
	case name == "":
		return nil
	case !login:
		return fmt.Errorf("%w: only a browser login has a username", ErrInvalid)
	case len(name) > 200:
		return fmt.Errorf("%w: the username is longer than 200 bytes", ErrInvalid)
	case strings.ContainsRune(name, ':'):
		return fmt.Errorf("%w: a username can't contain a colon", ErrInvalid)
	}
	for _, r := range name {
		if r < 0x20 || r == 0x7f {
			return fmt.Errorf("%w: the username contains a control character", ErrInvalid)
		}
	}
	return nil
}

// normalizeHosts validates the host patterns a secret may be sent to and removes duplicates.
// A secret must name its hosts, so the bare wildcard is refused.
func normalizeHosts(hosts []string) ([]string, error) {
	out := []string{}
	for _, h := range hosts {
		p, err := policy.ValidPattern(h)
		if err != nil {
			return nil, fmt.Errorf("%w: host %q: %w", ErrInvalid, h, err)
		}
		if p == "*" {
			return nil, fmt.Errorf("%w: * is not allowed; name the hosts the secret may be sent to", ErrInvalid)
		}
		if !slices.Contains(out, p) {
			out = append(out, p)
		}
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("%w: name at least one host the secret may be sent to", ErrInvalid)
	}
	return out, nil
}

// normalizeValue drops one trailing newline, which pasting usually adds, and keeps everything
// else exactly as typed.
func normalizeValue(value string) (string, error) {
	switch {
	case strings.HasSuffix(value, "\r\n"):
		value = value[:len(value)-2]
	case strings.HasSuffix(value, "\n"):
		value = value[:len(value)-1]
	}
	switch {
	case value == "":
		return "", fmt.Errorf("%w: the value is empty", ErrInvalid)
	case len(value) > maxValueBytes:
		return "", fmt.Errorf("%w: the value is larger than %d KiB", ErrInvalid, maxValueBytes>>10)
	case strings.IndexByte(value, 0) >= 0:
		return "", fmt.Errorf("%w: the value contains a NUL byte", ErrInvalid)
	}
	return value, nil
}

// newPlaceholder returns what a sandbox sees instead of a value: 160 random bits in lower-case
// base32, which is 32 characters without padding.
func newPlaceholder() string {
	b := make([]byte, 20)
	rand.Read(b)
	return "studio-" + strings.ToLower(base32.StdEncoding.EncodeToString(b))
}
