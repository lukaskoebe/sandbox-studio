package gateway

import (
	"context"
	"net"
	"strings"
	"testing"

	"github.com/lukaskoebe/sandbox-studio/internal/store"
)

func TestUnreachableRules(t *testing.T) {
	saved := interfaceAddrs
	interfaceAddrs = func() ([]net.Addr, error) {
		return []net.Addr{&net.IPNet{IP: net.ParseIP("192.168.178.63"), Mask: net.CIDRMask(24, 32)}}, nil
	}
	t.Cleanup(func() { interfaceAddrs = saved })
	g := &Gateway{
		Addr: "127.0.0.1:7879",
		PrivateHosts: func(context.Context, string) ([]store.PrivateHost, error) {
			return []store.PrivateHost{{Host: "192.168.178.20", Ports: []int{22}}, {Host: "10.1.0.0/16"}}, nil
		},
		HostPorts: func(context.Context, string) ([]int, error) { return []int{22, 7879}, nil },
	}
	cases := []struct {
		host   string
		ports  []int
		action string
		want   string // a fragment of the warning, or "" for none
	}{
		{"github.com", nil, store.ActionAllow, ""},
		{"8.8.8.8", []int{53}, store.ActionAllow, ""},
		{"git.company.local", nil, store.ActionAllow, ""}, // a name may resolve anywhere
		{"192.168.178.63", []int{22}, store.ActionAllow, "an address of this computer"},
		{"192.168.178.63", []int{22}, store.ActionDeny, ""},
		{"localhost", []int{3000}, store.ActionAllow, "stay inside the sandbox"},
		{"127.0.0.1", nil, store.ActionProxy, "stay inside the sandbox"},
		{"169.254.169.254", nil, store.ActionAllow, "never reach"},
		{"fd00::5", nil, store.ActionAllow, "only IPv4"},
		{"192.168.178.20", []int{22}, store.ActionAllow, ""},
		{"192.168.178.20", nil, store.ActionAllow, ""},
		{"192.168.178.20", []int{22, 443}, store.ActionAllow, "on port 443 only once"},
		{"192.168.178.21", []int{22}, store.ActionAllow, "only once it is on the local network list"},
		{"10.1.2.3", []int{443}, store.ActionAllow, ""},
		{"192-168-178-20.lan.studio.internal", []int{22}, store.ActionAllow, "make the rule for 192.168.178.20"},
		{"host.studio.internal", []int{22}, store.ActionAllow, ""},
		{"host.studio.internal", nil, store.ActionAllow, ""},
		{"host.studio.internal", []int{22, 5432}, store.ActionAllow, "Port 5432 of this computer is not open"},
		{"host.studio.internal", []int{7879}, store.ActionAllow, "Port 7879"}, // Studio's own, listed or not
	}
	for _, c := range cases {
		got := g.Unreachable(context.Background(), store.Rule{EnvironmentID: "env", Host: c.host, Ports: c.ports, Action: c.action})
		if (c.want == "") != (got == "") || !strings.Contains(got, c.want) {
			t.Errorf("%s %v %s: %q, want %q", c.host, c.ports, c.action, got, c.want)
		}
	}
	closed := &Gateway{}
	if got := closed.Unreachable(context.Background(), store.Rule{Host: "host.studio.internal", Action: store.ActionAllow}); !strings.Contains(got, "No port") {
		t.Errorf("no open ports: %q", got)
	}
}
