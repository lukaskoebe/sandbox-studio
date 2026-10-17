package tui

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/lukaskoebe/sandbox-studio/internal/events"
	"github.com/lukaskoebe/sandbox-studio/internal/runtime"
	"github.com/lukaskoebe/sandbox-studio/internal/store"
)

// connect signs a client in to the test Studio, and ends its session with the test.
func connect(t *testing.T, s *testStudio) *Client {
	t.Helper()
	c, err := Connect(context.Background(), s.Addr, s.Control)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { c.Close(context.Background()) })
	return c
}

func TestHomeLoadsFromTheServer(t *testing.T) {
	ctx := context.Background()
	s := startStudio(t)
	s.addSandbox(t, "blub", runtime.StatusStopped)
	s.addSandbox(t, "api-refactor", runtime.StatusRunning) // running, but its agent isn't connected
	m := newApp(ctx, connect(t, s), s.Addr)
	m = run(m, m.loadEnvs())
	m = run(m, m.loadSandboxes())
	m = run(m, m.loadApprovals())
	if m.env.ID != s.Env.ID || !m.loaded || len(m.visible()) != 2 {
		t.Fatalf("home: env %q, loaded %v, %d sandboxes", m.env.ID, m.loaded, len(m.visible()))
	}
	phases := map[string]phase{}
	for _, sb := range m.visible() {
		phases[sb.Name] = phaseOf(sb)
	}
	if phases["blub"] != phaseStopped || phases["api-refactor"] != phaseBooting {
		t.Fatalf("phases: %v", phases)
	}
}

func TestStopAndDeleteGoThroughTheServer(t *testing.T) {
	ctx := context.Background()
	s := startStudio(t)
	sb := s.addSandbox(t, "blub", runtime.StatusRunning)
	c := connect(t, s)
	view, err := c.Lifecycle(ctx, s.Env.ID, sb.ID, "stop")
	if err != nil || string(view.Status) != "stopped" {
		t.Fatalf("stop: %+v, %v", view, err)
	}

	m := newApp(ctx, c, s.Addr)
	m.env = s.Env
	m = run(m, m.loadSandboxes())
	m.cursor = 0
	m = run(m, m.remove(*m.selected()))
	if m.notice.text != "Deleted blub" || m.notice.failed {
		t.Fatalf("delete notice: %+v", m.notice)
	}
	m = run(m, m.loadSandboxes())
	if len(m.visible()) != 0 {
		t.Fatalf("deleted sandbox still listed: %d", len(m.visible()))
	}
}

func TestChangesReachTheEventStream(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	s := startStudio(t)
	sb := s.addSandbox(t, "blub", runtime.StatusRunning)
	c := connect(t, s)
	ch, err := c.Subscribe(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := c.Lifecycle(ctx, s.Env.ID, sb.ID, "stop"); err != nil {
		t.Fatal(err)
	}
	select {
	case ev := <-ch:
		if ev.Topic != events.TopicSandboxes || ev.ID != sb.ID {
			t.Fatalf("event: %+v", ev)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("no event for the stop")
	}
}

func TestPendingApprovalsOfEveryEnvironment(t *testing.T) {
	ctx := context.Background()
	s := startStudio(t)
	sb := s.addSandbox(t, "blub", runtime.StatusRunning)
	payload, _ := json.Marshal(map[string]any{"host": "api.github.com", "port": 443, "sandboxName": "blub", "patterns": []string{"api.github.com"}})
	if _, _, err := s.Store.RequestApproval(ctx, store.Approval{
		EnvironmentID: s.Env.ID, SandboxID: sb.ID, Kind: "network", Subject: "api.github.com:443", Payload: payload,
	}); err != nil {
		t.Fatal(err)
	}
	list, err := connect(t, s).PendingApprovals(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 1 || list[0].SandboxID != sb.ID || list[0].Network == nil || list[0].Network.Host != "api.github.com" {
		t.Fatalf("approvals: %+v", list)
	}
}

func TestSuspendGoesThroughTheServer(t *testing.T) {
	ctx := context.Background()
	s := startStudio(t)
	sb := s.addSandbox(t, "blub", runtime.StatusRunning)
	view, err := connect(t, s).Lifecycle(ctx, s.Env.ID, sb.ID, "suspend")
	if err != nil || string(view.Status) != "suspended" {
		t.Fatalf("suspend: %+v, %v", view, err)
	}
}
