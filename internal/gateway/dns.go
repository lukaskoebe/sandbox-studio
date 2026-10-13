package gateway

import (
	"context"

	"github.com/lukaskoebe/sandbox-studio/internal/dnsproxy"
	"github.com/lukaskoebe/sandbox-studio/internal/store"
)

// dnsHooks shape how a sandbox's resolver answers. A name on the environment's private hosts
// list gets virtual addresses, which the gateway maps back to the name: microsandbox drops
// private answers before they reach the sandbox, so the sandbox can't use the real ones.
func (g *Gateway) dnsHooks(sb store.Sandbox) dnsproxy.Hooks {
	return dnsproxy.Hooks{Virtualize: func(name string) bool { return g.privateName(sb.EnvironmentID, name) }}
}

// privateName reports whether name is on the environment's private hosts list, for any port.
// Its lookups are answered with virtual addresses; the connections are still checked per port.
func (g *Gateway) privateName(envID, name string) bool {
	if g.PrivateHosts == nil {
		return false
	}
	list, err := g.PrivateHosts(context.Background(), envID)
	return err == nil && listedName(list, name)
}
