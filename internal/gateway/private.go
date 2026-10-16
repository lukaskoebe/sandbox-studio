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

// The local network (the private hosts list, internally). Upstream dials refuse non-public
// addresses, so a company host that resolves to a private address (git.company.local behind a
// VPN, say) or a NAS on the LAN is unreachable. An environment's local network list is the one
// opt-in to that: a connection to a listed name may also reach the private addresses that name
// resolves to, and a listed private address or range may be reached itself. Nothing else
// changes: the policy still has to allow the host, and a name that isn't listed is refused
// private addresses as before.
//
// The gateway resolves a listed name itself, when it dials, through the host's resolver (the
// dialer's Resolver, which is net.DefaultResolver: /etc/resolv.conf and nsswitch.conf, or
// getaddrinfo with cgo, so a VPN's split DNS applies). The addresses the sandbox saw in its own
// DNS answers are never dialed: the sandbox's lookups of a listed name get virtual addresses
// instead (dns.go), which the gateway maps back to the name. Every address the dialer connects
// to is checked against the name it was dialed for. A listed address is reached by its name
// under dnsproxy.LANDomain, since microsandbox refuses private addresses. Loopback, link-local
// (the cloud metadata address among them), unspecified and multicast addresses, Studio's virtual
// services (198.18.0.0/15), the host's own private addresses and Studio's own listening ports
// stay refused for every entry. The host itself is reached as dnsproxy.HostName, on the ports
// the environment opened (host.go).

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

// ValidPrivateHost checks an entry of a local network list and returns it normalized. The host
// is a name, a wildcard over a domain of its own (a public suffix such as *.com must not make
// every name under it reachable on private addresses), or a private IPv4 address or range.
func ValidPrivateHost(h store.PrivateHost) (store.PrivateHost, error) {
	pattern, err := validPrivatePattern(h.Host)
	if err != nil {
		return h, err
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

// validPrivatePattern checks and normalizes the host of a local network entry.
func validPrivatePattern(host string) (string, error) {
	host = strings.TrimSpace(host)
	if prefix, err := netip.ParsePrefix(host); err == nil {
		prefix = prefix.Masked()
		if !prefix.Addr().Is4() {
			return "", fmt.Errorf("%s: only IPv4 ranges can be listed", host)
		}
		if !slices.ContainsFunc(privateRanges, func(p netip.Prefix) bool { return p.Overlaps(prefix) && p.Bits() <= prefix.Bits() }) {
			return "", fmt.Errorf("%s is not a private range: list a part of 10.0.0.0/8, 172.16.0.0/12, 192.168.0.0/16 or 100.64.0.0/10", host)
		}
		if prefix.IsSingleIP() {
			return prefix.Addr().String(), nil
		}
		return prefix.String(), nil
	}
	pattern, err := policy.ValidPattern(host)
	if err != nil {
		return "", err
	}
	if pattern == "*" {
		return "", errors.New("list a name, a wildcard such as *.company.local, or a private address, not *")
	}
	if ip, err := netip.ParseAddr(pattern); err == nil {
		ip = ip.Unmap()
		switch {
		case !ip.Is4():
			return "", fmt.Errorf("%s: only IPv4 addresses can be listed; list the host's name instead", pattern)
		case !slices.ContainsFunc(privateRanges, func(p netip.Prefix) bool { return p.Contains(ip) }):
			return "", fmt.Errorf("%s is not a private address: sandboxes reach public addresses without being listed, and never loopback or link-local ones", pattern)
		}
		return ip.String(), nil
	}
	return pattern, nil
}

// entryAddrs returns the addresses a local network entry lists, if it lists addresses.
func entryAddrs(h store.PrivateHost) (netip.Prefix, bool) {
	if prefix, err := netip.ParsePrefix(h.Host); err == nil {
		return prefix, true
	}
	if ip, err := netip.ParseAddr(h.Host); err == nil {
		return netip.PrefixFrom(ip, ip.BitLen()), true
	}
	return netip.Prefix{}, false
}

// entryCovers reports whether a local network entry covers host: a name its pattern covers, or an
// address in its range.
func entryCovers(h store.PrivateHost, host string) bool {
	prefix, isAddrs := entryAddrs(h)
	ip, err := netip.ParseAddr(host)
	switch {
	case isAddrs:
		return err == nil && prefix.Contains(ip.Unmap())
	case err == nil:
		return false
	}
	return policy.Covers(h.Host, host)
}

// listedName reports whether the local network list covers the name host on some port.
func listedName(list []store.PrivateHost, host string) bool {
	host = policy.Normalize(host)
	if net.ParseIP(host) != nil {
		return false
	}
	return slices.ContainsFunc(list, func(h store.PrivateHost) bool { return entryCovers(h, host) })
}

// listed reports whether the local network list covers a connection to host:port. An address is
// covered only by an entry that lists it, never by a name that resolves to it.
func listed(list []store.PrivateHost, host string, port int) bool {
	host = policy.Normalize(host)
	for _, h := range list {
		ports := h.Ports
		if len(ports) == 0 {
			ports = defaultPrivatePorts
		}
		if slices.Contains(ports, port) && entryCovers(h, host) {
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
		if !public(ap.Addr()) && hostAddr(ap.Addr()) {
			return fmt.Errorf("refusing to connect to %s: an address of this computer", ap.Addr())
		}
		return nil
	}
}

// interfaceAddrs lists the host's own addresses; tests replace it.
var interfaceAddrs = net.InterfaceAddrs

// hostAddr reports whether ip is one of the host's own addresses, whose services (a dev server
// on 0.0.0.0, say) a private host must not reach. It fails closed.
func hostAddr(ip netip.Addr) bool {
	addrs, err := interfaceAddrs()
	if err != nil {
		return true
	}
	ip = ip.Unmap()
	for _, a := range addrs {
		if n, ok := a.(*net.IPNet); ok {
			if own, ok := netip.AddrFromSlice(n.IP); ok && own.Unmap() == ip {
				return true
			}
		}
	}
	return false
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

// guardedDialer dials upstream connections of environment envID. dnsproxy.HostName reaches the
// ports of this computer the environment opened (host.go). A name on the environment's local
// network list may reach its private addresses, and a listed address may be reached. Any other name is dialed by fallback, or
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
		if isHost(policy.Normalize(host)) {
			return g.dialHost(ctx, envID, network, port)
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
