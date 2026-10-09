//go:build linux

package guest

import (
	"bytes"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"strings"

	"github.com/lukaskoebe/sandbox-studio/internal/agentproto"
)

// Files the environment's configuration is written to. Docker containers get ConfigDir too
// (see OCIRuntime).
const (
	ConfigDir   = "/etc/sandbox-studio"
	envFile     = ConfigDir + "/env"
	caFile      = ConfigDir + "/ca.crt"
	trustedCA   = "/usr/local/share/ca-certificates/sandbox-studio.crt"
	systemRoots = "/etc/ssl/certs/ca-certificates.crt" // built by update-ca-certificates
	profileHook = "/etc/profile.d/sandbox-studio-env.sh"
)

// The hook exports the current secrets into every new login shell, which includes new tmux
// windows; shells that are already running keep what they had.
const profileScript = `# Written by the Sandbox Studio guest agent: the environment's secrets, as placeholders.
if [ -r ` + envFile + ` ]; then
    while IFS='=' read -r name value; do
        [ -n "$name" ] && export "$name=$value"
    done < ` + envFile + `
fi
`

var (
	envName  = regexp.MustCompile(`^[A-Z_][A-Z0-9_]*$`)
	envValue = regexp.MustCompile(`^[A-Za-z0-9._-]*$`)
)

// applyConfig writes cfg below root ("/" in a sandbox). refreshCAs rebuilds the system
// trust store; it runs only when the CA changed.
func applyConfig(root string, cfg agentproto.Config, refreshCAs func() error) error {
	var ca []byte
	if cfg.CA != "" {
		block, _ := pem.Decode([]byte(cfg.CA))
		if block == nil || block.Type != "CERTIFICATE" {
			return errors.New("the CA is not a PEM certificate")
		}
		if cert, err := x509.ParseCertificate(block.Bytes); err != nil || !cert.IsCA {
			return fmt.Errorf("the CA is not a CA certificate: %v", err)
		}
		ca = pem.EncodeToMemory(block)
	}
	// Only placeholders travel here, but the file is sourced by shells: refuse anything a
	// shell could read as more than a plain assignment.
	names := make([]string, 0, len(cfg.Env))
	for name, value := range cfg.Env {
		if !envName.MatchString(name) || !envValue.MatchString(value) {
			return fmt.Errorf("invalid environment variable %q", name)
		}
		names = append(names, name)
	}
	slices.Sort(names)

	at := func(path string) string { return filepath.Join(root, path) }
	if err := os.MkdirAll(at(ConfigDir), 0o755); err != nil {
		return err
	}
	if ca != nil {
		if _, err := writeIfChanged(at(caFile), ca); err != nil {
			return err
		}
		if err := os.MkdirAll(filepath.Dir(at(trustedCA)), 0o755); err != nil {
			return err
		}
		changed, err := writeIfChanged(at(trustedCA), ca)
		if err != nil {
			return err
		}
		if changed {
			if err := refreshCAs(); err != nil {
				os.Remove(at(trustedCA)) // so the next push tries again
				return fmt.Errorf("update-ca-certificates: %w", err)
			}
		}
		// Containers get the system roots from here (see OCIRuntime). Copied on every push
		// in case the ca-certificates package was upgraded in between.
		roots, err := os.ReadFile(at(systemRoots))
		if err != nil {
			return err
		}
		if _, err := writeIfChanged(at(caBundle), roots); err != nil {
			return err
		}
	}

	var env strings.Builder
	for _, name := range names {
		env.WriteString(name + "=" + cfg.Env[name] + "\n")
	}
	if _, err := writeIfChanged(at(envFile), []byte(env.String())); err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(at(profileHook)), 0o755); err != nil {
		return err
	}
	_, err := writeIfChanged(at(profileHook), []byte(profileScript))
	return err
}

// writeIfChanged replaces path with data (mode 0644) unless it already holds it. The
// replacement is atomic, so readers never see half a file.
func writeIfChanged(path string, data []byte) (bool, error) {
	if old, err := os.ReadFile(path); err == nil && bytes.Equal(old, data) {
		return false, nil
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return false, err
	}
	if err := os.Chmod(tmp, 0o644); err != nil { // WriteFile keeps an existing file's mode
		return false, err
	}
	return true, os.Rename(tmp, path)
}

func updateCACertificates() error {
	out, err := exec.Command("update-ca-certificates").CombinedOutput()
	if err != nil {
		return fmt.Errorf("%w: %s", err, bytes.TrimSpace(out))
	}
	return nil
}
