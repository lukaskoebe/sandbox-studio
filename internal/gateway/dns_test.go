package gateway

import (
	"context"
	"errors"
	"testing"

	"github.com/lukaskoebe/sandbox-studio/internal/store"
)

func TestDNSHooksVirtualizePrivateHosts(t *testing.T) {
	g := &Gateway{
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
