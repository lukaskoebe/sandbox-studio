//go:build linux

package guest

import (
	"bufio"
	"io"
	"net"
	"strconv"
	"testing"
	"time"

	"github.com/lukaskoebe/sandbox-studio/internal/agentproto"
)

// freePort returns a loopback port nothing listens on.
func freePort(t *testing.T) int {
	t.Helper()
	ln, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	return ln.Addr().(*net.TCPAddr).Port
}

func TestForwarderRelaysToTheHost(t *testing.T) {
	ports := make(chan int, 4)
	f := &forwarder{listen: net.Listen, open: func() (net.Conn, error) {
		guest, host := net.Pipe()
		go func() {
			defer host.Close()
			br := bufio.NewReader(host)
			var h agentproto.Header
			if agentproto.ReadJSONLine(br, &h) != nil || h.Kind != agentproto.KindForward {
				return
			}
			ports <- h.Port
			_ = agentproto.WriteJSONLine(host, struct{}{})
			line, _ := br.ReadString('\n')
			_, _ = io.WriteString(host, "echo "+line)
		}()
		return guest, nil
	}}
	p := freePort(t)
	f.set([]int{p, agentproto.BrowserStreamPort})
	t.Cleanup(func() { f.set(nil) })
	if _, ok := f.ports[agentproto.BrowserStreamPort]; ok {
		t.Fatal("a reserved port was forwarded")
	}
	c, err := net.DialTimeout("tcp", net.JoinHostPort("127.0.0.1", strconv.Itoa(p)), 2*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	c.SetDeadline(time.Now().Add(5 * time.Second))
	if _, err := io.WriteString(c, "hi\n"); err != nil {
		t.Fatal(err)
	}
	got, err := io.ReadAll(c)
	if err != nil || string(got) != "echo hi\n" {
		t.Fatalf("relayed %q, %v", got, err)
	}
	if got := <-ports; got != p {
		t.Fatalf("the host was asked for port %d, not %d", got, p)
	}

	// A port no longer named stops listening.
	f.set(nil)
	if c, err := net.DialTimeout("tcp", net.JoinHostPort("127.0.0.1", strconv.Itoa(p)), time.Second); err == nil {
		c.Close()
		t.Fatal("the dropped port still listens")
	}
}

func TestForwarderSkipsAPortTheVMHolds(t *testing.T) {
	ln, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	p := ln.Addr().(*net.TCPAddr).Port
	f := &forwarder{listen: net.Listen, open: func() (net.Conn, error) { return nil, net.ErrClosed }}
	f.set([]int{p})
	t.Cleanup(func() { f.set(nil) })
	// The IPv6 listener may still come up; the VM's own IPv4 one stays the VM's.
	for _, l := range f.ports[p] {
		if l.Addr().(*net.TCPAddr).IP.To4() != nil {
			t.Fatal("the forward took the IPv4 port the VM listens on")
		}
	}
}
