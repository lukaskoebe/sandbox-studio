package tui

import (
	"context"
	"fmt"
	"net/http"
	"runtime/debug"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/coder/websocket"
)

// Every screen draws at every size, down to a terminal of one cell, as a phone's turned on its
// side or a split too small gives it: the frame may be cut, but the TUI never panics.
func TestEveryScreenDrawsAtAnySize(t *testing.T) {
	client := fakeTerminal(t, func(ctx context.Context, c *websocket.Conn, _ *http.Request) {
		_ = c.Write(ctx, websocket.MessageBinary, []byte("codex is ready\r\n> "))
		for {
			if _, _, err := c.Read(ctx); err != nil {
				return
			}
		}
	})
	live := func(t *testing.T, m *app) *app {
		m.client = client
		m = run(m, m.openPane())
		if m.pane.term == nil {
			t.Fatalf("the terminal is not connected: %v", m.pane.err)
		}
		waitFor(t, "the agent's screen", func() bool { return strings.Contains(strings.Join(m.pane.term.lines(80, 10), ""), "codex is ready") })
		return m
	}
	states := map[string]func(t *testing.T, m *app) *app{
		"home":      func(_ *testing.T, m *app) *app { return m },
		"dashboard": func(_ *testing.T, m *app) *app { return press(m, "j", "j", "j", "j") },
		"terminal":  func(t *testing.T, m *app) *app { return live(t, press(m, "j", "j", "j", "j", "j")) },
		"typing": func(t *testing.T, m *app) *app {
			m = live(t, press(m, "j", "j", "j", "j", "j"))
			m.pane.focus = true
			return m
		},
		"zoomed": func(t *testing.T, m *app) *app {
			m = live(t, press(m, "j", "j", "j", "j", "j"))
			m.pane.focus, m.pane.zoom = true, true
			return m
		},
		"banner": func(t *testing.T, m *app) *app {
			m = live(t, press(m, "j", "j", "j", "j", "j"))
			m.pane.focus = true
			return press(m, "alt+a")
		},
		"chat":      func(_ *testing.T, m *app) *app { return press(sampleChat(m), "p", "h", "i") },
		"asks":      func(_ *testing.T, m *app) *app { return pendingAsks(sampleChat(m), `{"type":"object"}`) },
		"approvals": func(_ *testing.T, m *app) *app { sampleApprovals(m); return press(m, "a", "j", "j", "right") },
		"git":       func(_ *testing.T, m *app) *app { samplePush(m); return press(m, "g") },
		"review":    func(_ *testing.T, m *app) *app { samplePush(m); return press(m, "a", "enter") },
		"activity":  func(_ *testing.T, m *app) *app { return press(sampleActivity(m), "l") },
		"detail":    func(_ *testing.T, m *app) *app { return press(m, "enter") },
		"delete":    func(_ *testing.T, m *app) *app { return press(m, "d") },
		"help":      func(_ *testing.T, m *app) *app { return press(m, "?") },
		"new":       func(_ *testing.T, m *app) *app { return press(m, "n") },
		"envs":      func(_ *testing.T, m *app) *app { return press(m, "e") },
	}
	sizes := [][2]int{}
	for _, w := range []int{0, 1, 2, 3, 5, 8, 12, 20, 33, 40, 60, 79, 80, 99, 100, 101, 120, 140} {
		for _, h := range []int{0, 1, 2, 3, 4, 5, 6, 7, 8, 10, 12, 16, 24, 40} {
			sizes = append(sizes, [2]int{w, h})
		}
	}
	for name, state := range states {
		for _, plain := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/plain=%v", name, plain), func(t *testing.T) {
				var m *app
				if plain {
					m = withSessions(sampleApp())
					m.width, m.height = 140, 40
				} else {
					m = paneApp()
				}
				m = state(t, m)
				for _, s := range sizes {
					if err := drawAt(m, s[0], s[1]); err != "" {
						t.Fatalf("at %d×%d: %s", s[0], s[1], err)
					}
				}
			})
		}
	}
}

// drawAt resizes the TUI to w×h and draws it, and returns the panic it caused, with its stack.
func drawAt(m *app, w, h int) (failed string) {
	defer func() {
		if r := recover(); r != nil {
			failed = fmt.Sprint(r) + "\n" + string(debug.Stack())
		}
	}()
	next, cmd := m.Update(tea.WindowSizeMsg{Width: w, Height: h})
	_ = cmd
	next.(*app).View()
	return ""
}
