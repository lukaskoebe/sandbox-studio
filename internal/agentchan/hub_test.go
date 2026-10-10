package agentchan

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
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

// TestHubDisconnect drops a live session without closing the listener, so the guest
// agent can connect again (resume after suspend).
func TestHubDisconnect(t *testing.T) {
	dir, err := os.MkdirTemp("", "hub")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(dir)
	path := filepath.Join(dir, "s.sock")
	h := NewHub(slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err := h.Listen("sb1", path); err != nil {
		t.Fatal(err)
	}
	defer h.Close("sb1")
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	g := fakeGuest(t, path, make(chan agentproto.Config, 1))
	if err := h.WaitConnected(ctx, "sb1"); err != nil {
		t.Fatal(err)
	}
	h.Disconnect("sb1")
	if _, ok := h.Connected("sb1"); ok {
		t.Fatal("still connected after Disconnect")
	}
	select {
	case <-g.CloseChan():
	case <-ctx.Done():
		t.Fatal("guest session not closed")
	}

	g2 := fakeGuest(t, path, make(chan agentproto.Config, 1))
	defer g2.Close()
	if err := h.WaitConnected(ctx, "sb1"); err != nil {
		t.Fatal(err)
	}
	if _, err := h.Sessions(ctx, "sb1"); err != nil {
		t.Fatalf("sessions after reconnect: %v", err)
	}
}

// TestHubCalls checks that a guest's calls reach OnCall with the sandbox of the socket,
// whatever the payload claims, and that oversized calls are refused.
func TestHubCalls(t *testing.T) {
	dir, _ := os.MkdirTemp("", "hub")
	defer os.RemoveAll(dir)
	h := NewHub(slog.New(slog.NewTextHandler(io.Discard, nil)))
	type got struct{ id, method, params string }
	calls := make(chan got, 4)
	h.OnCall = func(_ context.Context, id, method string, params json.RawMessage) (any, error) {
		calls <- got{id, method, string(params)}
		if method == "fail" {
			return nil, errors.New("nope")
		}
		return map[string]string{"ok": id}, nil
	}
	sock := filepath.Join(dir, "a.sock")
	if err := h.Listen("sbx-a", sock); err != nil {
		t.Fatal(err)
	}
	defer h.Close("sbx-a")
	sess := fakeGuest(t, sock, nil)
	defer sess.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := h.WaitConnected(ctx, "sbx-a"); err != nil {
		t.Fatal(err)
	}
	call := func(c any) agentproto.CallReply {
		st, err := sess.Open()
		if err != nil {
			t.Fatal(err)
		}
		defer st.Close()
		agentproto.WriteJSONLine(st, agentproto.Header{Kind: agentproto.KindCall})
		agentproto.WriteJSONLine(st, c)
		var r agentproto.CallReply
		if err := agentproto.ReadJSONLine(bufio.NewReader(st), &r); err != nil {
			t.Fatal(err)
		}
		return r
	}
	r := call(agentproto.Call{Method: "hook", Params: json.RawMessage(`{"sandbox":"sbx-b","persona":"other"}`)})
	if r.Error != "" || string(r.Result) != `{"ok":"sbx-a"}` {
		t.Fatalf("reply: %+v", r)
	}
	if g := <-calls; g.id != "sbx-a" || g.method != "hook" {
		t.Fatalf("call: %+v", g)
	}
	if r := call(agentproto.Call{Method: "fail"}); r.Error != "nope" {
		t.Fatalf("error reply: %+v", r)
	}
	<-calls
	big := agentproto.Call{Method: "hook", Params: json.RawMessage(`"` + strings.Repeat("x", agentproto.MaxCallLine) + `"`)}
	if r := call(big); r.Error == "" {
		t.Fatal("oversized call accepted")
	}
	select {
	case g := <-calls:
		t.Fatalf("oversized call reached OnCall: %s", g.method)
	default:
	}
}
