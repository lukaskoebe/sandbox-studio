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
	"github.com/lukaskoebe/sandbox-studio/internal/runtime"
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

// f asks for the fork's name, starting from the sandbox's; enter forks it and the progress
// screen follows the fork.
func TestForkAsksForANameAndFollowsTheFork(t *testing.T) {
	client, rec := recordingClient(t)
	rec.answers["POST /api/environments/env1/sandboxes/sb1/fork"] = `{"id":"sb9","name":"blub-fork","status":"creating"}`
	m := sampleApp()
	m.client = client
	m = press(m, "f")
	if m.typing == nil || m.typing.text.value() != "blub-fork" {
		t.Fatalf("f asks nothing: %+v", m.typing)
	}
	if bar := stripANSI(m.keyBar()); !strings.HasPrefix(bar, "enter fork") {
		t.Fatalf("bar: %q", bar)
	}
	m = press(m, "backspace", "backspace", "backspace", "backspace", "backspace", "_", "enter")
	if m.typing == nil || !strings.Contains(stripANSI(m.noticeLine(120)), "lowercase letters, digits and dashes") {
		t.Fatalf("a bad name was taken: %q", stripANSI(m.noticeLine(120)))
	}
	m = press(m, "backspace", "-", "T", "w", "o")
	if got := m.typing.text.value(); got != "blub-two" {
		t.Fatalf("name: %q", got)
	}
	m = pressRun(t, m, "enter")
	if got := rec.last(t); got.path != "/api/environments/env1/sandboxes/sb1/fork" || got.body != `{"name":"blub-two"}` {
		t.Fatalf("fork: %+v", got)
	}
	if m.screen != screenProgress || m.newSb.created == nil || m.newSb.created.ID != "sb9" {
		t.Fatalf("progress: screen %v, %+v", m.screen, m.newSb.created)
	}
}

func TestForkNameStaysAName(t *testing.T) {
	if got := forkName(strings.Repeat("a", 34) + "-b-c"); got != strings.Repeat("a", 34)+"-fork" {
		t.Fatalf("long: %q", got)
	}
	m := sampleApp()
	m.cursor = 3 // demo, which is still being created
	m.sandboxes[3].Status = "creating"
	if m = press(m, "f"); m.typing != nil || !m.notice.failed {
		t.Fatalf("forked while creating: %+v", m.notice)
	}
	m = press(sampleApp(), "f", "esc")
	if m.typing != nil || m.screen != screenHome {
		t.Fatalf("esc: typing %v, screen %v", m.typing != nil, m.screen)
	}
}

// Studio's answer to a fork reaches the notice line; the runtime of the tests can't copy disks.
func TestForkGoesThroughTheServer(t *testing.T) {
	ctx := context.Background()
	s := startStudio(t)
	s.addSandbox(t, "blub", runtime.StatusStopped)
	m := newApp(ctx, connect(t, s), s.Addr)
	m.env = s.Env
	m = run(m, m.loadSandboxes())
	m = press(m, "f")
	m = pressRun(t, m, "enter")
	if !m.notice.failed || !strings.Contains(m.notice.text, "fork blub: this runtime cannot copy workspaces") {
		t.Fatalf("fork: %+v", m.notice)
	}
}
