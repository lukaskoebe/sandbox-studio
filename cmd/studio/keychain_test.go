package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/lukaskoebe/sandbox-studio/internal/secrets"
	"github.com/lukaskoebe/sandbox-studio/internal/store"
	"github.com/lukaskoebe/sandbox-studio/internal/webauth"
)

// fakeKeychain stands in for the keychain: locked until unlock, counting reads of the key.
type fakeKeychain struct {
	locked atomic.Bool
	reads  atomic.Int32
	vault  *secrets.Vault
}

func useFakeKeychain(t *testing.T) *fakeKeychain {
	t.Helper()
	k := &fakeKeychain{vault: &secrets.Vault{}}
	k.locked.Store(true)
	open, locked, gap, poll, blind := openSecrets, keychainLocked, keychainRetryGap, keychainPoll, keychainBlindRetry
	t.Cleanup(func() {
		openSecrets, keychainLocked, keychainRetryGap, keychainPoll, keychainBlindRetry = open, locked, gap, poll, blind
	})
	openSecrets = func(context.Context, *store.Store, string, *slog.Logger) (*secrets.Vault, error) {
		k.reads.Add(1)
		if k.locked.Load() {
			return nil, fmt.Errorf("%w (prompt dismissed); unlock it", secrets.ErrKeychainLocked)
		}
		return k.vault, nil
	}
	keychainLocked = func() (bool, bool) { return k.locked.Load(), true }
	keychainRetryGap, keychainPoll, keychainBlindRetry = 100*time.Millisecond, 10*time.Millisecond, time.Hour
	return k
}

func waitingAuth(t *testing.T) *webauth.Auth {
	t.Helper()
	auth, err := webauth.New(bytes.Repeat([]byte{7}, 32), func(string) bool { return false })
	if err != nil {
		t.Fatal(err)
	}
	return auth
}

func freeAddr(t *testing.T) string {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	return l.Addr().String()
}

type vaultResult struct {
	v   *secrets.Vault
	err error
}

func startOpenVault(ctx context.Context, t *testing.T, addr string) <-chan vaultResult {
	t.Helper()
	done := make(chan vaultResult, 1)
	go func() {
		v, _, err := openVault(ctx, nil, t.TempDir(), addr, waitingAuth(t), false, slog.New(slog.DiscardHandler))
		done <- vaultResult{v, err}
	}()
	return done
}

func request(t *testing.T, method, url, origin string) (*http.Response, keychainView) {
	t.Helper()
	req, err := http.NewRequest(method, url, nil)
	if err != nil {
		t.Fatal(err)
	}
	if origin != "" {
		req.Header.Set("Origin", origin)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("%s %s: %v", method, url, err)
	}
	defer resp.Body.Close()
	var v keychainView
	if strings.HasPrefix(resp.Header.Get("Content-Type"), "application/json") {
		_ = json.NewDecoder(resp.Body).Decode(&v)
	}
	return resp, v
}

