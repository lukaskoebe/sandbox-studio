package gateway

import (
	"context"
	"time"

	"github.com/lukaskoebe/sandbox-studio/internal/dnsproxy"
	"github.com/lukaskoebe/sandbox-studio/internal/policy"
	"github.com/lukaskoebe/sandbox-studio/internal/store"
)

// dnsHooks shape how a sandbox's resolver answers. A name that a deny rule refuses gets NXDOMAIN,
// so tools fail at once instead of waiting on a held connection. A name on the environment's
// private hosts list gets virtual addresses, which the gateway maps back to the name: microsandbox
// drops private answers before they reach the sandbox, so the sandbox can't use the real ones.
// A lookup reaches the host's resolvers only for a name the sandbox may already connect to. Any
// other name would reach whoever runs its domain before anyone had a say, carrying what the
// sandbox put in it, so it stays here and gets a virtual address: a connection to that is held and
// asked about by name, like any other.
func (g *Gateway) dnsHooks(sb store.Sandbox) dnsproxy.Hooks {
	return dnsproxy.Hooks{
		Deny:       func(name string) bool { return g.refuseLookup(sb, name) },
		Virtualize: func(name string) bool { return g.privateName(sb.EnvironmentID, name) },
		Withhold:   func(name string) bool { return !g.resolvable(sb, name) },
		Withheld:   func(name, kind string) { g.logWithheld(sb, name, kind) },
	}
}

// resolvable reports whether sb's lookups of name may leave Studio. If that can't be told, they
// don't.
func (g *Gateway) resolvable(sb store.Sandbox, name string) bool {
	decider := g.decider(sb)
	ok, err := g.Policy.Resolvable(context.Background(), policy.Request{
		EnvironmentID: decider.EnvironmentID, SandboxID: decider.ID, Host: name,
		Browser: sb.Kind == store.SandboxKindBrowser,
		Allowed: policy.SystemHosts(sb.Kind), Preapproved: policy.BuildHosts(sb),
	})
	if err != nil {
		g.Log.Error("network policy", "sandbox", sb.ID, "host", name, "err", err)
		return false
	}
	return ok
}

// logWithheld notes in the sandbox's connection log, once a minute per name, a lookup that got an
// empty answer and that no connection follows from, which would otherwise look like a name with
// no such records.
func (g *Gateway) logWithheld(sb store.Sandbox, name, kind string) {
	if g.Conns == nil || !validHost(name) || !g.logRefusal(sb.ID, name) {
		return
	}
	entry := g.Conns.add(sb.ID, Conn{Host: name, Address: name, Source: "dns"})
	g.Conns.finish(entry, VerdictDenied, "", "this "+kind+" lookup wasn't sent, as no rule allows the name yet", 0, 0)
}

// refuseLookup reports whether a deny rule refuses sb's connections to name on every port. The
// refusal goes in the sandbox's connection log, once a minute per name.
func (g *Gateway) refuseLookup(sb store.Sandbox, name string) bool {
	ctx := context.Background()
	decider := g.decider(sb)
	rule, refused, err := g.Policy.Refused(ctx, decider.EnvironmentID, decider.ID, name)
	if err != nil || !refused {
		return false
	}
	if g.logRefusal(sb.ID, name) && g.Conns != nil {
		entry := g.Conns.add(sb.ID, Conn{Host: name, Address: name, Source: "dns"})
		g.Conns.finish(entry, VerdictDenied, rule.ID, "the name is refused by a deny rule, so its lookup failed", 0, 0)
	}
	return true
}

// logRefusal reports whether a refused lookup of name by sandboxID should be logged: not if one
// was logged within the last minute.
func (g *Gateway) logRefusal(sandboxID, name string) bool {
	g.refusedMu.Lock()
	defer g.refusedMu.Unlock()
	now := time.Now()
	key := sandboxID + " " + name
	if last, ok := g.refusedLog[key]; ok && now.Sub(last) < time.Minute {
		return false
	}
	if g.refusedLog == nil || len(g.refusedLog) >= 4096 {
		g.refusedLog = map[string]time.Time{}
	}
	g.refusedLog[key] = now
	return true
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
