package gateway

import (
	"bufio"
	"bytes"
	"net"
	"strings"
	"time"

	"golang.org/x/crypto/cryptobyte"

	"github.com/lukaskoebe/sandbox-studio/internal/policy"
)

// peekSize bounds what the gateway reads ahead to find a name: one full TLS record (a
// ClientHello with post-quantum key shares is about 2 KB, but may be up to 16 KB) or
// the request headers of plain HTTP.
const peekSize = 16<<10 + 5

// sniff waits up to timeout for the client's first bytes and returns the server name they
// carry (TLS SNI or the HTTP Host header). Nothing is consumed: the peeked bytes stay in br
// and are forwarded upstream.
func sniff(c net.Conn, br *bufio.Reader, timeout time.Duration) (name, proto string) {
	c.SetReadDeadline(time.Now().Add(timeout))
	defer c.SetReadDeadline(time.Time{})
	first, err := br.Peek(1)
	if err != nil {
		return "", ""
	}
	if first[0] == 0x16 { // TLS handshake record
		hdr, err := br.Peek(5)
		if err != nil {
			return "", "tls"
		}
		n := min(5+(int(hdr[3])<<8|int(hdr[4])), peekSize)
		rec, _ := br.Peek(n) // a short record still often holds the SNI
		return serverName(rec[5:]), "tls"
	}
	if !looksLikeHTTP(br) {
		return "", ""
	}
	for {
		b, _ := br.Peek(br.Buffered())
		if end := bytes.Index(b, []byte("\r\n\r\n")); end >= 0 || len(b) >= peekSize {
			return httpHost(b), "http"
		}
		if _, err := br.Peek(len(b) + 1); err != nil {
			b, _ = br.Peek(br.Buffered())
			return httpHost(b), "http"
		}
	}
}

var methods = []string{"GET ", "POST ", "PUT ", "HEAD ", "DELETE ", "OPTIONS ", "PATCH ", "CONNECT ", "TRACE "}

func looksLikeHTTP(br *bufio.Reader) bool {
	b, _ := br.Peek(min(br.Buffered(), 8))
	for _, m := range methods {
		if strings.HasPrefix(m, string(b)) || strings.HasPrefix(string(b), m) {
			return true
		}
	}
	return false
}

// httpHost returns the host of the Host header in a request head.
func httpHost(head []byte) string {
	for _, line := range strings.Split(string(head), "\r\n")[1:] {
		k, v, ok := strings.Cut(line, ":")
		if !ok {
			if line == "" {
				break
			}
			continue
		}
		if strings.EqualFold(strings.TrimSpace(k), "host") {
			v = strings.TrimSpace(v)
			if h, _, err := net.SplitHostPort(v); err == nil {
				return h
			}
			return strings.Trim(v, "[]")
		}
	}
	return ""
}

// serverName extracts the SNI host name from a TLS handshake message (a ClientHello). A
// message cut short by the end of the peeked record still yields the name if it comes
// early enough.
func serverName(msg []byte) string {
	if len(msg) < 4 || msg[0] != 1 { // client_hello
		return ""
	}
	n := int(msg[1])<<16 | int(msg[2])<<8 | int(msg[3])
	body := cryptobyte.String(msg[4:min(4+n, len(msg))])
	var skip cryptobyte.String
	var extLen uint16
	if !body.Skip(2+32) || // version, random
		!body.ReadUint8LengthPrefixed(&skip) || // session id
		!body.ReadUint16LengthPrefixed(&skip) || // cipher suites
		!body.ReadUint8LengthPrefixed(&skip) || // compression methods
		!body.ReadUint16(&extLen) {
		return ""
	}
	exts := body[:min(int(extLen), len(body))]
	for !exts.Empty() {
		var typ uint16
		var data cryptobyte.String
		if !exts.ReadUint16(&typ) || !exts.ReadUint16LengthPrefixed(&data) {
			return ""
		}
		if typ != 0 { // server_name
			continue
		}
		var list cryptobyte.String
		if !data.ReadUint16LengthPrefixed(&list) {
			return ""
		}
		for !list.Empty() {
			var nameType uint8
			var name cryptobyte.String
			if !list.ReadUint8(&nameType) || !list.ReadUint16LengthPrefixed(&name) {
				return ""
			}
			if nameType == 0 {
				return string(name)
			}
		}
	}
	return ""
}

// validHost reports whether name can be used as a dial target and rule subject.
func validHost(name string) bool {
	p, err := policy.ValidPattern(name)
	return err == nil && !strings.Contains(p, "*")
}
