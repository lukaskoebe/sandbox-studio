package tui

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
)

// The frame's columns at each width: one column below paneWidth, the sidebar from it, and a
// thread's computer beside it from splitWidth, where an agent's terminal takes the wider share
// and full screen takes it all.
func TestLayoutAtEachWidth(t *testing.T) {
	home := withSessions(sampleApp())
	chat := openSession(t, withSessions(sampleApp()), "sb1", "Fix the login form")
	term := openSession(t, withSessions(sampleApp()), "sb1", "claude")
	for _, c := range []struct {
		name  string
		m     *app
		width int
		want  layout
	}{
		{"home", home, 60, layout{0, 60, 0}},
		{"home", home, 80, layout{0, 80, 0}},
		{"home", home, 99, layout{0, 99, 0}},
		{"home", home, 100, layout{30, 69, 0}},
		{"home", home, 119, layout{30, 88, 0}},
		{"home", home, 140, layout{30, 109, 0}}, // Home has no computer beside it
		{"chat", chat, 80, layout{0, 80, 0}},
		{"chat", chat, 100, layout{30, 69, 0}},
		{"chat", chat, 119, layout{30, 88, 0}},
		{"chat", chat, 120, layout{30, 40, 48}},
		{"chat", chat, 140, layout{30, 45, 63}},
		{"chat", chat, 180, layout{30, 60, 88}},
		{"terminal", term, 120, layout{30, 48, 40}},
		{"terminal", term, 140, layout{30, 63, 45}},
		{"terminal", term, 180, layout{30, 88, 60}},
	} {
		if got := c.m.layoutFor(c.width); got != c.want {
			t.Errorf("%s at %d: %+v, want %+v", c.name, c.width, got, c.want)
		}
	}
	chat.zoom = true
	if got := chat.layoutFor(140); got != (layout{0, 140, 0}) {
		t.Errorf("zoomed at 140: %+v", got)
	}
}

// tab moves the cursor between a thread and its computer, beside it from splitWidth and in its
// place below; \ gives it to the sidebar and back.
func TestTabAndBackslashMoveTheCursor(t *testing.T) {
	m := openSession(t, withSessions(sampleApp()), "sb1", "Fix the login form")
	m.width, m.height = 140, 40
	m = press(m, "esc") // out of the composer
	if m.chats.composing || m.focus != focusMain {
		t.Fatalf("esc leaves the composer: composing %v, focus %v", m.chats.composing, m.focus)
	}
	if m = press(m, "tab"); m.focus != focusSide || m.mainPane() != paneThread {
		t.Fatalf("tab at 140: focus %v, main %v", m.focus, m.mainPane())
	}
	if m = press(m, "tab"); m.focus != focusMain {
		t.Fatalf("tab back at 140: focus %v", m.focus)
	}
	if m = press(m, "\\"); !m.sidebarKeys() || m.drawer {
		t.Fatalf("\\ at 140: sidebar keys %v, drawer %v", m.sidebarKeys(), m.drawer)
	}
	if m = press(m, "\\"); m.sidebarKeys() || m.focus != focusMain {
		t.Fatalf("\\ again at 140: sidebar keys %v, focus %v", m.sidebarKeys(), m.focus)
	}

	m.width, m.height = 80, 24
	if m = press(m, "tab"); !m.flip || m.mainPane() != paneComputer {
		t.Fatalf("tab at 80: flip %v, main %v", m.flip, m.mainPane())
	}
	if m = press(m, "tab"); m.flip || m.mainPane() != paneThread {
		t.Fatalf("tab back at 80: flip %v, main %v", m.flip, m.mainPane())
	}
}

// Below paneWidth, \ opens the sidebar as a drawer over the body, on the row of the place on
// show; it takes the keys, and enter opens the row's place and closes it.
func TestTheDrawerTakesTheKeysAndCloses(t *testing.T) {
	m := openSession(t, withSessions(sampleApp()), "sb1", "Fix the login form")
	m = press(m, "esc", "\\")
	if !m.drawer || !m.sidebarKeys() {
		t.Fatalf("\\ at 80: drawer %v, sidebar keys %v", m.drawer, m.sidebarKeys())
	}
	if x, ok := m.selectedSession(); !ok || x.title() != "Fix the login form" {
		t.Fatalf("the drawer opens on %+v, not the thread on show", x)
	}
	if out := strings.Join(m.mainFrame(m.width, m.height), "\n"); !strings.Contains(out, "YOUR COMPUTERS") {
		t.Fatalf("the drawer is not drawn:\n%s", out)
	}
	for i := 0; i < 5 && m.at != atMate; i++ {
		m = press(m, "k")
	}
	if m.at != atMate || m.mate != "t1" || !m.drawer {
		t.Fatalf("k in the drawer: at %v, mate %q, drawer %v", m.at, m.mate, m.drawer)
	}
	m = press(m, "enter")
	if m.drawer || m.here.kind != placeMate || m.here.mate != "t1" {
		t.Fatalf("enter in the drawer: drawer %v, here %+v", m.drawer, m.here)
	}
	if m = press(m, "\\", "esc"); m.drawer || m.sidebarKeys() || m.here.kind != placeMate {
		t.Fatalf("esc closes the drawer: drawer %v, here %+v", m.drawer, m.here)
	}
}

