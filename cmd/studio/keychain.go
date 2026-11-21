package main

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/lukaskoebe/sandbox-studio/internal/api"
	"github.com/lukaskoebe/sandbox-studio/internal/secrets"
	"github.com/lukaskoebe/sandbox-studio/internal/store"
	"github.com/lukaskoebe/sandbox-studio/internal/webauth"
	"github.com/lukaskoebe/sandbox-studio/internal/webui"
)

// Vars, so tests and the e2e build can shorten them or stand in for the keychain.
var (
	// keychainRetryGap is the least time between two reads of a locked keychain's key: on
	// Linux each read asks it to unlock with a prompt, and gnome-keyring keeps a file open for
	// every prompt it can't show. Checking whether it is locked asks nothing.
	keychainRetryGap = 15 * time.Second
	// keychainPoll is how often Studio checks whether the keychain unlocked, where it can tell
	// without asking (keychainLocked).
	keychainPoll = 3 * time.Second
	// keychainBlindRetry is how often it reads the key again where it can't tell.
	keychainBlindRetry = time.Minute
	// keychainAsks is how many reads Retry makes while the keychain says it is still locked.
	// Anything on this machine can press it, and each read of a locked keychain prompts: past
	// these, only its unlocking reads the key.
	keychainAsks = 5

	openSecrets    = secrets.Open
	keychainLocked = secrets.KeychainLocked
)

// keychainWait is Studio waiting for the OS keychain that holds the vault key.
type keychainWait struct {
	since time.Time
	nudge chan struct{}

	mu    sync.Mutex
	err   error     // the last read's
	last  time.Time // when the key was last read
	asked bool      // the user asked to read it again
	asks  int       // the reads Retry made while the keychain said it was locked
}

// keychainView is the wait as /api/auth/status, and every other API path, answers it.
type keychainView struct {
	Waiting string    `json:"waiting"` // "keychain"
	Message string    `json:"message"`
	Error   string    `json:"error,omitempty"`
	Since   time.Time `json:"since"`
	// RetryIn is how many seconds Retry waits before it reads the key again.
	RetryIn int `json:"retryIn"`
}

// openVault opens the vault. While the OS keychain that holds its key is locked, as after a
// reboot with only SSH logins, Studio doesn't exit: a service manager would start it again
// every few seconds, and every start would ask the keychain to unlock. It serves a page on
// addr that says so instead, and opens the vault once the keychain unlocks or the user
// retries. The vault is nil when ctx ends first; opened says whether it opened Studio in the
// browser or the app.
func openVault(ctx context.Context, st *store.Store, data, addr string, auth *webauth.Auth, open bool, pause func() func(), log *slog.Logger) (v *secrets.Vault, opened bool, err error) {
	v, err = openSecrets(ctx, st, data, log)
	if !errors.Is(err, secrets.ErrKeychainLocked) {
		return v, false, err
	}
	// An update waiting to serve doesn't roll back while the user is yet to unlock the keychain.
	if pause != nil {
		defer pause()()
	}
	now := time.Now()
	w := &keychainWait{since: now, last: now, err: err, nudge: make(chan struct{}, 1)}
	l, lerr := net.Listen("tcp", addr)
	if lerr != nil {
		return nil, false, lerr
	}
	log.Warn("Studio waits for the OS keychain: unlock it, such as by logging in on the desktop, and Studio starts", "err", err, "url", "http://"+addr)
	srv := &http.Server{Handler: api.Guard(w.handler(auth)), ReadHeaderTimeout: 10 * time.Second}
	go func() { _ = srv.Serve(l) }()
	defer func() {
		shutdown, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = srv.Shutdown(shutdown)
	}()
	if open {
		go openStudio(addr, loginURL(addr, auth.IssueLogin()), log)
		opened = true
	}
	tick := time.NewTicker(keychainPoll)
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil, opened, nil
		case <-w.nudge:
		case <-tick.C:
		}
		if !w.due(time.Now()) {
			continue
		}
		v, err = openSecrets(ctx, st, data, log)
		w.read(time.Now(), err)
		if err == nil {
			log.Info("the OS keychain unlocked; Studio starts")
			return v, opened, nil
		}
		if !errors.Is(err, secrets.ErrKeychainLocked) {
			return nil, opened, err
		}
	}
}

// due says whether to read the key again: when the user asked, when the keychain says it
// unlocked, or, where it can't say, once a minute; never sooner than keychainRetryGap after
// the last read.
func (w *keychainWait) due(now time.Time) bool {
	w.mu.Lock()
	last, asked := w.last, w.asked
	w.mu.Unlock()
	if now.Sub(last) < keychainRetryGap {
		return false
	}
	locked, ok := keychainLocked()
	switch {
	case ok && !locked:
		return true
	case asked && ok:
		w.mu.Lock()
		defer w.mu.Unlock()
		if w.asks >= keychainAsks {
			w.asked = false
			return false
		}
		w.asks++
		return true
	case asked:
		return true
	case ok:
		return false
	}
	return now.Sub(last) >= keychainBlindRetry
}

func (w *keychainWait) read(now time.Time, err error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.last, w.err, w.asked = now, err, false
}

func (w *keychainWait) view(now time.Time) keychainView {
	w.mu.Lock()
	defer w.mu.Unlock()
	v := keychainView{
		Waiting: "keychain",
		Message: "Studio keeps the key to your secrets in the OS keychain, which is locked. Unlock it, such as by logging in on this machine's desktop, and Studio starts by itself.",
		Since:   w.since,
	}
	if w.err != nil {
		v.Error = w.err.Error()
	}
	if w.asks >= keychainAsks {
		v.Error = "Retry asked the keychain to unlock " + strconv.Itoa(keychainAsks) + " times; unlock it on this machine's desktop, and Studio starts by itself."
	}
	if left := keychainRetryGap - now.Sub(w.last); left > 0 {
		v.RetryIn = int(left.Round(time.Second) / time.Second)
	}
	return v
}

// handler answers while Studio waits: /api/auth/status and every API path but the other
// login endpoints answer 503 with the wait, POST /api/keychain/retry reads the key again,
// and the web app loads, to show the wait. Login links and `studio quit` work as ever.
func (w *keychainWait) handler(auth *webauth.Auth) http.Handler {
	rest := auth.Middleware(webui.Handler())
	return http.HandlerFunc(func(rw http.ResponseWriter, r *http.Request) {
		path := r.URL.Path
		switch {
		case path == "/api/keychain/retry":
			if r.Method != http.MethodPost {
				rw.WriteHeader(http.StatusMethodNotAllowed)
				return
			}
			w.mu.Lock()
			w.asked = true
			w.mu.Unlock()
			select {
			case w.nudge <- struct{}{}:
			default:
			}
			w.write(rw, http.StatusAccepted)
		case path == "/api/auth/status", path == "/api" || strings.HasPrefix(path, "/api/") && !strings.HasPrefix(path, "/api/auth/"):
			w.write(rw, http.StatusServiceUnavailable)
		default:
			rest.ServeHTTP(rw, r)
		}
	})
}

func (w *keychainWait) write(rw http.ResponseWriter, status int) {
	rw.Header().Set("Content-Type", "application/json")
	rw.Header().Set("Cache-Control", "no-store")
	rw.Header().Set("X-Studio-Waiting", "keychain")
	if status == http.StatusServiceUnavailable {
		rw.Header().Set("Retry-After", "3")
	}
	rw.WriteHeader(status)
	_ = json.NewEncoder(rw).Encode(w.view(time.Now()))
}
