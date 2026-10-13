package policy

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/lukaskoebe/sandbox-studio/internal/store"
)

// The decision table: every level against the curated list, a placeholder, and a host that is a
// name or a public address. Rules, the tracker list and approvals come before it (TestLevels).
func TestLevelDecisionTable(t *testing.T) {
	type row struct {
		level                string
		curated, placeholder bool
		public               bool
		want                 verdict
	}
	rows := []row{
		// Locked asks for everything, as before levels.
		{store.LevelLocked, false, false, true, ask},
		{store.LevelLocked, true, false, true, ask},
		{store.LevelLocked, true, true, true, ask},
		{store.LevelLocked, false, false, false, ask},
		// Guarded allows the curated list and asks for the rest.
		{store.LevelGuarded, false, false, true, ask},
		{store.LevelGuarded, true, false, true, allowCurated},
		{store.LevelGuarded, false, false, false, ask},
		// Assisted allows the curated list too; the reviewer is decided in the engine.
		{store.LevelAssisted, false, false, true, ask},
		{store.LevelAssisted, true, false, true, allowCurated},
		// Open allows any public host, curated or not.
		{store.LevelOpen, false, false, true, allowOpen},
		{store.LevelOpen, true, false, true, allowOpen},
		{store.LevelOpen, false, false, false, ask},
		{store.LevelOpen, true, false, false, ask},
		// A placeholder to a host it isn't bound to is asked at every level.
		{store.LevelGuarded, true, true, true, ask},
		{store.LevelAssisted, true, true, true, ask},
		{store.LevelOpen, false, true, true, ask},
		{store.LevelOpen, true, true, true, ask},
	}
	for _, r := range rows {
		in := levelInput{level: r.level, curated: r.curated, placeholder: r.placeholder, public: r.public}
		if got := in.decide(); got != r.want {
			t.Errorf("%s curated=%v placeholder=%v public=%v: got %v, want %v", r.level, r.curated, r.placeholder, r.public, got, r.want)
		}
	}
}

func TestPublicHost(t *testing.T) {
	for host, want := range map[string]bool{
		"registry.npmjs.org": true, // names are checked by the dialer
		"localhost":          true,
		"1.1.1.1":            true,
		"2606:4700::1111":    true,
		"10.0.0.5":           false, // private
		"192.168.1.1":        false,
		"127.0.0.1":          false,
		"169.254.169.254":    false, // link-local, the cloud metadata address
		"100.64.1.1":         false, // carrier-grade NAT, also Tailscale
		"198.18.0.1":         false, // Studio's virtual services
		"::1":                false,
		"fd00::1":            false,
	} {
		if got := publicHost(host); got != want {
			t.Errorf("publicHost(%s) = %v, want %v", host, got, want)
		}
	}
}

