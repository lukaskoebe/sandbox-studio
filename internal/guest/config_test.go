//go:build linux

package guest

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/lukaskoebe/sandbox-studio/internal/agentproto"
)

func testCert(t *testing.T, isCA bool) string {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	tmpl := &x509.Certificate{
		SerialNumber:          big.NewInt(1),
		Subject:               pkix.Name{CommonName: "test"},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(time.Hour),
		BasicConstraintsValid: true,
		IsCA:                  isCA,
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, key.Public(), key)
	if err != nil {
		t.Fatal(err)
	}
	return string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}))
}

func TestApplyConfig(t *testing.T) {
	root := t.TempDir()
	refreshes := 0
	refresh := func() error {
		refreshes++
		ca, err := os.ReadFile(filepath.Join(root, trustedCA))
		if err != nil {
			return err
		}
		if err := os.MkdirAll(filepath.Dir(filepath.Join(root, systemRoots)), 0o755); err != nil {
			return err
		}
		return os.WriteFile(filepath.Join(root, systemRoots), append([]byte("public roots\n"), ca...), 0o644)
	}
	read := func(path string) string {
		b, err := os.ReadFile(filepath.Join(root, path))
		if err != nil {
			t.Fatal(err)
		}
		return string(b)
	}

	ca := testCert(t, true)
	cfg := agentproto.Config{CA: ca, Env: map[string]string{
		"OPENAI_API_KEY": "studio-abcdefghijklmnopqrstuvwxyz234567",
		"A_TOKEN":        "studio-234567abcdefghijklmnopqrstuvwxyz",
	}}
	if err := applyConfig(root, cfg, refresh); err != nil {
		t.Fatal(err)
	}
	if read(caFile) != ca || read(trustedCA) != ca || read(caBundle) != "public roots\n"+ca {
		t.Error("CA not written")
	}
	want := "A_TOKEN=studio-234567abcdefghijklmnopqrstuvwxyz\nOPENAI_API_KEY=studio-abcdefghijklmnopqrstuvwxyz234567\n"
	if got := read(envFile); got != want {
		t.Errorf("env file = %q, want %q", got, want)
	}
	if refreshes != 1 {
		t.Errorf("trust store refreshed %d times, want 1", refreshes)
	}

	// The same CA again (every reconnect) must not rebuild the trust store.
	delete(cfg.Env, "A_TOKEN")
	if err := applyConfig(root, cfg, refresh); err != nil {
		t.Fatal(err)
	}
	if refreshes != 1 {
		t.Errorf("trust store refreshed %d times for an unchanged CA", refreshes)
	}
	if got := read(envFile); strings.Contains(got, "A_TOKEN") {
		t.Errorf("deleted secret still in the env file: %q", got)
	}

	// The profile hook exports exactly the file's variables into a login shell.
	if _, err := exec.LookPath("sh"); err == nil {
		script := strings.ReplaceAll(read(profileHook), envFile, filepath.Join(root, envFile))
		out, err := exec.Command("sh", "-c", script+"\necho \"$OPENAI_API_KEY|${A_TOKEN-unset}\"").CombinedOutput()
		if err != nil {
			t.Fatalf("%v: %s", err, out)
		}
		if got := strings.TrimSpace(string(out)); got != "studio-abcdefghijklmnopqrstuvwxyz234567|unset" {
			t.Errorf("profile hook exported %q", got)
		}
	}
}

func TestApplyConfigRefuses(t *testing.T) {
	for name, cfg := range map[string]agentproto.Config{
		"not PEM":             {CA: "hello"},
		"not a CA":            {CA: testCert(t, false)},
		"lower-case name":     {Env: map[string]string{"path": "x"}},
		"name with =":         {Env: map[string]string{"A=B": "x"}},
		"value with newline":  {Env: map[string]string{"A": "x\nPATH=/tmp"}},
		"value with command":  {Env: map[string]string{"A": "$(reboot)"}},
		"value with a space":  {Env: map[string]string{"A": "x y"}},
		"name with a newline": {Env: map[string]string{"A\nB": "x"}},
	} {
		root := t.TempDir()
		if err := applyConfig(root, cfg, func() error { return nil }); err == nil {
			t.Errorf("%s: accepted", name)
		}
		if _, err := os.Stat(filepath.Join(root, envFile)); err == nil {
			t.Errorf("%s: env file written", name)
		}
	}
}
