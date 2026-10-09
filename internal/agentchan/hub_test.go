package agentchan

import (
	"bufio"
	"context"
	"io"
	"log/slog"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/hashicorp/yamux"

	"github.com/lukaskoebe/sandbox-studio/internal/agentproto"
)

// fakeGuest dials the hub socket and answers like a minimal studio-agent. Configurations it
// receives go to configs.
func fakeGuest(t *testing.T, path string, configs chan<- agentproto.Config) *yamux.Session {
	t.Helper()
	nc, err := net.Dial("unix", path)
	if err != nil {
		t.Fatal(err)
	}
	sess, err := yamux.Client(nc, nil)
	if err != nil {
		t.Fatal(err)
	}
	st, _ := sess.Open()
	agentproto.WriteJSONLine(st, agentproto.Header{Kind: agentproto.KindHello})
	agentproto.WriteJSONLine(st, agentproto.Hello{Version: "test", Arch: "amd64"})
	st.Close()
	go func() {
		for {
			st, err := sess.Accept()
			if err != nil {
				return
			}
			go func() {
				defer st.Close()
				br := bufio.NewReader(st)
				var h agentproto.Header
				agentproto.ReadJSONLine(br, &h)
				switch h.Kind {
				case agentproto.KindSessions:
					agentproto.WriteJSONLine(st, []agentproto.Session{{Name: "main", Windows: 1}})
				case agentproto.KindPorts:
					agentproto.WriteJSONLine(st, agentproto.Error{Error: "boom"})
				case agentproto.KindConfig:
					var cfg agentproto.Config
					agentproto.ReadJSONLine(br, &cfg)
					if cfg.CA == "bad" {
						agentproto.WriteJSONLine(st, agentproto.Error{Error: "bad CA"})
						return
					}
					agentproto.WriteJSONLine(st, struct{}{})
					configs <- cfg
				case agentproto.KindPTY:
					// Echo input upper-cased, then exit on "exit".
					for {
						typ, p, err := agentproto.ReadFrame(br)
						if err != nil {
							return
						}
						if typ != agentproto.FrameData {
							continue
						}
						if string(p) == "exit" {
							agentproto.WriteFrame(st, agentproto.FrameExit, []byte("0"))
							return
						}
						agentproto.WriteFrame(st, agentproto.FrameData, []byte(strings.ToUpper(string(p))))
					}
				}
			}()
		}
	}()
	return sess
}

func TestHub(t *testing.T) {
	dir, err := os.MkdirTemp("", "hub")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(dir)
	path := filepath.Join(dir, "s.sock")
	h := NewHub(slog.New(slog.NewTextHandler(io.Discard, nil)))
	connected := make(chan string, 1)
	h.OnConnect = func(id string) { connected <- id }
	if err := h.Listen("sb1", path); err != nil {
		t.Fatal(err)
	}
	defer h.Close("sb1")

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if _, err := h.Sessions(ctx, "sb1"); err != ErrNotConnected {
		t.Fatalf("want ErrNotConnected, got %v", err)
	}
	configs := make(chan agentproto.Config, 1)
	g := fakeGuest(t, path, configs)
	defer g.Close()
	if err := h.WaitConnected(ctx, "sb1"); err != nil {
		t.Fatal(err)
	}
	select {
	case id := <-connected:
		if id != "sb1" {
			t.Fatalf("OnConnect(%q)", id)
		}
	case <-ctx.Done():
		t.Fatal("OnConnect not called")
	}
	if hello, ok := h.Connected("sb1"); !ok || hello.Version != "test" {
		t.Fatalf("hello %+v %v", hello, ok)
	}

	sessions, err := h.Sessions(ctx, "sb1")
	if err != nil || len(sessions) != 1 || sessions[0].Name != "main" {
		t.Fatalf("sessions %+v %v", sessions, err)
	}
	if _, err := h.Ports(ctx, "sb1"); err == nil || err.Error() != "boom" {
		t.Fatalf("want guest error, got %v", err)
	}

	want := agentproto.Config{CA: "pem", Env: map[string]string{"API_KEY": "studio-x"}}
	if err := h.Configure(ctx, "sb1", want); err != nil {
		t.Fatal(err)
	}
	if got := <-configs; got.CA != want.CA || got.Env["API_KEY"] != "studio-x" {
		t.Fatalf("guest got %+v", got)
	}
	if err := h.Configure(ctx, "sb1", agentproto.Config{CA: "bad"}); err == nil || err.Error() != "bad CA" {
		t.Fatalf("want guest error, got %v", err)
	}

	p, err := h.OpenPTY(ctx, "sb1", "main", 80, 24)
	if err != nil {
		t.Fatal(err)
	}
	p.Write([]byte("hi"))
	buf := make([]byte, 16)
	n, err := p.Read(buf)
	if err != nil || string(buf[:n]) != "HI" {
		t.Fatalf("pty read %q %v", buf[:n], err)
	}
	p.Write([]byte("exit"))
	if _, err := p.Read(buf); err != io.EOF {
		t.Fatalf("want EOF after exit, got %v", err)
	}

	g.Close()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if _, ok := h.Connected("sb1"); !ok {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("session not dropped after guest disconnect")
}
