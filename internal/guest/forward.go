//go:build linux

package guest

import (
	"bufio"
	"errors"
	"io"
	"net"
	"slices"
	"strconv"
	"sync"
	"time"

	"github.com/hashicorp/yamux"

	"github.com/lukaskoebe/sandbox-studio/internal/agentproto"
)

// forwarder holds a browser VM's forwarded loopback ports: a listener on 127.0.0.1 and one
// on ::1 for each, whose connections go to the same port in the sandbox that drives the
// browser. Studio decides the ports; the guest only listens and relays.
type forwarder struct {
	mu    sync.Mutex
	ports map[int][]net.Listener
	// open opens a stream to the host; listen listens on a loopback address.
	open   func() (net.Conn, error)
	listen func(network, addr string) (net.Listener, error)
}

// set listens on exactly ports, closing the listeners of the others. A port another program
// in the VM already holds, or a reserved one, is skipped.
func (f *forwarder) set(ports []int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.ports == nil {
		f.ports = map[int][]net.Listener{}
	}
	for p, lns := range f.ports {
		if !slices.Contains(ports, p) {
			for _, ln := range lns {
				ln.Close()
			}
			delete(f.ports, p)
		}
	}
	for _, p := range ports {
		if _, ok := f.ports[p]; ok || p <= 0 || p > 65535 || agentproto.BrowserReservedPort(p) {
			continue
		}
		var lns []net.Listener
		for _, a := range []struct{ network, host string }{{"tcp4", "127.0.0.1"}, {"tcp6", "::1"}} {
			ln, err := f.listen(a.network, net.JoinHostPort(a.host, strconv.Itoa(p)))
			if err != nil {
				continue
			}
			lns = append(lns, ln)
			go f.serve(ln, p)
		}
		if len(lns) > 0 {
			f.ports[p] = lns
		}
	}
}

func (f *forwarder) serve(ln net.Listener, port int) {
	for {
		c, err := ln.Accept()
		if err != nil {
			if errors.Is(err, net.ErrClosed) {
				return
			}
			time.Sleep(50 * time.Millisecond)
			continue
		}
		go f.relay(c, port)
	}
}

// relay carries one connection to the host as a KindForward stream. If the host refuses,
// the connection closes, which the browser shows like a port nothing listens on.
func (f *forwarder) relay(c net.Conn, port int) {
	defer c.Close()
	st, err := f.open()
	if err != nil {
		return
	}
	defer st.Close()
	st.SetDeadline(time.Now().Add(15 * time.Second))
	if err := agentproto.WriteJSONLine(st, agentproto.Header{Kind: agentproto.KindForward, Port: port}); err != nil {
		return
	}
	br := bufio.NewReader(st)
	var e agentproto.Error
	if err := agentproto.ReadJSONLineLimit(br, &e, 4096); err != nil || e.Error != "" {
		return
	}
	st.SetDeadline(time.Time{})
	done := make(chan struct{})
	go func() {
		_, _ = br.WriteTo(c)
		if tc, ok := c.(*net.TCPConn); ok {
			_ = tc.CloseWrite()
		}
		close(done)
	}()
	_, _ = io.Copy(st, c)
	st.Close() // a yamux Close ends only our side
	<-done
}

// forwards answers a KindForwards stream.
func (a *Agent) forwards(st net.Conn, br *bufio.Reader) error {
	var req agentproto.Forwards
	if err := agentproto.ReadJSONLineLimit(br, &req, 64<<10); err != nil {
		return err
	}
	if len(req.Ports) > agentproto.MaxForwards {
		return agentproto.WriteJSONLine(st, agentproto.Error{Error: "too many ports"})
	}
	a.fwdOnce.Do(func() {
		a.fwd = &forwarder{
			open: func() (net.Conn, error) {
				s := a.session()
				if s == nil {
					return nil, yamux.ErrSessionShutdown
				}
				return s.Open()
			},
			listen: net.Listen,
		}
	})
	a.fwd.set(req.Ports)
	return agentproto.WriteJSONLine(st, struct{}{})
}
