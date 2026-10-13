package login

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/lukaskoebe/sandbox-studio/internal/store"
	"github.com/lukaskoebe/sandbox-studio/internal/teammates"
)

// A Claude Code sign-in stores the token the CLI prints as the provider's key, bound to the
// Anthropic host, and removes its VM when done.
func TestClaudeSignInStoresTheCapturedToken(t *testing.T) {
	e := newTestEnv(t)
	p := e.provider(t, "Max", teammates.KindClaudeSubscription)
	e.guest.setPane("Open https://claude.ai/oauth/authorize?x=1 and paste the code.", false)

	v, err := e.svc.Start(context.Background(), e.env, p.ID)
	if err != nil || v.State != StateStarting {
		t.Fatalf("start: %+v, %v", v, err)
	}
	waitFor(t, func() bool { return e.svc.Status(p.ID).URL != "" })
	if got := e.svc.Status(p.ID); got.State != StateWaiting || got.URL != "https://claude.ai/oauth/authorize?x=1" || got.Paste {
		t.Fatalf("waiting view %+v", got)
	}
	if !e.vms.running("login-" + p.ID) {
		t.Fatal("the login VM isn't running while the CLI waits")
	}
	if cmds := e.guest.commands(); len(cmds) < 1 || !strings.Contains(cmds[0], "claude setup-token") {
		t.Fatalf("the CLI didn't start: %v", cmds)
	}

	e.guest.setPane("Your OAuth token (valid for 1 year):\n\n"+tokenText+"\n", true)
	if got := e.await(t, p.ID); got.State != StateDone {
		t.Fatalf("end view %+v", got)
	}
	stored, err := e.st.Provider(context.Background(), e.env, p.ID)
	if err != nil || stored.SecretID == "" || stored.Placeholder == "" {
		t.Fatalf("provider after sign-in %+v, %v", stored, err)
	}
	if e.vault.value(stored.SecretID) != tokenText || stored.Hosts[0] != "api.anthropic.com" {
		t.Fatalf("stored key %q for hosts %v", e.vault.value(stored.SecretID), stored.Hosts)
	}
	if e.vms.running("login-" + p.ID) {
		t.Fatal("the login VM outlived its sign-in")
	}
	if files, err := e.svc.GuestFiles(context.Background(), stored); err != nil || files != nil {
		t.Fatalf("a Claude session gets files %v, %v; it gets its token as a placeholder", files, err)
	}
}

// A CLI that ends without a token Studio can read ends failed, offering the paste.
func TestClaudeSignInOffersPasteWhenNoTokenIsRead(t *testing.T) {
	e := newTestEnv(t)
	p := e.provider(t, "Max", teammates.KindClaudeSubscription)
	e.guest.setPane("Login failed.", true)
	if _, err := e.svc.Start(context.Background(), e.env, p.ID); err != nil {
		t.Fatal(err)
	}
	got := e.await(t, p.ID)
	if got.State != StateFailed || !got.Paste || got.Message == "" {
		t.Fatalf("view %+v; want a failed sign-in offering the paste", got)
	}
	if e.vms.running("login-" + p.ID) {
		t.Fatal("the login VM outlived its failed sign-in")
	}
	stored, _ := e.st.Provider(context.Background(), e.env, p.ID)
	if stored.SecretID != "" {
		t.Fatal("a failed sign-in stored a key")
	}
}

// A pasted token is stored like a captured one, and it ends a running sign-in.
func TestPasteTokenEndsTheSignIn(t *testing.T) {
	ctx := context.Background()
	e := newTestEnv(t)
	p := e.provider(t, "Max", teammates.KindClaudeSubscription)
	e.guest.setPane("waiting", false)
	if _, err := e.svc.Start(ctx, e.env, p.ID); err != nil {
		t.Fatal(err)
	}
	waitFor(t, func() bool { return e.svc.Status(p.ID).State == StateWaiting })

	if err := e.svc.PasteToken(ctx, e.env, p.ID, "sk-ant-oat01-short"); !errors.Is(err, ErrInvalidToken) {
		t.Fatalf("a short token: %v", err)
	}
	if err := e.svc.PasteToken(ctx, e.env, p.ID, "  "+tokenText+"\n"); err != nil {
		t.Fatal(err)
	}
	if got := e.svc.Status(p.ID); got.State != StateIdle {
		t.Fatalf("the running sign-in after a paste: %+v", got)
	}
	stored, _ := e.st.Provider(ctx, e.env, p.ID)
	if e.vault.value(stored.SecretID) != tokenText {
		t.Fatalf("the pasted token was not stored: %q", e.vault.value(stored.SecretID))
	}
	if e.vms.running("login-" + p.ID) {
		t.Fatal("the login VM outlived the paste")
	}
}

