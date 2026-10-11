//go:build linux

package guest

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"math/big"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"
)

type fakeBrowserTrustCert struct {
	pem   []byte
	trust string
}

type fakeBrowserTrust struct {
	dbDir          string
	entries        map[string]fakeBrowserTrustCert
	commands       [][]string
	failNext       map[string]int
	importAttempts int
}

func (f *fakeBrowserTrust) run(ctx context.Context, args ...string) ([]byte, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	f.commands = append(f.commands, append([]string(nil), args...))
	if len(args) == 0 {
		return nil, errors.New("empty certutil arguments")
	}
	if args[0] == "-A" {
		f.importAttempts++
	}
	if f.failNext[args[0]] > 0 {
		f.failNext[args[0]]--
		return nil, fmt.Errorf("simulated %s failure", args[0])
	}
	f.dbDir = strings.TrimPrefix(argumentAfter(args, "-d"), "sql:")
	switch args[0] {
	case "-N":
		if f.dbDir == "" {
			return nil, errors.New("missing database path")
		}
		if err := os.WriteFile(filepath.Join(f.dbDir, "cert9.db"), []byte("test database"), 0o600); err != nil {
			return nil, err
		}
		return nil, nil
	case "-L":
		if name := argumentAfter(args, "-n"); name != "" {
			entry, ok := f.entries[name]
			if !ok {
				return nil, fmt.Errorf("certificate %q not found", name)
			}
			return append([]byte(nil), entry.pem...), nil
		}
		names := make([]string, 0, len(f.entries))
		for name := range f.entries {
			names = append(names, name)
		}
		sort.Strings(names)
		var listing strings.Builder
		listing.WriteString("Certificate Nickname  Trust Attributes\n")
		listing.WriteString("                      SSL,S/MIME,JAR/XPI\n")
		for _, name := range names {
			fmt.Fprintf(&listing, "%s\t%s\n", name, f.entries[name].trust)
		}
		return []byte(listing.String()), nil
	case "-M":
		name, trust := argumentAfter(args, "-n"), argumentAfter(args, "-t")
		entry, ok := f.entries[name]
		if !ok {
			return nil, fmt.Errorf("certificate %q not found", name)
		}
		entry.trust = trust
		f.entries[name] = entry
		return nil, nil
	case "-D":
		name := argumentAfter(args, "-n")
		if _, ok := f.entries[name]; !ok {
			return nil, fmt.Errorf("certificate %q not found", name)
		}
		delete(f.entries, name)
		return nil, nil
	case "-A":
		if argumentAfter(args, "-t") != "C,," {
			return nil, errors.New("Studio CA must have SSL CA trust only")
		}
		data, err := os.ReadFile(argumentAfter(args, "-i"))
		if err != nil {
			return nil, err
		}
		f.entries[argumentAfter(args, "-n")] = fakeBrowserTrustCert{pem: data, trust: argumentAfter(args, "-t")}
		return nil, nil
	default:
		return nil, fmt.Errorf("unexpected certutil command %q", args[0])
	}
}

func argumentAfter(args []string, flag string) string {
	for i, arg := range args {
		if arg == flag && i+1 < len(args) {
			return args[i+1]
		}
	}
	return ""
}

func testBrowserCA(t *testing.T, commonName string) []byte {
	t.Helper()
	publicKey, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	template := &x509.Certificate{
		SerialNumber:          big.NewInt(int64(len(commonName) + 1)),
		Subject:               pkix.Name{CommonName: commonName},
		NotBefore:             time.Unix(0, 0),
		NotAfter:              time.Now().Add(time.Hour),
		IsCA:                  true,
		BasicConstraintsValid: true,
		KeyUsage:              x509.KeyUsageCertSign,
	}
	der, err := x509.CreateCertificate(rand.Reader, template, template, publicKey, privateKey)
	if err != nil {
		t.Fatal(err)
	}
	return pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
}

func writeBrowserCA(t *testing.T, path string, ca []byte) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, ca, 0o600); err != nil {
		t.Fatal(err)
	}
}

func prepareTestBrowserTrust(t *testing.T, ctx context.Context, state *browserTrustState, caPath, nssDir string, runner *fakeBrowserTrust) error {
	t.Helper()
	return prepareBrowserTrust(ctx, state, caPath, nssDir, uint32(os.Getuid()), uint32(os.Getgid()), runner.run)
}

