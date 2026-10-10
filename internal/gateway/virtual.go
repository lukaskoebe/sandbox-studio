package gateway

import (
	"bufio"
	"crypto/tls"
	"fmt"
	"net"
	"net/http"
	"time"

	"github.com/lukaskoebe/sandbox-studio/internal/dnsproxy"
	"github.com/lukaskoebe/sandbox-studio/internal/store"
)

// A VirtualHost is a service Studio itself offers sandboxes under dnsproxy.VirtualDomain,
// such as the virtual git remote. The gateway terminates TLS for it with the
// environment's CA and serves it in process: no rule applies and nothing is dialed.
type VirtualHost struct {
	Name string // e.g. git.studio.internal
	// Serve returns the handler for one sandbox's connection. The sandbox is the one the
	// gateway authenticated; handlers must not trust anything else about the caller.
	Serve func(sb store.Sandbox) http.Handler
}

// serveVirtual answers a connection to a name under dnsproxy.VirtualDomain.
func (g *Gateway) serveVirtual(c net.Conn, br *bufio.Reader, entry *Conn, sb store.Sandbox, host, proto string) (verdict string, sent, received int64, problem string) {
	var vh *VirtualHost
	for i := range g.Virtual {
		if g.Virtual[i].Name == host {
			vh = &g.Virtual[i]
		}
	}
	switch {
	case vh == nil:
		g.refuse(c, br, sb.EnvironmentID, host, proto, fmt.Sprintf("Sandbox Studio offers no service at %s.", host))
		return VerdictDenied, 0, 0, "no such Studio service"
	case proto != "tls":
		g.refuse(c, br, sb.EnvironmentID, host, proto, fmt.Sprintf("Use https://%s/.", host))
		return VerdictDenied, 0, 0, "Studio services only speak HTTPS"
	case g.CA == nil:
		return VerdictFailed, 0, 0, "no CA to serve Studio services with"
	}
	g.Conns.opened(entry, "", true)
	cc := &countingConn{Conn: c, r: br}
	tc := tls.Server(cc, g.serverTLS(sb.EnvironmentID, host))
	tc.SetDeadline(time.Now().Add(15 * time.Second))
	if err := tc.Handshake(); err != nil {
		return VerdictAllowed, cc.sent.Load(), cc.received.Load(), handshakeProblem(err)
	}
	tc.SetDeadline(time.Time{})
	h := vh.Serve(sb)
	serveHTTP(tc, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if hostOnly(r.Host) != host {
			writeRefusal(w, http.StatusMisdirectedRequest, fmt.Sprintf("This connection only serves %s.", host))
			return
		}
		h.ServeHTTP(w, r)
	}), g.Log, time.Minute)
	return VerdictAllowed, cc.sent.Load(), cc.received.Load(), ""
}

func isVirtual(host string) bool { return dnsproxy.IsVirtual(host) }