// Cancel ends a sign-in in progress, idle, and removes its VM.
func TestCancelEndsTheSignInIdle(t *testing.T) {
	ctx := context.Background()
	e := newTestEnv(t)
	p := e.provider(t, "Max", teammates.KindClaudeSubscription)
	e.guest.setPane("waiting", false)
	if _, err := e.svc.Start(ctx, e.env, p.ID); err != nil {
		t.Fatal(err)
	}
	waitFor(t, func() bool { return e.svc.Status(p.ID).State == StateWaiting })
	if err := e.svc.Cancel(ctx, e.env, p.ID); err != nil {
		t.Fatal(err)
	}
	if got := e.svc.Status(p.ID); got.State != StateIdle || got.Message != "" {
		t.Fatalf("cancelled view %+v", got)
	}
	if e.vms.running("login-" + p.ID) {
		t.Fatal("the login VM outlived the cancel")
	}
}

// A sign-in that runs past its timeout fails, and its VM goes.
func TestSignInTimesOut(t *testing.T) {
	e := newTestEnv(t)
	e.svc.Timeout = 60 * time.Millisecond
	p := e.provider(t, "Max", teammates.KindClaudeSubscription)
	e.guest.setPane("still waiting", false)
	if _, err := e.svc.Start(context.Background(), e.env, p.ID); err != nil {
		t.Fatal(err)
	}
	if got := e.await(t, p.ID); got.State != StateFailed || !strings.Contains(got.Message, "timed out") {
		t.Fatalf("timed-out view %+v", got)
	}
	if e.vms.running("login-" + p.ID) {
		t.Fatal("the login VM outlived its timeout")
	}
}

// A VM that doesn't start fails the sign-in with a message, not with the VM's error alone.
func TestSignInFailsWhenTheVMDoesNotStart(t *testing.T) {
	e := newTestEnv(t)
	p := e.provider(t, "Max", teammates.KindClaudeSubscription)
	e.vms.failOn = "login-" + p.ID
	if _, err := e.svc.Start(context.Background(), e.env, p.ID); err != nil {
		t.Fatal(err)
	}
	if got := e.await(t, p.ID); got.State != StateFailed || !strings.Contains(got.Message, "login VM didn't start") {
		t.Fatalf("view %+v", got)
	}
}

