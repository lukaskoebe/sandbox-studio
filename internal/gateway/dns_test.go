package gateway

import (
	"context"
	"errors"
	"log/slog"
	"testing"

	"github.com/lukaskoebe/sandbox-studio/internal/store"
)

func TestDNSHooksRefuseAndVirtualize(t *testing.T) {
	pol := &fakePolicy{refused: map[string]store.Rule{"evil.example": {ID: "deny-evil", Action: store.ActionDeny}}}
	conns := &ConnLog{}
	g := &Gateway{
		Policy: pol,
		Conns:  conns,
		Log:    slog.New(slog.DiscardHandler),
		PrivateHosts: func(_ context.Context, envID string) ([]store.PrivateHost, error) {
			switch envID {
			case "env":
				return []store.PrivateHost{{Host: "git.company.local"}}, nil
			case "broken":
				return nil, errors.New("database is closed")
			}
			return nil, nil
		},
	}
	hooks := g.dnsHooks(store.Sandbox{ID: "sb1", EnvironmentID: "env"})

	if !hooks.Deny("evil.example") {
		t.Fatal("a name a deny rule refuses is not refused")
	}
	if hooks.Deny("unknown.example") {
		t.Fatal("a name no rule refuses is refused")
	}
	// A refusal is logged once a minute per name, and says which rule refused it.
	hooks.Deny("evil.example")
	logged := conns.List("sb1")
	if len(logged) != 1 || logged[0].Verdict != VerdictDenied || logged[0].Host != "evil.example" || logged[0].RuleID != "deny-evil" {
		t.Fatalf("connection log: %+v", logged)
	}

	if !hooks.Virtualize("git.company.local") {
		t.Error("a private host's lookups are not virtualized")
	}
	if hooks.Virtualize("other.company.local") {
		t.Error("a name that isn't a private host is virtualized")
	}
	if g.dnsHooks(store.Sandbox{ID: "sb2", EnvironmentID: "other"}).Virtualize("git.company.local") {
		t.Error("another environment's private hosts apply")
	}
	if g.dnsHooks(store.Sandbox{ID: "sb3", EnvironmentID: "broken"}).Virtualize("git.company.local") {
		t.Error("a failed read of the private hosts virtualizes")
	}
}
