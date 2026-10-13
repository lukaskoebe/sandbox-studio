package policy

import (
	"context"
	"errors"
	"net"
	"strings"
	"testing"
	"time"

	"github.com/lukaskoebe/sandbox-studio/internal/store"
)

// The shipped list is well formed: every entry names hosts and ports, each host is listed once,
// and the ports are the web ones.
func TestCuratedListIsWellFormed(t *testing.T) {
	version, entries := CuratedList()
	if version == "" || len(entries) < 20 {
		t.Fatalf("list %q with %d entries", version, len(entries))
	}
	ids, hosts := map[string]bool{}, map[string]bool{}
	for _, e := range entries {
		if e.ID == "" || e.Name == "" || e.Group == "" || ids[e.ID] {
			t.Errorf("entry %+v: an ID, name and group are needed, and each ID once", e)
		}
		ids[e.ID] = true
		if len(e.Hosts) == 0 || len(e.Ports) == 0 {
			t.Errorf("%s: hosts and ports are needed", e.ID)
		}
		for _, p := range e.Ports {
			if p != 80 && p != 443 {
				t.Errorf("%s: port %d; the curated list only has web ports", e.ID, p)
			}
		}
		for _, h := range e.Hosts {
			if h != Normalize(h) || strings.Contains(h, "*") || net.ParseIP(h) != nil {
				t.Errorf("%s: host %q must be a lowercase name without wildcards", e.ID, h)
			}
			if _, err := ValidPattern(h); err != nil {
				t.Errorf("%s: host %q: %v", e.ID, h, err)
			}
			if hosts[h] {
				t.Errorf("%s: host %q is listed twice", e.ID, h)
			}
			hosts[h] = true
		}
	}
}

func TestCuratedMatch(t *testing.T) {
	for _, c := range []struct {
		host string
		port int
		off  []string
		want string
	}{
		{"registry.npmjs.org", 443, nil, "npm"},
		{"Registry.NPMJS.org.", 443, nil, "npm"},  // names are normalized
		{"registry.npmjs.org", 80, nil, ""},       // the entry is https only
		{"deb.debian.org", 80, nil, "debian"},     // debian's source needs plain http
		{"evil.registry.npmjs.org", 443, nil, ""}, // exact names, not domains
		{"registry.npmjs.org", 443, []string{"npm"}, ""},
		{"registry.npmjs.org", 443, []string{"pypi"}, "npm"},
		{"example.org", 443, nil, ""},
	} {
		e, ok := curatedMatch(c.host, c.port, c.off)
		if got := map[bool]string{true: e.ID}[ok]; got != c.want {
			t.Errorf("%s:%d off=%v: got %q, want %q", c.host, c.port, c.off, got, c.want)
		}
	}
}

// Guarded and assisted allow the curated list and ask for the rest. The environment's disabled
// entries and a placeholder stop an allow, and an allow shows its entry in Activity.
func TestCuratedLevels(t *testing.T) {
	ctx := context.Background()
	e, env, sb := newEngine(t)
	e.Hold = 30 * time.Millisecond
	setLevel := func(level string) {
		t.Helper()
		if err := e.Store.SetSecurityLevel(ctx, env.ID, level); err != nil {
			t.Fatal(err)
		}
	}
	decide := func(host string, placeholder bool) (store.Rule, error) {
		t.Helper()
		return e.Decide(ctx, Request{EnvironmentID: env.ID, SandboxID: sb.ID, SandboxName: sb.Name, Host: host, Port: 443, Placeholder: placeholder})
	}

	setLevel(store.LevelGuarded)
	r, err := decide("registry.npmjs.org", false)
	if err != nil || r.Action != store.ActionAllow || r.ID != "" {
		t.Fatalf("guarded, curated: %+v, %v", r, err)
	}
	logged, _ := e.Store.Approvals(ctx, env.ID, store.StatusApproved, 10)
	if len(logged) != 1 || !strings.Contains(string(logged[0].Payload), `"decided":"curated"`) || !strings.Contains(string(logged[0].Payload), `"reason":"npm registry"`) {
		t.Fatalf("the curated allow in Activity: %+v", logged)
	}
	if _, err := decide("example.org", false); !errors.Is(err, ErrUndecided) {
		t.Errorf("guarded, not curated: %v; want the user asked", err)
	}

	// The environment turns the entry off: the host is asked again.
	if err := e.Store.SetCuratedOff(ctx, env.ID, []string{"npm"}); err != nil {
		t.Fatal(err)
	}
	if _, err := decide("registry.npmjs.org", false); !errors.Is(err, ErrUndecided) {
		t.Errorf("a disabled entry: %v; want the user asked", err)
	}
	if err := e.Store.SetCuratedOff(ctx, env.ID, nil); err != nil {
		t.Fatal(err)
	}

	// A placeholder is never allowed by the list.
	if _, err := decide("registry.npmjs.org", true); !errors.Is(err, ErrUndecided) {
		t.Errorf("a placeholder to a curated host: %v; want the user asked", err)
	}

	// Assisted allows the list too; locked doesn't.
	setLevel(store.LevelAssisted)
	if r, err := decide("pypi.org", false); err != nil || r.Action != store.ActionAllow {
		t.Errorf("assisted, curated: %+v, %v", r, err)
	}
	setLevel(store.LevelLocked)
	if _, err := decide("pypi.org", false); !errors.Is(err, ErrUndecided) {
		t.Errorf("locked, curated: %v; want the user asked", err)
	}
}