func TestPrepareBrowserTrustImportsOnceAndUpdatesOnlyStudioCA(t *testing.T) {
	root := t.TempDir()
	caPath := filepath.Join(root, "config", "ca.crt")
	caOne, caTwo := testBrowserCA(t, "Studio CA one"), testBrowserCA(t, "Studio CA two")
	writeBrowserCA(t, caPath, caOne)
	nssDir := filepath.Join(root, "home", browserTrustNSSDBPath)
	runner := &fakeBrowserTrust{
		entries:  map[string]fakeBrowserTrustCert{"unrelated": {pem: []byte("keep me"), trust: ",,"}},
		failNext: make(map[string]int),
	}
	if err := os.MkdirAll(nssDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(nssDir, "cert9.db"), []byte("existing database"), 0o600); err != nil {
		t.Fatal(err)
	}
	state := &browserTrustState{}
	ctx := context.Background()

	if err := prepareTestBrowserTrust(t, ctx, state, caPath, nssDir, runner); err != nil {
		t.Fatal(err)
	}
	if runner.importAttempts != 1 {
		t.Fatalf("first preparation imported %d times, want 1", runner.importAttempts)
	}
	if !bytes.Equal(runner.entries["unrelated"].pem, []byte("keep me")) {
		t.Fatal("preparation changed an unrelated NSS certificate")
	}
	if runner.entries[browserTrustNickname].trust != "C,," {
		t.Fatalf("Studio CA trust = %q, want C,,", runner.entries[browserTrustNickname].trust)
	}
	commandsAfterImport := len(runner.commands)
	if err := prepareTestBrowserTrust(t, ctx, state, caPath, nssDir, runner); err != nil {
		t.Fatal(err)
	}
	if len(runner.commands) != commandsAfterImport {
		t.Fatalf("unchanged CA ran certutil again: %q", runner.commands[commandsAfterImport:])
	}

	writeBrowserCA(t, caPath, caTwo)
	if err := prepareTestBrowserTrust(t, ctx, state, caPath, nssDir, runner); err != nil {
		t.Fatal(err)
	}
	if runner.importAttempts != 2 {
		t.Fatalf("changed CA imported %d times total, want 2", runner.importAttempts)
	}
	if !bytes.Equal(runner.entries["unrelated"].pem, []byte("keep me")) {
		t.Fatal("CA rotation changed an unrelated NSS certificate")
	}
	installed, err := parseBrowserTrustCA(runner.entries[browserTrustNickname].pem)
	if err != nil || !bytes.Equal(installed.Raw, mustParseBrowserCA(t, caTwo).Raw) {
		t.Fatalf("Studio NSS entry was not replaced with changed CA: %v", err)
	}

	// A fresh state simulates an agent process restart with the persistent NSS database.
	commandsBeforeRestart := len(runner.commands)
	restarted := &browserTrustState{}
	if err := prepareTestBrowserTrust(t, ctx, restarted, caPath, nssDir, runner); err != nil {
		t.Fatal(err)
	}
	for _, command := range runner.commands[commandsBeforeRestart:] {
		if command[0] == "-N" || command[0] == "-D" || command[0] == "-A" {
			t.Fatalf("restart reinitialized or reimported the existing NSS database: %q", command)
		}
	}
	if runner.importAttempts != 2 {
		t.Fatalf("restart imported CA again; total imports = %d", runner.importAttempts)
	}
}

func mustParseBrowserCA(t *testing.T, data []byte) *x509.Certificate {
	t.Helper()
	cert, err := parseBrowserTrustCA(data)
	if err != nil {
		t.Fatal(err)
	}
	return cert
}

func TestPrepareBrowserTrustRetriesFailedImport(t *testing.T) {
	root := t.TempDir()
	caPath := filepath.Join(root, "config", "ca.crt")
	writeBrowserCA(t, caPath, testBrowserCA(t, "Studio retry CA"))
	runner := &fakeBrowserTrust{entries: make(map[string]fakeBrowserTrustCert), failNext: map[string]int{"-A": 1}}
	state := &browserTrustState{}
	nssDir := filepath.Join(root, "home", browserTrustNSSDBPath)

	if err := prepareTestBrowserTrust(t, context.Background(), state, caPath, nssDir, runner); err == nil {
		t.Fatal("failed import was accepted")
	}
	if state.ready {
		t.Fatal("failed import was cached")
	}
	if err := prepareTestBrowserTrust(t, context.Background(), state, caPath, nssDir, runner); err != nil {
		t.Fatalf("retry after failed import: %v", err)
	}
	if runner.importAttempts != 2 || !state.ready {
		t.Fatalf("retry did not import successfully: attempts=%d ready=%t", runner.importAttempts, state.ready)
	}
}

func TestPrepareBrowserTrustFailsWhenStudioCAIsMissing(t *testing.T) {
	root := t.TempDir()
	runner := &fakeBrowserTrust{entries: make(map[string]fakeBrowserTrustCert), failNext: make(map[string]int)}
	err := prepareTestBrowserTrust(t, context.Background(), &browserTrustState{}, filepath.Join(root, "missing-ca.crt"), filepath.Join(root, "nssdb"), runner)
	if err == nil || !strings.Contains(err.Error(), "retry after environment configuration completes") {
		t.Fatalf("missing CA error = %v, want clear retryable error", err)
	}
	if len(runner.commands) != 0 {
		t.Fatalf("certutil ran without the Studio CA: %q", runner.commands)
	}
}

func TestRunBrowserTrustFailureDoesNotLaunchBrowser(t *testing.T) {
	launched := false
	_, err := runBrowserAfterTrust(context.Background(), func(context.Context) error {
		return errors.New("browser trust: simulated preparation failure")
	}, func(context.Context) (json.RawMessage, error) {
		launched = true
		return json.RawMessage(`{"success":true}`), nil
	})
	if err == nil {
		t.Fatal("trust failure was accepted")
	}
	if launched {
		t.Fatal("agent-browser launched after trust preparation failed")
	}
}
