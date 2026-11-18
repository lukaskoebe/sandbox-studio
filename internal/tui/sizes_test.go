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
	terminal := func(t *testing.T, m *app) *app {
		m.open(place{kind: placeComputer, sandbox: "sb2"})
		m.comp.tab, m.comp.term = tabTerminal, "codex"
		return live(t, settle(m))
	}
	agent := func(t *testing.T, m *app) *app { return live(t, openSession(t, m, "sb2", "codex")) }
	states := map[string]func(t *testing.T, m *app) *app{
		"home": func(_ *testing.T, m *app) *app { return m },
		"reading computer": func(t *testing.T, m *app) *app {
			m.goHome()
			for _, r := range m.inboxRows() {
				if r.sb != "" {
					m.inbox.cursor = r.id
					return m
				}
			}
			t.Fatal("Home's list has no computer")
			return m
		},
		"drawer": func(_ *testing.T, m *app) *app {
			m.drawer, m.focus = true, focusSidebar
			return m
		},
		"thread": func(_ *testing.T, m *app) *app { return press(sampleChat(m), "p", "h", "i") },
		"thread+card": func(t *testing.T, m *app) *app {
			sampleApprovals(m)
			return openSession(t, m, "sb2", "Port the parser to go")
		},
		"terminal": terminal,
		"typing": func(t *testing.T, m *app) *app {
			m = terminal(t, m)
			m.pane.focus = true
			return m
		},
		"zoomed": func(t *testing.T, m *app) *app {
			m = terminal(t, m)
			m.pane.focus, m.zoom = true, true
			return m
		},
		"zoomed thread": func(t *testing.T, m *app) *app {
			m = agent(t, m)
			m.zoom = true
			return m
		},
		"banner": func(t *testing.T, m *app) *app {
			m = agent(t, m)
			m.pane.focus = true
			return press(m, "alt+a")
		},
		"flipped": func(t *testing.T, m *app) *app {
			m = agent(t, m)
			m.flip = true
			return m
		},
		"details":        func(_ *testing.T, m *app) *app { return openComputer(m, "sb2", tabDetails) },
		"mate":           func(_ *testing.T, m *app) *app { m.open(place{kind: placeMate, mate: "t1"}); return m },
		"asks":           func(_ *testing.T, m *app) *app { return pendingAsks(sampleChat(m), `{"type":"object"}`) },
		"inbox":          func(_ *testing.T, m *app) *app { sampleApprovals(m); return press(onCard(m, 2), "right") },
		"changes":        func(_ *testing.T, m *app) *app { return withGitStatus(openComputer(m, "sb2", tabChanges)) },
		"changes review": func(_ *testing.T, m *app) *app { samplePush(m); return press(onCard(m, 0), "enter") },
		"files":          func(_ *testing.T, m *app) *app { return withFiles(openComputer(m, "sb2", tabFiles), "sb2") },
		"file open": func(_ *testing.T, m *app) *app {
			return withFileOpen(withFiles(openComputer(m, "sb2", tabFiles), "sb2"))
		},
		"browser": func(_ *testing.T, m *app) *app { return withBrowser(openComputer(m, "sb2", tabBrowser)) },
		"review place": func(_ *testing.T, m *app) *app {
			a := samplePush(m)
			a.EnvironmentID = "env2"
			m.approvals = nil
			m.openReview(a)
			return m
		},
		"push asks": func(_ *testing.T, m *app) *app { samplePush(m); return press(onCard(m, 0), "y") },
		"thread row": func(t *testing.T, m *app) *app {
			m.goHome()
			for _, r := range m.inboxRows() {
				if r.thread != nil {
					m.inbox.cursor = r.id
					return m
				}
			}
			t.Fatal("Home's list has no thread")
			return m
		},
		"activity":  func(_ *testing.T, m *app) *app { return press(sampleActivity(m), "l") },
		"computers": func(_ *testing.T, m *app) *app { m.openComputers(); return m },
		"delete":    func(_ *testing.T, m *app) *app { return press(openComputer(m, "sb2", tabDetails), "d") },
		"help":      func(_ *testing.T, m *app) *app { return press(m, "?") },
		"new":       func(_ *testing.T, m *app) *app { return press(m, "+") },
		"envs":      func(_ *testing.T, m *app) *app { return press(m, "e") },
	}
	sizes := [][2]int{}
	for _, w := range []int{0, 1, 2, 3, 5, 8, 12, 20, 33, 40, 60, 79, 80, 99, 100, 101, 119, 120, 121, 140} {
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
