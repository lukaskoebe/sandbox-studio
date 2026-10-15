package login

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/lukaskoebe/sandbox-studio/internal/store"
	"github.com/lukaskoebe/sandbox-studio/internal/teammates"
)

// oauthServer is a fake token endpoint. It answers refreshes with next, or refuses them with
// status, and records what it was sent.
type oauthServer struct {
	srv    *httptest.Server
	mu     sync.Mutex
	status int
	next   map[string]string
	bodies []map[string]string
}

func newOAuthServer(t *testing.T) *oauthServer {
	t.Helper()
	o := &oauthServer{status: http.StatusOK}
	o.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		o.mu.Lock()
		defer o.mu.Unlock()
		var body map[string]string
		b, _ := io.ReadAll(r.Body)
		if err := json.Unmarshal(b, &body); err != nil || r.Method != http.MethodPost {
			http.Error(w, "bad request", http.StatusBadRequest)
			return
		}
		o.bodies = append(o.bodies, body)
		if o.status != http.StatusOK {
			http.Error(w, `{"error":"invalid_grant"}`, o.status)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(o.next)
	}))
	t.Cleanup(o.srv.Close)
	return o
}

func (o *oauthServer) requests() []map[string]string {
	o.mu.Lock()
	defer o.mu.Unlock()
	return append([]map[string]string(nil), o.bodies...)
}

// chatGPTProvider signs a provider in with a login whose access token expires at expires.
func chatGPTProvider(t *testing.T, e *testEnv, expires time.Time) store.Provider {
	t.Helper()
	ctx := context.Background()
	p := e.provider(t, "Work", teammates.KindChatGPTSubscription)
	l := testLogin(t, expires)
	if err := e.svc.storeCodex(ctx, p, l); err != nil {
		t.Fatal(err)
	}
	stored, err := e.st.Provider(ctx, e.env, p.ID)
	if err != nil {
		t.Fatal(err)
	}
	return stored
}

// A login due for refresh is renewed at the token endpoint, its new tokens replace the old ones
// in the key and the login, and a connected teammate sandbox gets the new auth file.
func TestRefreshRenewsADueLogin(t *testing.T) {
	ctx := context.Background()
	e := newTestEnv(t)
	oauth := newOAuthServer(t)
	newAccess := jwt(t, map[string]any{"exp": time.Now().Add(time.Hour).Unix(), "sub": "user-1"}, "newaccesssig")
	oauth.next = map[string]string{"access_token": newAccess, "refresh_token": "rt_rotated"}
	e.svc.TokenURL = oauth.srv.URL
	e.svc.HTTP = oauth.srv.Client()

	p := chatGPTProvider(t, e, time.Now().Add(time.Minute))
	tm, err := e.st.CreateTeammate(ctx, store.Teammate{EnvironmentID: e.env, Name: "ada", Harness: teammates.HarnessCodex, ProviderID: p.ID, GitName: "ada", GitEmail: "ada@agents.invalid"})
	if err != nil {
		t.Fatal(err)
	}
	sb, err := e.st.CreateSandbox(ctx, store.Sandbox{EnvironmentID: e.env, Name: "api", CPUs: 1, TeammateID: tm.ID})
	if err != nil {
		t.Fatal(err)
	}
	e.guest.connected[sb.ID] = true

	e.svc.refreshDue(ctx)

	reqs := oauth.requests()
	if len(reqs) != 1 || reqs[0]["grant_type"] != "refresh_token" || reqs[0]["refresh_token"] != "rt_secretrefresh" || reqs[0]["client_id"] != codexClientID {
		t.Fatalf("the refresh request %+v", reqs)
	}
	if got := e.vault.value(p.SecretID); got != "newaccesssig" {
		t.Fatalf("the key after the refresh is %q", got)
	}
	l, _, ok, err := e.svc.login(ctx, p)
	if err != nil || !ok || l.RefreshToken != "rt_rotated" || l.AccessToken != newAccess || l.LastRefresh == "" {
		t.Fatalf("the login after the refresh: %+v, %v, %v", l, ok, err)
	}
	if expired, err := e.svc.Expired(ctx, p); err != nil || expired {
		t.Fatalf("a renewed login expired: %v, %v", expired, err)
	}
	e.guest.mu.Lock()
	written := e.guest.written
	e.guest.mu.Unlock()
	if len(written) != 1 || len(written[0]) != 1 {
		t.Fatalf("the running sandbox got %d writes; want one auth file", len(written))
	}
	if content := written[0][0].Content; strings.Contains(content, "rt_rotated") || !strings.Contains(content, p.Placeholder) {
		t.Fatalf("the pushed auth file is not the placeholder form of the renewed login:\n%s", content)
	}

	// A login that is not due is left alone.
	e.svc.refreshDue(ctx)
	if n := len(oauth.requests()); n != 1 {
		t.Fatalf("a fresh login was refreshed: %d requests", n)
	}
}