func eventually(t *testing.T, what string, ok func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for !ok() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting: %s", what)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func TestOpenVaultWaitsForALockedKeychainWithoutAskingItAgain(t *testing.T) {
	k := useFakeKeychain(t)
	addr := freeAddr(t)
	done := startOpenVault(context.Background(), t, addr)
	base := "http://" + addr

	eventually(t, "Studio listens while it waits", func() bool {
		resp, err := http.Get(base + "/api/auth/status")
		if err == nil {
			resp.Body.Close()
		}
		return err == nil
	})
	resp, v := request(t, http.MethodGet, base+"/api/auth/status", "")
	if resp.StatusCode != http.StatusServiceUnavailable || resp.Header.Get("X-Studio-Waiting") != "keychain" || v.Waiting != "keychain" ||
		!strings.Contains(v.Error, "prompt dismissed") || !strings.Contains(v.Message, "logging in on this computer's desktop") {
		t.Fatalf("status while waiting: %d %q %+v", resp.StatusCode, resp.Header.Get("X-Studio-Waiting"), v)
	}
	if resp, _ := request(t, http.MethodGet, base+"/api/environments", ""); resp.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("an API path while waiting: %d", resp.StatusCode)
	}
	// The login endpoints are still the auth middleware's: `studio quit` and launch links work.
	if resp, _ := request(t, http.MethodPost, base+"/api/auth/quit", ""); resp.StatusCode == http.StatusServiceUnavailable || resp.StatusCode == http.StatusOK {
		t.Fatalf("quit without the control token: %d", resp.StatusCode)
	}

	// Checking whether it is locked never reads the key, which would ask the keychain again.
	time.Sleep(300 * time.Millisecond)
	if n := k.reads.Load(); n != 1 {
		t.Fatalf("the key was read %d times while the keychain stayed locked, want 1", n)
	}

	// Retry reads it again; another site's page can't ask for that.
	if resp, _ := request(t, http.MethodPost, base+"/api/keychain/retry", "http://elsewhere.example"); resp.StatusCode != http.StatusForbidden {
		t.Fatalf("a cross-origin retry: %d", resp.StatusCode)
	}
	if resp, _ := request(t, http.MethodPost, base+"/api/keychain/retry", ""); resp.StatusCode != http.StatusAccepted {
		t.Fatalf("retry: %d", resp.StatusCode)
	}
	eventually(t, "retry reads the key", func() bool { return k.reads.Load() == 2 })

	// Unlocked, Studio opens the vault by itself and stops serving the wait.
	k.locked.Store(false)
	select {
	case r := <-done:
		if r.err != nil || r.v != k.vault {
			t.Fatalf("openVault after the unlock: %v %v", r.v, r.err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("openVault didn't return once the keychain unlocked")
	}
	if n := k.reads.Load(); n != 3 {
		t.Fatalf("the key was read %d times, want 3", n)
	}
	if resp, err := http.Get(base + "/api/auth/status"); err == nil {
		resp.Body.Close()
		t.Fatalf("the wait still answers after the vault opened: %d", resp.StatusCode)
	}
}

func TestOpenVaultRetriesNoSoonerThanTheGap(t *testing.T) {
	k := useFakeKeychain(t)
	keychainRetryGap = time.Hour
	addr := freeAddr(t)
	ctx, cancel := context.WithCancel(context.Background())
	done := startOpenVault(ctx, t, addr)
	base := "http://" + addr
	eventually(t, "Studio listens while it waits", func() bool {
		resp, err := http.Get(base + "/api/auth/status")
		if err == nil {
			resp.Body.Close()
		}
		return err == nil
	})
	for range 3 {
		resp, v := request(t, http.MethodPost, base+"/api/keychain/retry", "")
		if resp.StatusCode != http.StatusAccepted || v.RetryIn < 3500 {
			t.Fatalf("retry within the gap: %d %+v", resp.StatusCode, v)
		}
	}
	// Unlocked too: the gap still holds, as the keychain may not have said so yet.
	k.locked.Store(false)
	time.Sleep(200 * time.Millisecond)
	if n := k.reads.Load(); n != 1 {
		t.Fatalf("the key was read %d times within the gap, want 1", n)
	}

	// Studio stopping while it waits is no error: under systemd it isn't restarted.
	cancel()
	select {
	case r := <-done:
		if r.err != nil || r.v != nil {
			t.Fatalf("openVault after a stop: %v %v", r.v, r.err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("openVault didn't return once Studio stopped")
	}
}

func TestOpenVaultDoesntWaitForOtherFailures(t *testing.T) {
	useFakeKeychain(t)
	missing := errors.New("the vault key is kept in the OS keychain, but it is missing")
	openSecrets = func(context.Context, *store.Store, string, *slog.Logger) (*secrets.Vault, error) { return nil, missing }
	addr := freeAddr(t)
	r := <-startOpenVault(context.Background(), t, addr)
	if !errors.Is(r.err, missing) {
		t.Fatalf("openVault with a missing key: %v", r.err)
	}
	if resp, err := http.Get("http://" + addr + "/api/auth/status"); err == nil {
		resp.Body.Close()
		t.Fatal("Studio waited for a key that won't come")
	}
}
