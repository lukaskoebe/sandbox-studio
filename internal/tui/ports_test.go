package tui

import (
	"context"
	"strings"
	"testing"

	"github.com/lukaskoebe/sandbox-studio/internal/api"
	"github.com/lukaskoebe/sandbox-studio/internal/runtime"
)

// The detail of a running sandbox reads its ports, lists them as links, and stops reading once
// it is left.
func TestDetailListsThePorts(t *testing.T) {
	m := sampleApp()
	m, cmd := pressCmd(m, "enter")
	if cmd == nil || !m.ports.busy || m.ports.sandbox != "sb1" {
		t.Fatalf("enter reads no ports: %+v", m.ports)
	}
	if again := m.watchPorts(); again != nil {
		t.Fatal("a second read started while one was under way")
	}
	next, tick := m.Update(portsLoaded{sandbox: "sb1", list: []api.PreviewPort{{Port: 3000, URL: "http://3000-sb1.localhost:7878/"}}})
	m = next.(*app)
	if tick == nil {
		t.Fatal("no next read")
	}
	view := m.View().Content
	if !strings.Contains(stripANSI(view), "Ports      :3000  http://3000-sb1.localhost:7878/") {
		t.Fatalf("ports:\n%s", stripANSI(view))
	}
	if !strings.Contains(view, "\x1b]8;;http://3000-sb1.localhost:7878/") {
		t.Fatal("the preview address is no link")
	}

	m = press(m, "esc")
	next, cmd = m.Update(portsTick{sandbox: "sb1"})
	m = next.(*app)
	if cmd != nil || m.ports.busy {
		t.Fatalf("reads go on off the detail: busy %v", m.ports.busy)
	}
}

func TestPortLinesSayWhyThereAreNone(t *testing.T) {
	m := sampleApp()
	if got := stripANSI(strings.Join(m.portLines("sb3", false), "\n")); !strings.Contains(got, "listed while the sandbox runs") {
		t.Fatalf("stopped: %q", got)
	}
	m.ports = portsState{sandbox: "sb1", loaded: true}
	if got := stripANSI(strings.Join(m.portLines("sb1", true), "\n")); !strings.Contains(got, "none open") {
		t.Fatalf("none: %q", got)
	}
	for i := range maxPorts + 2 {
		m.ports.list = append(m.ports.list, api.PreviewPort{Port: 3000 + i, URL: "http://x/"})
	}
	lines := m.portLines("sb1", true)
	if len(lines) != maxPorts+1 || !strings.Contains(stripANSI(lines[maxPorts]), "and 2 more") {
		t.Fatalf("many: %q", stripANSI(strings.Join(lines, "\n")))
	}
}

// Studio lists the ports of a sandbox whose agent is connected; the tests' agents never are.
func TestPortsComeFromTheServer(t *testing.T) {
	s := startStudio(t)
	sb := s.addSandbox(t, "blub", runtime.StatusRunning)
	_, err := connect(t, s).Ports(context.Background(), s.Env.ID, sb.ID)
	if err == nil || !strings.Contains(err.Error(), "not running or still booting") {
		t.Fatalf("ports: %v", err)
	}
}
