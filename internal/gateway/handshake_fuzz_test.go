package gateway

import (
	"bufio"
	"bytes"
	"context"
	"net"
	"testing"

	"github.com/lukaskoebe/sandbox-studio/internal/store"
)

// replyConn takes the replies the handshake writes to the client and serves nothing else.
type replyConn struct{ net.Conn }

func (replyConn) Write(p []byte) (int, error) { return len(p), nil }

// socksLogin offers user/password authentication and sends the credentials.
func socksLogin(user, pass string) []byte {
	b := append([]byte{5, 1, 2, 1, byte(len(user))}, user...)
	return append(append(b, byte(len(pass))), pass...)
}

// socksDomain is a CONNECT request to a domain name.
func socksDomain(name string, port uint16) []byte {
	return append(append([]byte{5, 1, 0, 3, byte(len(name))}, name...), byte(port>>8), byte(port))
}

// FuzzSOCKSHandshake feeds the SOCKS5 handshake arbitrary bytes, seeded with greetings, good
// and bad passwords and CONNECT requests for one sandbox, cut short at every length.
func FuzzSOCKSHandshake(f *testing.F) {
	key := []byte("fuzz-key")
	g := &Gateway{Key: key, Sandbox: func(_ context.Context, id string) (store.Sandbox, error) {
		return store.Sandbox{ID: id}, nil
	}}
	pass := Password(key, "sb1")
	full := append(socksLogin("sb1", pass), socksDomain("example.com", 443)...)
	for i := range len(full) {
		f.Add(full[:i])
	}
	for _, in := range [][]byte{
		append(socksLogin("sb1", pass), socksDomain("10.0.0.1", 80)...),
		append(socksLogin("sb1", pass), 5, 1, 0, 1, 1, 2, 3, 4, 0, 80),
		append(socksLogin("sb1", pass), 5, 1, 0, 4, 0x20, 0x01, 0x0d, 0xb8, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 1, 1, 0xbb),
		append(socksLogin("sb1", pass), socksDomain("Example.COM.", 443)...),
		append(socksLogin("sb1", pass), socksDomain(" example.com", 443)...),
		append(socksLogin("sb1", pass), socksDomain("*.example.com", 443)...),
		append(socksLogin("sb1", pass), socksDomain("evil.example/../x", 443)...),
		append(socksLogin("sb1", pass), socksDomain("", 443)...),
		append(socksLogin("sb1", pass), 5, 2, 0, 1, 1, 2, 3, 4, 0, 80),
		append(socksLogin("sb1", pass), 5, 1, 0, 9, 1, 2, 3, 4, 0, 80),
		socksLogin("sb1", "wrong-password"),
		socksLogin("sb2", pass),
		{5, 0},
		{4, 1, 0},
	} {
		f.Add(in)
	}
	f.Fuzz(func(t *testing.T, in []byte) {
		sb, target, err := g.handshake(replyConn{}, bufio.NewReader(bytes.NewReader(in)))
		if err != nil {
			return
		}
		if sb.ID == "" {
			t.Error("connected without a sandbox")
		}
		if (target.domain != "") == target.addr.IsValid() {
			t.Errorf("target has domain %q and address %v", target.domain, target.addr)
		}
		if target.domain != "" && !isHostName(target.domain) {
			t.Errorf("domain %q is not a host name", target.domain)
		}
		_ = target.String()
	})
}
