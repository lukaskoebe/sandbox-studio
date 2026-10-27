package launcher

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// fakeStudio serves once started, after a few polls, and records the commands it ran.
type fakeStudio struct {
	serving  atomic.Bool
	starts   atomic.Int32
	exit     chan error
	commands []string
	opened   []string
}

func (f *fakeStudio) launcher() *Launcher {
	return &Launcher{
		Addr:    "127.0.0.1:7878",
		Serving: func(context.Context) bool { return f.serving.Load() },
		Start: func() (<-chan error, error) {
			f.starts.Add(1)
			if f.exit != nil {
				return f.exit, nil
			}
			go func() {
				time.Sleep(20 * time.Millisecond)
				f.serving.Store(true)
			}()
			return make(chan error), nil
		},
		Studio: func(_ context.Context, args ...string) (string, error) {
			f.commands = append(f.commands, strings.Join(args, " "))
			switch args[len(args)-1] {
			case "login-url":
				return "http://127.0.0.1:7878/#studio-login=t\n", nil
			case "quit":
				f.serving.Store(false)
			}
			return "", nil
		},
		Open: func(url string) error { f.opened = append(f.opened, url); return nil },
		Poll: 5 * time.Millisecond,
	}
}

func TestOpenStartsStudioOnceAndOpensALoginLink(t *testing.T) {
	f := &fakeStudio{}
	l := f.launcher()
	if err := l.OpenStudio(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := l.OpenStudio(context.Background()); err != nil {
		t.Fatal(err)
	}
	if f.starts.Load() != 1 || len(f.opened) != 2 || f.opened[0] != "http://127.0.0.1:7878/#studio-login=t" {
		t.Fatalf("starts %d, opened %v", f.starts.Load(), f.opened)
	}
	if err := l.Quit(context.Background()); err != nil || f.serving.Load() || f.commands[len(f.commands)-1] != "-addr 127.0.0.1:7878 quit" {
		t.Fatalf("quit: %v, commands %v", err, f.commands)
	}
}

func TestEnsureReportsAStudioThatExits(t *testing.T) {
	f := &fakeStudio{exit: make(chan error, 1)}
	f.exit <- errors.New("exit status 1")
	err := f.launcher().Ensure(context.Background())
	if err == nil || !strings.Contains(err.Error(), "exit status 1") {
		t.Fatalf("ensure = %v", err)
	}
}

func TestWatchReportsChanges(t *testing.T) {
	f := &fakeStudio{}
	l := f.launcher()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	states := make(chan State, 4)
	go l.Watch(ctx, func(s State) { states <- s })
	if s := <-states; s != Stopped {
		t.Fatalf("first State %v", s)
	}
	f.serving.Store(true)
	select {
	case s := <-states:
		if s != Running {
			t.Fatalf("then %v", s)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("no change reported")
	}
}

func TestHTTPServing(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/auth/status" {
			_, _ = w.Write([]byte(`{"authenticated":false}`))
			return
		}
		http.NotFound(w, r)
	}))
	defer srv.Close()
	if !HTTPServing(strings.TrimPrefix(srv.URL, "http://"))(context.Background()) {
		t.Fatal("a Studio wasn't seen")
	}
	other := httptest.NewServer(http.NotFoundHandler())
	defer other.Close()
	if HTTPServing(strings.TrimPrefix(other.URL, "http://"))(context.Background()) {
		t.Fatal("another server counted as Studio")
	}
}