// esc walks back through the places shown, skipping none that still live, to Home.
func TestEscWalksTheHistoryBackToHome(t *testing.T) {
	m := withSessions(sampleApp())
	m.open(place{kind: placeMate, mate: "t1"})
	m = openSession(t, m, "sb1", "Fix the login form")
	m = openComputer(m, "sb3", tabDetails)
	m = press(m, "esc")
	if m.here.kind != placeThread || m.here.chat != "c1" {
		t.Fatalf("esc from the computer: %+v", m.here)
	}
	m = press(m, "esc") // the composer first
	m = press(m, "esc")
	if m.here.kind != placeMate || m.here.mate != "t1" {
		t.Fatalf("esc from the thread: %+v", m.here)
	}
	if m = press(m, "esc"); m.here.kind != placeHome {
		t.Fatalf("esc from Iris's home: %+v", m.here)
	}
	if m = press(m, "esc"); m.here.kind != placeHome {
		t.Fatalf("esc on Home goes nowhere: %+v", m.here)
	}
}

// alt+z shows the place's terminal on the whole screen, a resize keeps it there, and alt+z
// again brings the frame back.
func TestFullScreenSurvivesAResize(t *testing.T) {
	m := openSession(t, withSessions(sampleApp()), "sb1", "claude")
	alt := tea.KeyPressMsg{Code: 'z', Mod: tea.ModAlt}
	next, _ := m.Update(alt)
	m = next.(*app)
	if !m.zoom || m.layoutFor(m.width) != (layout{0, m.width, 0}) {
		t.Fatalf("alt+z: zoom %v, layout %+v", m.zoom, m.layoutFor(m.width))
	}
	next, _ = m.Update(tea.WindowSizeMsg{Width: 140, Height: 40})
	m = next.(*app)
	if !m.zoom || m.layoutFor(140) != (layout{0, 140, 0}) {
		t.Fatalf("a resize ends full screen: zoom %v", m.zoom)
	}
	next, _ = m.Update(alt)
	if m = next.(*app); m.zoom || m.layoutFor(140).sidebar != sidebarW {
		t.Fatalf("alt+z again: zoom %v, layout %+v", m.zoom, m.layoutFor(140))
	}
}

// m shows the teammate's next computer, in the order the Computers screen lists them, and says
// so when there is only one.
func TestMCyclesTheTeammatesComputers(t *testing.T) {
	m := withSessions(sampleApp())
	m.open(place{kind: placeMate, mate: "t1"})
	m = settle(m)
	var order []string
	for _, sb := range m.visible() {
		if sb.TeammateID == "t1" {
			order = append(order, sb.ID)
		}
	}
	if len(order) != 2 || m.computerOn() == nil || m.computerOn().ID != order[0] {
		t.Fatalf("Iris's home shows %+v of %v", m.computerOn(), order)
	}
	m = press(m, "m")
	if m.computerOn().ID != order[1] || m.here.sandbox != order[1] || !strings.Contains(m.notice.text, "docs-site") {
		t.Fatalf("m: on %s, here %+v, notice %q", m.computerOn().ID, m.here, m.notice.text)
	}
	if m = press(m, "m"); m.computerOn().ID != order[0] {
		t.Fatalf("m again: on %s", m.computerOn().ID)
	}

	m.open(place{kind: placeMate, mate: "t2"})
	m = press(settle(m), "m")
	if m.computerOn().ID != "sb2" || m.notice.text != "Only one computer to show." {
		t.Fatalf("m on Finn's home: on %s, notice %q", m.computerOn().ID, m.notice.text)
	}
}

// ? lists the keys of the place, or the screen, it was pressed on, under its title.
func TestHelpListsThePlacesKeys(t *testing.T) {
	m := withSessions(sampleApp())
	m.open(place{kind: placeMate, mate: "t1"})
	mate := settle(m)
	for _, c := range []struct {
		name, title, key string
		m                *app
	}{
		{"home", "Home", "pgup/pgdn", withSessions(sampleApp())},
		{"thread", "Thread", "p / enter", press(openSession(t, withSessions(sampleApp()), "sb1", "Fix the login form"), "esc")},
		{"mate", "Teammate", "give the teammate another computer", mate},
		{"computer", "Computer", "1-5", openComputer(withSessions(sampleApp()), "sb2", tabDetails)},
		{"checkpoints", "Checkpoints", "save a checkpoint", func() *app { m := sampleApp(); m.screen = screenCheckpoints; return m }()},
		{"computers", "Computers", "a new computer", func() *app { m := sampleApp(); m.screen = screenComputers; return m }()},
	} {
		m := press(c.m, "?")
		if m.screen != screenHelp {
			t.Fatalf("%s: ? opens %v", c.name, m.screen)
		}
		out := strings.Join(m.helpBody(), "\n")
		if !strings.Contains(out, c.title) || !strings.Contains(out, c.key) {
			t.Errorf("%s: help lacks %q or %q:\n%s", c.name, c.title, c.key, out)
		}
	}
}
