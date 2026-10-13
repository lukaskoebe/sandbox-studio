package policy

import "net/netip"

// Public reports whether ip is on the public Internet, the only addresses a sandbox's connections
// reach (docs/network.md). Private, loopback, link-local, carrier-grade NAT and documentation
// addresses aren't.
func Public(ip netip.Addr) bool {
	ip = ip.Unmap()
	if !ip.IsGlobalUnicast() || ip.IsPrivate() {
		return false
	}
	// IsGlobalUnicast also accepts reserved IPv6 space. Restrict native IPv6 to
	// the allocated Internet range; translation prefixes could reach private IPv4.
	// Special-use ranges: https://www.iana.org/assignments/iana-ipv6-special-registry
	if ip.Is6() && !ipv6Internet.Contains(ip) {
		return false
	}
	for _, p := range nonPublic {
		if p.Contains(ip) {
			return false
		}
	}
	return true
}

var nonPublic = []netip.Prefix{
	netip.MustParsePrefix("0.0.0.0/8"),
	netip.MustParsePrefix("100.64.0.0/10"), // carrier-grade NAT, also Tailscale
	netip.MustParsePrefix("192.0.0.0/24"),
	netip.MustParsePrefix("192.0.2.0/24"),    // documentation
	netip.MustParsePrefix("192.88.99.0/24"),  // deprecated 6to4 relay
	netip.MustParsePrefix("198.18.0.0/15"),   // benchmarking; Studio's virtual host services
	netip.MustParsePrefix("198.51.100.0/24"), // documentation
	netip.MustParsePrefix("203.0.113.0/24"),  // documentation
	netip.MustParsePrefix("240.0.0.0/4"),
	netip.MustParsePrefix("64:ff9b:1::/48"),
	netip.MustParsePrefix("2001:db8::/32"),
	netip.MustParsePrefix("2001::/23"), // special protocol assignments, including Teredo
	netip.MustParsePrefix("2002::/16"), // 6to4 can encapsulate non-public IPv4
	netip.MustParsePrefix("3fff::/20"), // documentation
}

var ipv6Internet = netip.MustParsePrefix("2000::/3")
