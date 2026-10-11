//go:build linux

package guest

import (
	"bufio"
	"io"
	"net"
	"testing"
	"time"

	"github.com/hashicorp/yamux"
)

// Vite and other dev servers that listen on "localhost" often bind only ::1.
func TestDialLoopbackFallsBackToIPv6(t *testing.T) {
	ln, err := net.Listen("tcp6", "[::1]:0")
	if err != nil {
		t.Skip("no IPv6 loopback:", err)
	}
	defer ln.Close()
	port := ln.Addr().(*net.TCPAddr).Port
	go func() {
		if c, err := ln.Accept(); err == nil {
			c.Close()
		}
	}()
	c, err := dialLoopback(port)
	if err != nil {
		t.Fatalf("dialLoopback(%d): %v", port, err)
	}
	c.Close()
}

// When the server closes (as dev servers do with idle keep-alive connections), the host
// must see EOF, or it reuses the dead connection and its next request hangs.
func TestServTCPPassesServerClose(t *testing.T) {
	ln, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	go func() {
		if c, err := ln.Accept(); err == nil {
			c.Write([]byte("bye"))
			c.Close()
		}
	}()

	hostEnd, guestEnd := net.Pipe()
	host, err := yamux.Client(hostEnd, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer host.Close()
	guest, err := yamux.Server(guestEnd, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer guest.Close()
	go func() {
		st, err := guest.Accept()
		if err != nil {
			return
		}
		defer st.Close()
		servTCP(st, bufio.NewReader(st), ln.Addr().(*net.TCPAddr).Port)
	}()

	st, err := host.Open()
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	st.SetReadDeadline(time.Now().Add(5 * time.Second))
	got, err := io.ReadAll(st)
	if err != nil || string(got) != "bye" {
		t.Fatalf("read %q, %v; want the server's bytes, then EOF", got, err)
	}
}