// TestLevels runs the engine over the levels: the rules, the tracker list, placeholders, private
// addresses, a teammate's override and the Activity record of an allow.
func TestLevels(t *testing.T) {
	ctx := context.Background()
	e, env, sb := newEngine(t)
	e.Hold = 30 * time.Millisecond // unanswered connections are asked, and time out quickly
	tm, err := e.Store.CreateTeammate(ctx, store.Teammate{EnvironmentID: env.ID, Name: "iris", Harness: "claude", GitName: "Iris", GitEmail: "iris@agents.invalid"})
	if err != nil {
		t.Fatal(err)
	}
	owned, err := e.Store.CreateSandbox(ctx, store.Sandbox{EnvironmentID: env.ID, Name: "owned", CPUs: 1, TeammateID: tm.ID})
	if err != nil {
		t.Fatal(err)
	}
	setLevel := func(level string) {
		t.Helper()
		if err := e.Store.SetSecurityLevel(ctx, env.ID, level); err != nil {
			t.Fatal(err)
		}
	}
	decide := func(sandbox store.Sandbox, host string, port int, placeholder bool) (store.Rule, error) {
		t.Helper()
		return e.Decide(ctx, Request{EnvironmentID: env.ID, SandboxID: sandbox.ID, SandboxName: sandbox.Name, Host: host, Port: port, Placeholder: placeholder})
	}
	expectAllowed := func(name string, r store.Rule, err error) {
		t.Helper()
		if err != nil || r.Action != store.ActionAllow || r.ID != "" {
			t.Errorf("%s: got rule %+v, %v; want an allow made by the level", name, r, err)
		}
	}
	expectAsked := func(name string, err error) {
		t.Helper()
		if !errors.Is(err, ErrUndecided) {
			t.Errorf("%s: got %v; want the connection to be asked", name, err)
		}
	}

	// Guarded is the default, and asks for a host no rule covers.
	setLevel(store.LevelGuarded)
	_, err = decide(sb, "example.org", 443, false)
	expectAsked("guarded asks", err)

	// Locked asks for everything, public or not.
	setLevel(store.LevelLocked)
	_, err = decide(sb, "example.org", 443, false)
	expectAsked("locked asks", err)

	// Open allows public names and addresses, and logs the allow.
	setLevel(store.LevelOpen)
	r, err := decide(sb, "example.org", 443, false)
	expectAllowed("open allows a public name", r, err)
	r, err = decide(sb, "1.1.1.1", 443, false)
	expectAllowed("open allows a public address", r, err)
	_, err = decide(sb, "10.0.0.5", 443, false)
	expectAsked("open asks for a private address", err)
	_, err = decide(sb, "example.org", 443, true)
	expectAsked("open asks for a placeholder to an unbound host", err)
	logged, _ := e.Store.Approvals(ctx, env.ID, store.StatusApproved, 10)
	if len(logged) != 2 || logged[0].Attempts != 1 {
		t.Fatalf("activity of the allows: %+v", logged)
	}
	if got := string(logged[0].Payload); !strings.Contains(got, `"decided":"open"`) {
		t.Errorf("the allow doesn't say it was decided by the level: %s", got)
	}

	// An explicit rule wins over the level, in both directions.
	setLevel(store.LevelOpen)
	if _, err := e.Store.CreateRule(ctx, store.Rule{EnvironmentID: env.ID, Host: "blocked.example.org", Action: store.ActionDeny}); err != nil {
		t.Fatal(err)
	}
	if err := e.Settle(ctx, env.ID); err != nil {
		t.Fatal(err)
	}
	r, err = decide(sb, "blocked.example.org", 443, false)
	if err != nil || r.Action != store.ActionDeny {
		t.Errorf("a deny rule under open: %+v, %v", r, err)
	}
	if _, err := e.Store.CreateRule(ctx, store.Rule{EnvironmentID: env.ID, Host: "asked.example.org", Action: store.ActionAllow, SandboxID: sb.ID}); err != nil {
		t.Fatal(err)
	}
	if err := e.Settle(ctx, env.ID); err != nil {
		t.Fatal(err)
	}
	setLevel(store.LevelLocked)
	r, err = decide(sb, "asked.example.org", 443, false)
	if err != nil || r.Action != store.ActionAllow || r.ID == "" {
		t.Errorf("an allow rule under locked: %+v, %v", r, err)
	}

	// A teammate's override wins over the environment's level, and "" hands it back.
	setLevel(store.LevelOpen)
	if _, err := e.Store.SetTeammateSecurityLevel(ctx, env.ID, tm.ID, store.LevelLocked); err != nil {
		t.Fatal(err)
	}
	_, err = decide(owned, "example.net", 443, false)
	expectAsked("a locked teammate under an open environment", err)
	r, err = decide(sb, "example.net", 443, false)
	expectAllowed("the environment's own sandboxes stay open", r, err)
	setLevel(store.LevelLocked)
	if _, err := e.Store.SetTeammateSecurityLevel(ctx, env.ID, tm.ID, store.LevelOpen); err != nil {
		t.Fatal(err)
	}
	r, err = decide(owned, "example.net", 443, false)
	expectAllowed("an open teammate under a locked environment", r, err)
	if _, err := e.Store.SetTeammateSecurityLevel(ctx, env.ID, tm.ID, ""); err != nil {
		t.Fatal(err)
	}
	_, err = decide(owned, "example.net", 443, false)
	expectAsked("a teammate back on the environment's level", err)

	// A browser's tracker is denied under every level that would allow the host.
	setLevel(store.LevelOpen)
	_, err = e.Decide(ctx, Request{EnvironmentID: env.ID, SandboxID: sb.ID, Host: "google-analytics.com", Port: 443, Browser: true, Deferred: true,
		HTTP: &HTTPRequest{Method: "GET", Path: "/collect"}})
	if !errors.Is(err, ErrTracker) {
		t.Errorf("a tracker under open: %v", err)
	}
}
