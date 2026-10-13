package gateway

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/netip"
	"slices"
	"sort"
	"strconv"
	"strings"
	"syscall"
	"time"

	"golang.org/x/net/publicsuffix"

	"github.com/lukaskoebe/sandbox-studio/internal/policy"
	"github.com/lukaskoebe/sandbox-studio/internal/store"
)

// Private hosts. Upstream dials refuse non-public addresses, so a company host that resolves
// to a private address (git.company.local behind a VPN, say) is unreachable. An environment's
// private hosts list is the one opt-in to that: a connection to a listed name may also reach
// the private addresses that name resolves to. Nothing else changes: the policy still has to
// allow the host, and a name that isn't listed is refused private addresses as before.
//
// The gateway resolves a listed name itself, when it dials, through the host's resolver (the
// dialer's Resolver, which is net.DefaultResolver: /etc/resolv.conf and nsswitch.conf, or
// getaddrinfo with cgo, so a VPN's split DNS applies). The addresses the sandbox saw in its own
// DNS answers are never dialed: the sandbox's lookups of a listed name get virtual addresses
// instead (dns.go), which the gateway maps back to the name. Every address the dialer connects
// to is checked against the name it was dialed for. Loopback, link-local (the cloud metadata
// address among them), unspecified and multicast addresses, Studio's virtual services
// (198.18.0.0/15), and Studio's own listening ports stay refused for every name.

// privateRanges are the private addresses a listed name may resolve to: RFC 1918, carrier-grade
// NAT (100.64.0.0/10, which VPNs such as Tailscale use) and IPv6 unique local addresses.
var privateRanges = []netip.Prefix{
	netip.MustParsePrefix("10.0.0.0/8"),
	netip.MustParsePrefix("172.16.0.0/12"),
	netip.MustParsePrefix("192.168.0.0/16"),
	netip.MustParsePrefix("100.64.0.0/10"),
	netip.MustParsePrefix("fc00::/7"),
}

// defaultPrivatePorts are the ports of a private host entry that names none.
var defaultPrivatePorts = []int{80, 443}

// ValidPrivateHost checks an entry of a private hosts list and returns it normalized. The host
// is a name, or a wildcard over a domain of its own: a public suffix such as *.com must not make
// every name under it reachable on private addresses.
func ValidPrivateHost(h store.PrivateHost) (store.PrivateHost, error) {
	pattern, err := policy.ValidPattern(h.Host)
	if err != nil {
		return h, err
	}
	switch {
	case pattern == "*":
		return h, errors.New("a private host is a name, or a wildcard such as *.company.local, not *")
	case net.ParseIP(pattern) != nil:
		return h, fmt.Errorf("%s is an IP address; a private host is a name, such as git.company.local", pattern)
	}
	if domain, wild := strings.CutPrefix(pattern, "*."); wild {
		if _, err := publicsuffix.EffectiveTLDPlusOne(domain); err != nil {
			return h, fmt.Errorf("%s is too broad: a wildcard names a domain of its own, such as *.company.local, not a public suffix such as *.com", pattern)
		}
	}
	ports := slices.Clone(h.Ports)
	for _, p := range ports {
		if p < 1 || p > 65535 {
			return h, fmt.Errorf("port %d is not between 1 and 65535", p)
		}
	}
	sort.Ints(ports)
	ports = slices.Compact(ports)
	if ports == nil {
		ports = []int{}
	}
	return store.PrivateHost{Host: pattern, Ports: ports}, nil
}

// listedName reports whether the private hosts list covers host on some port.
func listedName(list []store.PrivateHost, host string) bool {
	host = policy.Normalize(host)
	if net.ParseIP(host) != nil {
		return false
	}
	return slices.ContainsFunc(list, func(h store.PrivateHost) bool { return policy.Covers(h.Host, host) })
}

