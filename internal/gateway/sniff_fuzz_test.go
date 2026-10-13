package gateway

import (
	"bufio"
	"bytes"
	"io"
	"net"
	"strings"
	"testing"
	"time"
)

// sniffConn serves in and nothing else: sniff only reads from a connection and sets its
// read deadline.
type sniffConn struct {
	net.Conn // never used by sniff
	r        *bytes.Reader
}

func (c sniffConn) Read(p []byte) (int, error)      { return c.r.Read(p) }
func (c sniffConn) SetReadDeadline(time.Time) error { return nil }

// FuzzSniffTLS feeds sniff arbitrary bytes, seeded with ClientHellos.
func FuzzSniffTLS(f *testing.F) {
	for _, seed := range tlsSeeds(f) {
		f.Add(seed)
	}
	f.Fuzz(checkSniff)
}

// FuzzSniffHTTP feeds sniff arbitrary bytes, seeded with request heads.
func FuzzSniffHTTP(f *testing.F) {
	for _, head := range httpHeads {
		f.Add([]byte(head))
	}
	f.Fuzz(checkSniff)
}

// checkSniff holds sniff to its contract on in: it neither panics nor consumes input, and
// the name it returns is empty or a host name in the form clientHost keeps, taken from in.
func checkSniff(t *testing.T, in []byte) {
	c := sniffConn{r: bytes.NewReader(in)}
	br := bufio.NewReaderSize(c, peekSize)
	name, proto := sniff(c, br, time.Second)
	switch proto {
	case "", "tls", "http":
	default:
		t.Fatalf("protocol %q", proto)
	}
	if name != "" {
		if proto == "" {
			t.Errorf("name %q without a protocol", name)
		}
		if !isHostName(name) {
			t.Errorf("name %q is not a host name", name)
		}
		if !bytes.Contains(bytes.ToLower(in), []byte(name)) {
			t.Errorf("name %q is not in the input", name)
		}
	}
	if rest, err := io.ReadAll(br); err != nil || !bytes.Equal(rest, in) {
		t.Errorf("sniff consumed input: %d of %d bytes left", len(rest), len(in))
	}
}

// isHostName reports whether s is an IP address or a host name in the form the policy
// keeps: labels of lower-case letters, digits, hyphens and underscores, none empty or
// edged by a hyphen, and at most 253 bytes in all.
func isHostName(s string) bool {
	if net.ParseIP(s) != nil {
		return true
	}
	if s == "" || len(s) > 253 {
		return false
	}
	for _, label := range strings.Split(s, ".") {
		if label == "" || len(label) > 63 || label[0] == '-' || label[len(label)-1] == '-' {
			return false
		}
		for i := range len(label) {
			if c := label[i]; (c < 'a' || c > 'z') && (c < '0' || c > '9') && c != '-' && c != '_' {
				return false
			}
		}
	}
	return true
}

// tlsSeeds are ClientHellos as crypto/tls sends them, cut short, with length fields that
// disagree with the bytes present, and with the server name changed to ones a sandbox could
// send.
func tlsSeeds(f testing.TB) [][]byte {
	var seeds [][]byte
	for _, name := range []string{"example.com", "Registry.NPMJS.org", "xn--bcher-kva.example", "a.b_c-d.example", "10.0.0.1"} {
		hello := clientHello(f, name)
		seeds = append(seeds, hello)
		for _, n := range []int{1, 5, 9, 60, len(hello) / 2, len(hello) - 1} {
			seeds = append(seeds, hello[:n])
		}
		for _, at := range []int{3, 4, 6, 7, 8} { // record and handshake lengths
			m := bytes.Clone(hello)
			m[at] = 0xff
			seeds = append(seeds, m)
		}
	}
	hello := clientHello(f, "example.com")
	for _, bad := range []string{" example.co", "example.co ", "exa\x00ple.com", "*.xample.com", "EXAMPLE.com", "ex\xffmple.com"} {
		seeds = append(seeds, bytes.Replace(hello, []byte("example.com"), []byte(bad), 1))
	}
	return seeds
}

// httpHeads are request heads the gateway sees on plain HTTP ports: Host with and without a
// port, IPv6 literals, odd spacing and case, and heads that are malformed, cut short or hide
// a second Host.
var httpHeads = []string{
	"GET / HTTP/1.1\r\nHost: example.com\r\n\r\n",
	"GET /index.html HTTP/1.1\r\nhost:  Example.com:8080 \r\nAccept: */*\r\n\r\n",
	"CONNECT registry.npmjs.org:443 HTTP/1.1\r\nHost: registry.npmjs.org:443\r\n\r\n",
	"POST /x HTTP/1.1\r\nHost: [2001:db8::1]:80\r\nContent-Length: 0\r\n\r\n",
	"GET / HTTP/1.1\r\nX: y\r\n\r\nHost: smuggled.example",
	"GET / HTTP/1.1\r\nHost:  example.com \r\nHost: second.example\r\n\r\n",
	"GET / HTTP/1.1\r\nHost: exa mple.com\r\n\r\n",
	"GET / HTTP/1.1\r\nHost: *.example.com\r\n\r\n",
	"GET / HTTP/1.1\r\nHost: example.com..\r\n\r\n",
	"GET / HTTP/1.1\r\nHost: evil.example/../x\r\n\r\n",
	"GET / HTTP/1.1\r\nHost: \r\n\r\n",
	"GET / HTTP/1.1\r\nHost: example.com",
	"GET / HTTP/1.1\r\n",
	"GET",
	"G",
}
