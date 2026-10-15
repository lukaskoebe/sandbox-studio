package tui

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/lukaskoebe/sandbox-studio/internal/api"
	"github.com/lukaskoebe/sandbox-studio/internal/resources"
	"github.com/lukaskoebe/sandbox-studio/internal/sandboxes"
	"github.com/lukaskoebe/sandbox-studio/internal/store"
)

// sampleNew opens the form of a new sandbox in the sample app, with Iris and Finn to choose
// from and two templates.
func sampleNew() *app {
	m := sampleApp()
	m.team = []store.Teammate{{ID: "t1", Name: "Iris"}, {ID: "t2", Name: "Finn"}}
	m = press(m, "n")
	m.newSb.loaded = true
	m.newSb.templates = []api.TemplateView{
		{ID: "tp1", Name: "node-22+python-3.13@2", Resources: resources.Resources{CPUs: 2, MemoryMiB: 4096}},
		{ID: "tp2", Name: "go@2", Resources: resources.Resources{CPUs: 4, MemoryMiB: 8192}},
	}
	return m
}

// creating is a sandbox whose create pulls its image.
func creating(m *app) sandboxes.View {
	return sandboxes.View{
		Sandbox: store.Sandbox{ID: "sb9", Name: "login-fix", TeammateID: "t1"}, Status: "creating", TemplateName: "node-22+python-3.13@2",
		Progress: &sandboxes.Progress{
			Operation: "create", State: "running", Step: "pull",
			Steps: []string{"pull", "vm", "boot", "services", "agent", "configure"},
			Pull:  &sandboxes.PullProgress{Layers: 7, LayersDone: 3, Bytes: 312 << 20, TotalBytes: 840 << 20},
		},
	}
}

func TestGoldenNewTeammate(t *testing.T) {
	golden(t, "new_teammate", sampleNew())
}

func TestGoldenNewTemplate(t *testing.T) {
	golden(t, "new_template", press(sampleNew(), "enter", "j"))
}

func TestGoldenNewName(t *testing.T) {
	golden(t, "new_name", press(sampleNew(), "enter", "j", "enter", "l", "o", "g", "i", "n"))
}

func TestGoldenNewProgress(t *testing.T) {
	m := press(sampleNew(), "enter", "j", "enter", "l", "o", "g", "i", "n", "-", "f", "i", "x")
	m = run(m, func() tea.Msg { return sandboxCreated{sb: creating(m)} })
	m.sandboxes = append(m.sandboxes, creating(m))
	golden(t, "new_progress", m)
}

func TestGoldenNewFailed(t *testing.T) {
	m := sampleNew()
	sb := creating(m)
	sb.Status = "failed"
	sb.Progress.State, sb.Progress.Step, sb.Progress.Pull = "failed", "boot", nil
	sb.Progress.Error = "the guest did not answer within 2m0s"
	m.screen, m.newSb.created = screenProgress, &sb
	m.sandboxes = append(m.sandboxes, sb)
	golden(t, "new_failed", m)
}

func TestNewSandboxChoosesTeammateAndTemplate(t *testing.T) {
	m := press(sampleNew(), "j", "enter", "j", "j", "enter")
	if m.newSb.step != stepName || m.newTeammate().ID != "t2" || m.newTemplate().ID != "tp2" {
		t.Fatalf("step %d, teammate %q, template %q", m.newSb.step, m.newTeammate().ID, m.newTemplate().ID)
	}
	m = press(m, "esc", "k", "k", "enter")
	if m.newTemplate().ID != "" {
		t.Fatalf("the first row is the base image: %q", m.newTemplate().ID)
	}
	m = press(m, "esc", "esc", "j", "j", "enter")
	if m.newTeammate().ID != "" || m.newSb.step != stepTemplate {
		t.Fatalf("the last row is no teammate: %q, step %d", m.newTeammate().ID, m.newSb.step)
	}
	if m = press(m, "esc", "esc"); m.screen != screenHome {
		t.Fatalf("esc from the first step returns to %v", m.screen)
	}
}

func TestNewSandboxNameIsCheckedAndLowercase(t *testing.T) {
	m := press(sampleNew(), "enter", "enter", "A", "b", "-")
	if got := m.newSb.name.value(); got != "ab-" {
		t.Fatalf("name %q", got)
	}
	next, cmd := m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	m = next.(*app)
	if cmd != nil || !m.notice.failed || m.newSb.creating {
		t.Fatalf("a name ending in a dash: cmd %v, notice %+v", cmd != nil, m.notice)
	}
	m = press(m, "backspace")
	next, cmd = m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	m = next.(*app)
	if cmd == nil || !m.newSb.creating {
		t.Fatal("a valid name does not create")
	}
	next, _ = m.Update(sandboxCreated{err: errors.New("a sandbox named ab exists")})
	m = next.(*app)
	if m.screen != screenNew || m.newSb.creating || !strings.Contains(m.notice.text, "ab exists") {
		t.Fatalf("a refused create: screen %v, notice %q", m.screen, m.notice.text)
	}
}