// listed reports whether the private hosts list covers a connection to host:port. Only names
// can be listed: an IP address is never a private host, so a sandbox can't reach private
// addresses by naming them.
func listed(list []store.PrivateHost, host string, port int) bool {
	host = policy.Normalize(host)
	if net.ParseIP(host) != nil {
		return false
	}
	for _, h := range list {
		ports := h.Ports
		if len(ports) == 0 {
			ports = defaultPrivatePorts
		}
		if slices.Contains(ports, port) && policy.Covers(h.Host, host) {
			return true
		}
	}
	return false
}

// privateAddr reports whether a listed name may connect to ip: any public address, or one of
// privateRanges. Everything else is refused, whatever the name.
func privateAddr(ip netip.Addr) bool {
	ip = ip.Unmap()
	if !ip.IsGlobalUnicast() { // loopback, link-local, multicast and unspecified addresses
		return false
	}
	if slices.ContainsFunc(privateRanges, func(p netip.Prefix) bool { return p.Contains(ip) }) {
		return true
	}
	return public(ip)
}

// checkPublic is the Control of a dialer for names that aren't listed: public addresses only.
func checkPublic(network, address string, _ syscall.RawConn) error {
	ap, err := netip.ParseAddrPort(address)
	if err != nil {
		return err
	}
	if !public(ap.Addr()) {
		return fmt.Errorf("refusing to connect to non-public address %s", ap.Addr())
	}
	return nil
}

// checkPrivate returns the Control of a dialer for a listed name: privateAddr, and never one
// of Studio's own ports.
func checkPrivate(reserved []uint16) func(network, address string, _ syscall.RawConn) error {
	return func(network, address string, _ syscall.RawConn) error {
		ap, err := netip.ParseAddrPort(address)
		if err != nil {
			return err
		}
		if slices.Contains(reserved, ap.Port()) {
			return fmt.Errorf("refusing to connect to %s: Studio's own port", ap)
		}
		if !privateAddr(ap.Addr()) {
			return fmt.Errorf("refusing to connect to %s: not a public or private address", ap.Addr())
		}
		return nil
	}
}

// reservedPorts are the ports of Studio's own listeners, which no private host is dialed on.
func (g *Gateway) reservedPorts() []uint16 {
	var out []uint16
	for _, addr := range append([]string{g.Addr}, g.Own...) {
		if _, port, err := net.SplitHostPort(addr); err == nil {
			if n, err := strconv.ParseUint(port, 10, 16); err == nil {
				out = append(out, uint16(n))
			}
		}
	}
	return out
}

// upstreamDialer returns the dialer of the gateway's upstream connections of environment envID.
// A test's Dial, or the e2e build's, replaces it.
func (g *Gateway) upstreamDialer(envID string) func(ctx context.Context, network, addr string) (net.Conn, error) {
	if g.Dial != nil {
		return g.Dial
	}
	return g.guardedDialer(envID, nil)
}

// guardedDialer dials upstream connections of environment envID. A name on the environment's
// private hosts list may reach its private addresses. Any other name is dialed by fallback, or
// with public addresses only if fallback is nil.
func (g *Gateway) guardedDialer(envID string, fallback func(ctx context.Context, network, addr string) (net.Conn, error)) func(ctx context.Context, network, addr string) (net.Conn, error) {
	return func(ctx context.Context, network, addr string) (net.Conn, error) {
		host, portString, err := net.SplitHostPort(addr)
		if err != nil {
			return nil, err
		}
		port, err := strconv.Atoi(portString)
		if err != nil {
			return nil, err
		}
		var list []store.PrivateHost
		if g.PrivateHosts != nil {
			if list, err = g.PrivateHosts(ctx, envID); err != nil {
				return nil, fmt.Errorf("private hosts: %w", err)
			}
		}
		if !listed(list, host, port) {
			if fallback != nil {
				return fallback(ctx, network, addr)
			}
			d := &net.Dialer{Timeout: 15 * time.Second, Resolver: g.resolver, Control: checkPublic}
			return d.DialContext(ctx, network, addr)
		}
		d := &net.Dialer{Timeout: 15 * time.Second, Resolver: g.resolver, Control: checkPrivate(g.reservedPorts())}
		return d.DialContext(ctx, network, addr)
	}
}