// A ChatGPT sign-in shows the device page and code, then stores the login from the auth file the
// CLI writes: the access token's signature as the key, the login itself studio-only, and a guest
// file made of placeholders.
func TestChatGPTSignInStoresTheDeviceLogin(t *testing.T) {
	ctx := context.Background()
	e := newTestEnv(t)
	p := e.provider(t, "Work", teammates.KindChatGPTSubscription)
	e.guest.setPane("Open https://auth.openai.com/codex/device and enter ABCD-EFGH", false)
	if _, err := e.svc.Start(ctx, e.env, p.ID); err != nil {
		t.Fatal(err)
	}
	waitFor(t, func() bool { return e.svc.Status(p.ID).Code == "ABCD-EFGH" })
	if got := e.svc.Status(p.ID); got.URL != "https://auth.openai.com/codex/device" || got.State != StateWaiting {
		t.Fatalf("device view %+v", got)
	}

	e.guest.setFile(".codex/auth.json", authJSON(t, time.Now().Add(time.Hour), "rt_real_refresh"))
	if got := e.await(t, p.ID); got.State != StateDone {
		t.Fatalf("end view %+v", got)
	}
	stored, err := e.st.Provider(ctx, e.env, p.ID)
	if err != nil || stored.SecretID == "" || stored.Hosts[0] != "chatgpt.com" {
		t.Fatalf("provider %+v, %v", stored, err)
	}
	if got := e.vault.value(stored.SecretID); got != "accesssig" {
		t.Fatalf("the provider's key is %q; want the access token's signature only", got)
	}
	login, ok, err := e.svc.loginSecret(ctx, stored)
	if err != nil || !ok || !login.StudioOnly || len(login.Hosts) != 0 {
		t.Fatalf("the studio-only login: %+v, %v, %v", login, ok, err)
	}
	if expired, err := e.svc.Expired(ctx, stored); err != nil || expired {
		t.Fatalf("a fresh login expired: %v, %v", expired, err)
	}

	files, err := e.svc.GuestFiles(ctx, stored)
	if err != nil || len(files) != 1 || files[0].Path != ".codex/auth.json" || files[0].Mode != 0o600 {
		t.Fatalf("guest files %+v, %v", files, err)
	}
	if strings.Contains(files[0].Content, "rt_real_refresh") || strings.Contains(files[0].Content, "accesssig") {
		t.Fatalf("the guest's auth file holds a secret:\n%s", files[0].Content)
	}
	if !strings.Contains(files[0].Content, stored.Placeholder) {
		t.Fatalf("the guest's access token isn't the provider's placeholder:\n%s", files[0].Content)
	}
}

// Sign-ins are only for subscription providers, and starting one twice shows the same sign-in.
func TestSignInRefusesOtherProvidersAndRunsOnce(t *testing.T) {
	ctx := context.Background()
	e := newTestEnv(t)
	key, err := e.st.CreateProvider(ctx, store.Provider{ID: store.NewID(), EnvironmentID: e.env, Name: "Key", Kind: teammates.KindAnthropicAPI}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := e.svc.Start(ctx, e.env, key.ID); !errors.Is(err, ErrNotSubscription) {
		t.Fatalf("an API key provider signed in: %v", err)
	}
	p := e.provider(t, "Max", teammates.KindClaudeSubscription)
	e.guest.setPane("waiting", false)
	if _, err := e.svc.Start(ctx, e.env, p.ID); err != nil {
		t.Fatal(err)
	}
	waitFor(t, func() bool { return e.svc.Status(p.ID).State == StateWaiting })
	if v, err := e.svc.Start(ctx, e.env, p.ID); err != nil || v.State != StateWaiting {
		t.Fatalf("a second start: %+v, %v", v, err)
	}
	if n := countRuns(e.guest, "tmux new-session"); n != 1 {
		t.Fatalf("the CLI started %d times", n)
	}
	if _, err := e.svc.Terminal(ctx, e.env, key.ID, 80, 24); !errors.Is(err, ErrNoLogin) {
		t.Fatalf("a terminal for a provider with no sign-in: %v", err)
	}
	_ = e.svc.Cancel(ctx, e.env, p.ID)
}

func countRuns(g *fakeGuest, prefix string) int {
	n := 0
	for _, line := range g.commands() {
		if strings.HasPrefix(line, prefix) {
			n++
		}
	}
	return n
}

// waitFor polls cond until it holds, failing the test after a few seconds.
func waitFor(t *testing.T, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatal("condition not reached")
		}
		time.Sleep(2 * time.Millisecond)
	}
}

// A session that vanishes while the CLI waits (its tmux server was killed) ends the sign-in
// without a token: the dialog offers the paste.
func TestSignInEndsWhenItsSessionIsGone(t *testing.T) {
	e := newTestEnv(t)
	p := e.provider(t, "Max", teammates.KindClaudeSubscription)
	e.guest.setPane("waiting", false)
	if _, err := e.svc.Start(context.Background(), e.env, p.ID); err != nil {
		t.Fatal(err)
	}
	waitFor(t, func() bool { return e.svc.Status(p.ID).State == StateWaiting })
	e.guest.mu.Lock()
	e.guest.gone = true
	e.guest.mu.Unlock()
	if got := e.await(t, p.ID); got.State != StateFailed || !got.Paste {
		t.Fatalf("view after the session vanished: %+v", got)
	}
}