// A refusal leaves the login as it was; once its access token lapses the provider is expired.
func TestRefreshRefusedExpiresTheLogin(t *testing.T) {
	ctx := context.Background()
	e := newTestEnv(t)
	oauth := newOAuthServer(t)
	oauth.status = http.StatusUnauthorized
	e.svc.TokenURL = oauth.srv.URL
	e.svc.HTTP = oauth.srv.Client()

	p := chatGPTProvider(t, e, time.Now().Add(time.Minute))
	if err := e.svc.refreshProvider(ctx, p); !errors.Is(err, ErrSignInAgain) {
		t.Fatalf("a refused refresh: %v", err)
	}
	if expired, _ := e.svc.Expired(ctx, p); expired {
		t.Fatal("expired before the access token lapsed")
	}

	e.svc.Now = func() time.Time { return time.Now().Add(2 * time.Hour) }
	if expired, err := e.svc.Expired(ctx, p); err != nil || !expired {
		t.Fatalf("a lapsed login after a refused refresh: expired %v, %v", expired, err)
	}
}

// A login that isn't due makes no request at all.
func TestRefreshSkipsFreshLogins(t *testing.T) {
	ctx := context.Background()
	e := newTestEnv(t)
	oauth := newOAuthServer(t)
	e.svc.TokenURL = oauth.srv.URL
	e.svc.HTTP = oauth.srv.Client()
	p := chatGPTProvider(t, e, time.Now().Add(3*time.Hour))
	if err := e.svc.refreshProvider(ctx, p); err != nil {
		t.Fatal(err)
	}
	if n := len(oauth.requests()); n != 0 {
		t.Fatalf("%d requests for a login that isn't due", n)
	}
}

// An answer that doesn't hold a login Studio can keep changes nothing.
func TestRefreshKeepsTheLoginOnABadAnswer(t *testing.T) {
	ctx := context.Background()
	e := newTestEnv(t)
	oauth := newOAuthServer(t)
	oauth.next = map[string]string{"access_token": "not-a-jwt", "refresh_token": "rt_rotated"}
	e.svc.TokenURL = oauth.srv.URL
	e.svc.HTTP = oauth.srv.Client()
	p := chatGPTProvider(t, e, time.Now().Add(time.Minute))
	if err := e.svc.refreshProvider(ctx, p); err == nil {
		t.Fatal("a refresh that returned a bad access token was accepted")
	}
	if got := e.vault.value(p.SecretID); got != "accesssig" {
		t.Fatalf("the key changed to %q", got)
	}
}

// Studio's start removes the login VMs an earlier process left, and stops the utility VMs.
func TestSweepRemovesLoginVMsLeftBehind(t *testing.T) {
	ctx := context.Background()
	e := newTestEnv(t)
	if _, err := e.st.CreateSystemSandbox(ctx, store.Sandbox{EnvironmentID: e.env, Name: "login-old"}, store.SandboxKindClaudeLogin); err != nil {
		t.Fatal(err)
	}
	if _, err := e.st.CreateSystemSandbox(ctx, store.Sandbox{EnvironmentID: e.env, Name: "utility-old"}, store.SandboxKindClaudeUtility); err != nil {
		t.Fatal(err)
	}
	e.svc.sweep(ctx)
	if len(e.vms.removed) != 1 || e.vms.removed[0] != "login-old" {
		t.Fatalf("removed %v; want the login VM only", e.vms.removed)
	}
}
