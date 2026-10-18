package tui

import (
	"context"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/charmbracelet/x/ansi"
	"github.com/coder/websocket"

	tea "charm.land/bubbletea/v2"
)

func TestLiveTermDrawsTheOutputAndSendsKeysAndSizes(t *testing.T) {
	var mu sync.Mutex
	var keys strings.Builder
	sizes := make(chan string, 4)
	query := make(chan string, 1)
	client := fakeTerminal(t, func(ctx context.Context, c *websocket.Conn, r *http.Request) {
		query <- r.URL.RawQuery
		_ = c.Write(ctx, websocket.MessageBinary, []byte("\x1b[31mred\x1b[0m plain\r\n$ "))
		for {
			typ, data, err := c.Read(ctx)
			if err != nil {
				return
			}
			if typ == websocket.MessageText {
				sizes <- string(data)
				continue
			}
			mu.Lock()
			keys.Write(data)
			mu.Unlock()
		}
	})
	term, err := openTerm(context.Background(), client, "env1", "sb1", "claude", 40, 5, false)
	if err != nil {
		t.Fatal(err)
	}
	defer term.close()
	if q := <-query; q != "cols=40&rows=5" {
		t.Fatalf("attach size: %q", q)
	}
	waitFor(t, "the output", func() bool {
		lines := term.lines(40, 5)
		return len(lines) == 5 && ansi.Strip(lines[1]) == "$"
	})
	lines := term.lines(40, 5)
	if got := ansi.Strip(lines[0]); got != "red plain" {
		t.Fatalf("first line %q", got)
	}
	if !strings.Contains(lines[0], "red") || lines[0] == "red plain" {
		t.Fatalf("the color is lost: %q", lines[0])
	}
	if x, y, shown := term.cursor(); x != 2 || y != 1 || !shown {
		t.Fatalf("cursor at %d,%d shown %v", x, y, shown)
	}

	// Keys arrive as a terminal sends them: text, enter, an arrow, a control key.
	term.key(tea.KeyPressMsg{Code: 'l', Text: "l"})
	term.key(tea.KeyPressMsg{Code: 's', Text: "s"})
	term.key(tea.KeyPressMsg{Code: tea.KeyEnter})
	term.key(tea.KeyPressMsg{Code: tea.KeyUp})
	term.key(tea.KeyPressMsg{Code: 'c', Mod: tea.ModCtrl})
	term.paste("pasted")
	waitFor(t, "the keys", func() bool {
		mu.Lock()
		defer mu.Unlock()
		return keys.String() == "ls\r\x1b[A\x03pasted"
	})

	term.resize(60, 10)
	select {
	case msg := <-sizes:
		if msg != `{"cols":60,"rows":10,"type":"resize"}` {
			t.Fatalf("resize message: %s", msg)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the size was not sent")
	}
	if got := len(term.lines(60, 10)); got != 10 {
		t.Fatalf("%d rows after the resize", got)
	}
}

func TestLiveTermReportsItsEnd(t *testing.T) {
	client := fakeTerminal(t, func(ctx context.Context, c *websocket.Conn, _ *http.Request) {
		_ = c.Write(ctx, websocket.MessageBinary, []byte("bye"))
		c.Close(websocket.StatusNormalClosure, "exited")
	})
	term, err := openTerm(context.Background(), client, "env1", "sb1", "main", 20, 3, false)
	if err != nil {
		t.Fatal(err)
	}
	defer term.close()
	ended := false
	for range 3 {
		msg := term.waitOutput(7)()
		if _, ok := msg.(paneEnded); ok {
			ended = true
			break
		}
	}
	if !ended {
		t.Fatal("the end was not reported")
	}
	if got := ansi.Strip(term.lines(20, 3)[0]); got != "bye" {
		t.Fatalf("last output %q", got)
	}
	term.key(tea.KeyPressMsg{Code: 'x', Text: "x"}) // after the end, a key is dropped, not stuck
}

func TestLiveTermDrawsWideCharactersOnce(t *testing.T) {
	term := newLiveTerm(10, 2, false)
	go term.writeInput()
	defer term.close()
	term.write([]byte("日本 ok"))
	if got := ansi.Strip(term.lines(10, 2)[0]); got != "日本 ok" {
		t.Fatalf("wide line %q", got)
	}
	if got := ansi.Strip(term.lines(3, 2)[0]); got != "日" {
		t.Fatalf("a wide character that doesn't fit is left out: %q", got)
	}
}
