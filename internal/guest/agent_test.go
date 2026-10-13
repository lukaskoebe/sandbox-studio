//go:build linux

package guest

import (
	"bufio"
	"bytes"
	"context"
	"io"
	"net"
	"sync"
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

// writeRecorder records the size of every write that reaches the connection.
type writeRecorder struct {
	net.Conn
	mu     sync.Mutex
	writes []int
}

func (w *writeRecorder) Write(p []byte) (int, error) {
	w.mu.Lock()
	w.writes = append(w.writes, len(p))
	w.mu.Unlock()
	return w.Conn.Write(p)
}

// A reply longer than a stream window reaches the host whole, in writes of at most
// maxChannelWrite bytes. A single write over 32 KiB stalls msb's vsock (docs/guest-agent.md).
func TestChannelWritesStayBounded(t *testing.T) {
	hostEnd, guestEnd := net.Pipe()
	rec := &writeRecorder{Conn: guestEnd}
	guest, err := hostSession(rec)
	if err != nil {
		t.Fatal(err)
	}
	defer guest.Close()
	host, err := yamux.Server(hostEnd, yamux.DefaultConfig())
	if err != nil {
		t.Fatal(err)
	}
	defer host.Close()

	// A reply of about 740 KiB: a stream window (256 KiB) is about a third of it.
	reply := bytes.Repeat([]byte("a reply line of a browser screenshot\n"), 20<<10)
	go func() {
		if st, err := guest.Open(); err == nil {
			st.Write(reply)
			st.Close()
		}
	}()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	st, err := host.AcceptStreamWithContext(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	st.SetReadDeadline(time.Now().Add(10 * time.Second))
	got, err := io.ReadAll(st)
	if err != nil || !bytes.Equal(got, reply) {
		t.Fatalf("host read %d of %d bytes, %v", len(got), len(reply), err)
	}
	rec.mu.Lock()
	defer rec.mu.Unlock()
	for _, n := range rec.writes {
		if n > maxChannelWrite {
			t.Fatalf("a guest write of %d bytes; the limit is %d", n, maxChannelWrite)
		}
	}
	if len(rec.writes) < len(reply)/maxChannelWrite {
		t.Fatalf("%d writes for %d bytes; the reply was not split", len(rec.writes), len(reply))
	}
}
