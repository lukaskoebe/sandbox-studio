//go:build linux

package guest

import (
	"bytes"
	"context"
	"crypto/sha256"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

const (
	browserCertutil       = "/usr/bin/certutil"
	browserTrustNickname  = "sandbox-studio-browser-ca"
	browserTrustNSSDBPath = ".pki/nssdb"
	browserTrustOutputMax = 1 << 20
)

// Chromium on Linux recognizes this NSS database path, including on Chromium 146+.
// Keep it outside the persistent browser profile so CA updates never touch cookies.
type browserTrustState struct {
	mu       sync.Mutex
	caDigest [sha256.Size]byte
	ready    bool
}

type browserTrustRunner func(context.Context, ...string) ([]byte, error)

// prepareBrowserTrust serializes NSS updates and only caches a CA after the database is
// known to contain it with SSL CA trust. The parameters make the filesystem and certutil
// runner injectable so tests never touch the VM or host trust store.
func prepareBrowserTrust(ctx context.Context, state *browserTrustState, caPath, nssDir string, uid, gid uint32, run browserTrustRunner) error {
	state.mu.Lock()
	defer state.mu.Unlock()

	if err := ctx.Err(); err != nil {
		return fmt.Errorf("browser trust: command context ended: %w", err)
	}
	caPEM, err := os.ReadFile(caPath)
	if err != nil {
		return fmt.Errorf("browser trust: Studio CA is unavailable at %s; retry after environment configuration completes: %w", caPath, err)
	}
	ca, err := parseBrowserTrustCA(caPEM)
	if err != nil {
		return fmt.Errorf("browser trust: invalid Studio CA at %s: %w", caPath, err)
	}
	digest := sha256.Sum256(ca.Raw)

	if err := prepareBrowserNSSDirectories(nssDir, uid, gid); err != nil {
		return fmt.Errorf("browser trust: prepare agent NSS database directory %s: %w", nssDir, err)
	}
	dbExists, err := browserNSSDatabaseExists(nssDir)
	if err != nil {
		return fmt.Errorf("browser trust: inspect agent NSS database %s: %w", nssDir, err)
	}
	if state.ready && state.caDigest == digest && dbExists {
		return nil
	}
	state.ready = false

	dbArg := "sql:" + nssDir
	if !dbExists {
		if _, err := runBrowserTrustCommand(ctx, run, "-N", "-d", dbArg, "--empty-password"); err != nil {
			return err
		}
	}

	listing, err := runBrowserTrustCommand(ctx, run, "-L", "-d", dbArg)
	if err != nil {
		return err
	}
	trust, found := browserTrustEntry(listing, browserTrustNickname)
	if found {
		oldPEM, err := runBrowserTrustCommand(ctx, run, "-L", "-a", "-d", dbArg, "-n", browserTrustNickname)
		if err != nil {
			return err
		}
		oldCA, parseErr := parseBrowserTrustCA(oldPEM)
		if parseErr == nil && bytes.Equal(oldCA.Raw, ca.Raw) {
			if trust != "C,," {
				if _, err := runBrowserTrustCommand(ctx, run, "-M", "-d", dbArg, "-n", browserTrustNickname, "-t", "C,,"); err != nil {
					return err
				}
			}
			state.caDigest, state.ready = digest, true
			return nil
		}
		// Only the entry owned by Studio is replaced; other NSS certificates remain.
		if _, err := runBrowserTrustCommand(ctx, run, "-D", "-d", dbArg, "-n", browserTrustNickname); err != nil {
			return err
		}
	}

	if _, err := runBrowserTrustCommand(ctx, run, "-A", "-d", dbArg, "-n", browserTrustNickname, "-t", "C,,", "-i", caPath); err != nil {
		return err
	}
	state.caDigest, state.ready = digest, true
	return nil
}

func (a *Agent) prepareBrowserTrust(ctx context.Context) error {
	// Configuration writes caFile atomically under configMu. Hold it while reading and
	// importing so the digest and the file certutil reads always refer to the same CA.
	a.configMu.Lock()
	defer a.configMu.Unlock()
	return prepareBrowserTrust(ctx, &a.browserTrust, caFile, filepath.Join(a.user.home, browserTrustNSSDBPath), a.user.uid, a.user.gid, a.runBrowserTrustCertutil)
}

func (a *Agent) runBrowserTrustCertutil(ctx context.Context, args ...string) ([]byte, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	cmd := a.command(browserCertutil, args...)
	var stdout, stderr capped
	stdout.max, stderr.max = browserTrustOutputMax, 4<<10
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	cmd.WaitDelay = 2 * time.Second
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	select {
	case err := <-done:
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		if stdout.over || stderr.over {
			return nil, errors.New("certutil output exceeded the limit")
		}
		if err != nil {
			return nil, fmt.Errorf("%w: %s", err, strings.TrimSpace(stderr.buf.String()))
		}
		return stdout.buf.Bytes(), nil
	case <-ctx.Done():
		_ = cmd.Process.Kill()
		<-done
		return nil, ctx.Err()
	}
}

func runBrowserTrustCommand(ctx context.Context, run browserTrustRunner, args ...string) ([]byte, error) {
	if err := ctx.Err(); err != nil {
		return nil, fmt.Errorf("browser trust: certutil %s: %w", strings.Join(args, " "), err)
	}
	out, err := run(ctx, args...)
	if err != nil {
		return nil, fmt.Errorf("browser trust: certutil %s: %w", strings.Join(args, " "), err)
	}
	return out, nil
}

func prepareBrowserNSSDirectories(nssDir string, uid, gid uint32) error {
	for _, dir := range []string{filepath.Dir(nssDir), nssDir} {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			return err
		}
		if err := os.Chown(dir, int(uid), int(gid)); err != nil {
			return err
		}
		if err := os.Chmod(dir, 0o700); err != nil {
			return err
		}
	}
	return nil
}

func browserNSSDatabaseExists(nssDir string) (bool, error) {
	// Include DBM marker names so an older existing database is never overwritten by
	// initialization of a new SQL database in the same directory.
	for _, name := range []string{"cert9.db", "key4.db", "pkcs11.txt", "cert8.db", "key3.db"} {
		_, err := os.Stat(filepath.Join(nssDir, name))
		if err == nil {
			return true, nil
		}
		if !os.IsNotExist(err) {
			return false, err
		}
	}
	return false, nil
}

func browserTrustEntry(listing []byte, nickname string) (string, bool) {
	for _, line := range strings.Split(string(listing), "\n") {
		fields := strings.Fields(line)
		if len(fields) > 0 && fields[0] == nickname {
			if len(fields) > 1 {
				return fields[1], true
			}
			return "", true
		}
	}
	return "", false
}

func parseBrowserTrustCA(data []byte) (*x509.Certificate, error) {
	block, _ := pem.Decode(data)
	if block == nil || block.Type != "CERTIFICATE" {
		return nil, errors.New("expected a PEM certificate")
	}
	cert, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		return nil, err
	}
	if !cert.IsCA {
		return nil, errors.New("certificate is not a CA")
	}
	return cert, nil
}