func TestProgressOpensTheSandboxOnceReady(t *testing.T) {
	m := sampleNew()
	sb := creating(m)
	m.newSb.step = stepName
	m.newSb.name.set(sb.Name)
	next, cmd := m.Update(sandboxCreated{sb: sb})
	m = next.(*app)
	if m.screen != screenProgress || cmd == nil {
		t.Fatalf("created: screen %v, refresh %v", m.screen, cmd != nil)
	}
	list := append([]sandboxes.View{}, m.sandboxes...)
	next, _ = m.Update(sandboxesLoaded{env: "env1", list: append(list, sb)})
	m = next.(*app)
	if m.screen != screenProgress {
		t.Fatalf("still creating: %v", m.screen)
	}
	started := m.now
	sb.Status, sb.Progress, sb.Agent, sb.StartedAt = "running", nil, &sandboxes.AgentInfo{}, &started
	next, _ = m.Update(sandboxesLoaded{env: "env1", list: append(list, sb)})
	m = next.(*app)
	if m.screen != screenDetail || m.selected().ID != "sb9" {
		t.Fatalf("ready: screen %v, selected %+v", m.screen, m.selected())
	}
}

func TestProgressReturnsHomeWhenTheSandboxIsDeleted(t *testing.T) {
	m := sampleNew()
	sb := creating(m)
	sb.Status, sb.Progress.State = "failed", "failed"
	m.screen, m.newSb.created = screenProgress, &sb
	list := append([]sandboxes.View{}, m.sandboxes...)
	next, _ := m.Update(sandboxesLoaded{env: "env1", list: append(list, sb)})
	m = press(next.(*app), "d")
	if m.confirm == nil || m.confirm.ID != "sb9" {
		t.Fatalf("d on a failed create asks to delete it: %+v", m.confirm)
	}
	m.confirm = nil
	next, _ = m.Update(sandboxesLoaded{env: "env1", list: list})
	if m = next.(*app); m.screen != screenHome {
		t.Fatalf("deleted: screen %v", m.screen)
	}
}

func TestBytesText(t *testing.T) {
	for n, want := range map[int64]string{512: "512 B", 2048: "2.0 KiB", 312 << 20: "312.0 MiB", 3 << 30: "3.0 GiB"} {
		if got := bytesText(n); got != want {
			t.Errorf("%d: %q, want %q", n, got, want)
		}
	}
}

func TestNewSandboxCreatesOnTheServer(t *testing.T) {
	ctx := context.Background()
	s := startStudio(t)
	s.VMs.createErr = errors.New("no hypervisor")
	m := newApp(ctx, connect(t, s), s.Addr)
	m.env = s.Env
	m = run(m, m.loadSandboxes())
	next, cmd := m.Update(tea.KeyPressMsg{Code: 'n', Text: "n"})
	m = run(next.(*app), cmd)
	if m.screen != screenNew || !m.newSb.loaded || m.newSb.step != stepTemplate {
		t.Fatalf("form: screen %v, loaded %v, step %d (no teammates skip to the template)", m.screen, m.newSb.loaded, m.newSb.step)
	}
	m = press(m, "enter", "b", "l", "u", "b")
	next, cmd = m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	m = run(next.(*app), cmd)
	if m.screen != screenProgress || m.newSb.created == nil {
		t.Fatalf("create: screen %v, notice %q", m.screen, m.notice.text)
	}
	// The create goes on in the background, where the fake runtime refuses the VM: the list
	// has the sandbox, failed, with the step and the error; d deletes it and Home is back.
	deadline := time.Now().Add(20 * time.Second)
	for {
		m = run(m, m.loadSandboxes())
		if sb, _, _ := m.progressSandbox(); phaseOf(sb) == phaseFailed || time.Now().After(deadline) {
			break
		}
	}
	sb, listed, _ := m.progressSandbox()
	if !listed || m.screen != screenProgress || sb.Name != "blub" || phaseOf(sb) != phaseFailed || sb.Progress == nil {
		t.Fatalf("progress: listed %v, screen %v, sandbox %s %s %+v", listed, m.screen, sb.Name, sb.Status, sb.Progress)
	}
	if body := strings.Join(m.progressBody(80, 19), "\n"); !strings.Contains(body, "Starting the VM") || !strings.Contains(body, "no hypervisor") {
		t.Fatalf("progress body:\n%s", body)
	}
	m = press(m, "d")
	next, cmd = m.Update(tea.KeyPressMsg{Code: 'y', Text: "y"})
	m = run(next.(*app), cmd)
	m = run(m, m.loadSandboxes())
	if m.screen != screenHome || len(m.sandboxes) != 0 {
		t.Fatalf("deleted: screen %v, %d sandboxes, notice %q", m.screen, len(m.sandboxes), m.notice.text)
	}
}
