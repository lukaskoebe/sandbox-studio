package gateway

import (
	"context"
	"fmt"
	"net/netip"
	"slices"
	"strconv"
	"strings"

	"github.com/lukaskoebe/sandbox-studio/internal/dnsproxy"
	"github.com/lukaskoebe/sandbox-studio/internal/policy"
	"github.com/lukaskoebe/sandbox-studio/internal/store"
)

// Unreachable says why no sandbox can make a connection that rule r allows, or "" when one can
// (or the gateway can't tell: a name may resolve anywhere). A rule for an address microsandbox or
// the dial guard refuses is accepted, but it never applies, which is easy to miss: the connection
// is refused before the policy is asked, so the rule never gets a hit. Deny rules are never
// unreachable in this sense.
func (g *Gateway) Unreachable(ctx context.Context, r store.Rule) string {
	if r.Action == store.ActionDeny {
		return ""
	}
	host := policy.Normalize(r.Host)
	const toHost = "Sandboxes reach this computer as host.studio.internal, on the ports open to them in Settings → Ports and LAN."
	switch {
	case host == "localhost" || strings.HasSuffix(host, ".localhost"):
		return "Connections to localhost stay inside the sandbox, so this rule never applies. " + toHost
	case isHost(host):
		return g.closedHostPorts(ctx, r)
	case strings.HasSuffix(host, "."+dnsproxy.LANDomain):
		if ip, ok := dnsproxy.LANAddr(host); ok {
			return fmt.Sprintf("Connections to %s are decided by the address it stands for: make the rule for %s instead.", host, ip)
		}
		return ""
	}
	ip, err := netip.ParseAddr(host)
	if err != nil {
		return ""
	}
	ip = ip.Unmap()
	switch {
	case policy.Public(ip):
		return ""
	case ip.IsLoopback():
		return "Connections to loopback addresses stay inside the sandbox, so this rule never applies. " + toHost
	case !slices.ContainsFunc(privateRanges, func(p netip.Prefix) bool { return p.Contains(ip) }):
		return fmt.Sprintf("Sandboxes never reach %s: link-local, multicast, reserved and Studio's own addresses stay refused.", ip)
	case hostAddr(ip):
		return fmt.Sprintf("%s is an address of this computer, which sandboxes don't reach by its address. %s Make the rule for host.studio.internal instead.", ip, toHost)
	case !ip.Is4():
		return fmt.Sprintf("Sandboxes don't reach %s: only IPv4 addresses can be on the local network list. List the host's name in Settings → Ports and LAN instead.", ip)
	}
	var list []store.PrivateHost
	if g.PrivateHosts != nil {
		if list, err = g.PrivateHosts(ctx, r.EnvironmentID); err != nil {
			return ""
		}
	}
	addr := ip.String()
	if len(r.Ports) == 0 {
		if slices.ContainsFunc(list, func(h store.PrivateHost) bool { return entryCovers(h, addr) }) {
			return ""
		}
		return fmt.Sprintf("Sandboxes reach %s only once it is on the local network list in Settings → Ports and LAN.", addr)
	}
	var missing []string
	for _, p := range r.Ports {
		if !listed(list, addr, p) {
			missing = append(missing, strconv.Itoa(p))
		}
	}
	if len(missing) == 0 {
		return ""
	}
	return fmt.Sprintf("Sandboxes reach %s on port %s only once it is on the local network list with that port, in Settings → Ports and LAN.", addr, strings.Join(missing, ", "))
}

// closedHostPorts says which of the ports rule r allows on this computer are not open to
// sandboxes, or "" when they all are.
func (g *Gateway) closedHostPorts(ctx context.Context, r store.Rule) string {
	var open []int
	if g.HostPorts != nil {
		var err error
		if open, err = g.HostPorts(ctx, r.EnvironmentID); err != nil {
			return ""
		}
	}
	if len(r.Ports) == 0 {
		if len(open) == 0 {
			return "No port of this computer is open to sandboxes, so this rule never applies. Open the ports it is for in Settings → Ports and LAN."
		}
		return ""
	}
	var closed []string
	for _, p := range r.Ports {
		if !slices.Contains(open, p) || slices.Contains(g.reservedPorts(), uint16(p)) {
			closed = append(closed, strconv.Itoa(p))
		}
	}
	switch len(closed) {
	case 0:
		return ""
	case 1:
		return fmt.Sprintf("Port %s of this computer is not open to sandboxes: open it in Settings → Ports and LAN.", closed[0])
	}
	return fmt.Sprintf("Ports %s of this computer are not open to sandboxes: open them in Settings → Ports and LAN.", strings.Join(closed, ", "))
}
