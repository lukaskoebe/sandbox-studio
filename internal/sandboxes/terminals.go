package sandboxes

import (
	"cmp"
	"context"
	"errors"
	"path"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/lukaskoebe/sandbox-studio/internal/agentproto"
	"github.com/lukaskoebe/sandbox-studio/internal/events"
)

// ErrTerminalTitle is returned for a name that can't name a terminal.
var ErrTerminalTitle = errors.New("invalid terminal name")

// TerminalState is what a terminal does, for a teammate's status.
type TerminalState string

// Terminal states. A harness counts as working while it prints, so one waiting at its
// prompt is idle; any other program is busy, so a dev server that runs for hours doesn't
// look like work.
const (
	TerminalWorking TerminalState = "working"
	TerminalBusy    TerminalState = "busy"
	TerminalIdle    TerminalState = "idle"
)

// Terminal is a terminal session as the UI shows it: what the guest reports, with a label
// and what it does.
type Terminal struct {
	agentproto.Session
	Label string `json:"label" doc:"The session's name in the UI: the name the user gave it, the harness that runs, the program in the foreground or the base name of its directory, made unique with \" 2\""`
	// Program is what runs, for a status line such as "npm run dev running".
	Program string        `json:"program,omitempty" doc:"The harness or the foreground program, cut to 24 characters; empty at a shell prompt"`
	State   TerminalState `json:"state" enum:"working,busy,idle" doc:"working: an agent harness runs and printed in the last 20 s; busy: another program than the shell runs; idle otherwise"`
}

// SandboxTerminals are the terminals of one sandbox.
type SandboxTerminals struct {
	SandboxID string     `json:"sandboxId"`
	Terminals []Terminal `json:"terminals"`
}

// workingWindow is how recently a harness must have printed to count as working.
const workingWindow = 20 * time.Second

// labelLength is the most characters of a command a label or program shows.
const labelLength = 24

// harnessPrograms are the agent harnesses people start in a terminal, by program name.
var harnessPrograms = map[string]bool{"claude": true, "codex": true, "opencode": true,
	"cursor-agent": true, "gemini": true, "aider": true}

// interpreters run a script named by their first argument, which names the program better:
// "node /usr/local/bin/codex" is codex.
var interpreters = map[string]bool{"node": true, "bun": true, "deno": true, "python": true,
	"python3": true, "ruby": true, "perl": true}

// describeTerminals labels the sessions of a sandbox and says what each does, at now. A
// label two sessions share gets " 2", " 3" in the order they were created.
func describeTerminals(sessions []agentproto.Session, now time.Time) []Terminal {
	out := make([]Terminal, len(sessions))
	for i, s := range sessions {
		out[i] = describeTerminal(s, now)
	}
	order := make([]int, len(out))
	for i := range order {
		order[i] = i
	}
	slices.SortStableFunc(order, func(a, b int) int {
		return cmp.Or(cmp.Compare(out[a].Created, out[b].Created), strings.Compare(out[a].Name, out[b].Name))
	})
	seen := map[string]int{}
	for _, i := range order {
		seen[out[i].Label]++
		if n := seen[out[i].Label]; n > 1 {
			out[i].Label += " " + strconv.Itoa(n)
		}
	}
	return out
}

func describeTerminal(s agentproto.Session, now time.Time) Terminal {
	t := Terminal{Session: s, State: TerminalIdle}
	command := commandLine(s.Foreground)
	harness := s.Harness
	if harness == "" {
		if name := programName(command); harnessPrograms[name] {
			harness = name
		} else if harnessPrograms[s.Command] {
			harness = s.Command
		}
	}
	switch {
	case harness != "":
		t.Program = harness
		if s.Activity > 0 && now.Sub(time.Unix(s.Activity, 0)) < workingWindow {
			t.State = TerminalWorking
		}
	case command != "":
		t.Program = cut(command, labelLength)
		t.State = TerminalBusy
	}
	switch {
	case s.Title != "":
		t.Label = s.Title
	case t.Program != "":
		t.Label = t.Program
	case s.Path != "":
		t.Label = directoryLabel(s.Path)
	default:
		t.Label = s.Name
	}
	return t
}

// commandLine is a foreground command line as a label shows it: the program's base name
// and its arguments, or the script's for an interpreter running one.
func commandLine(args []string) string {
	if len(args) == 0 {
		return ""
	}
	if len(args) > 1 && interpreters[path.Base(args[0])] && !strings.HasPrefix(args[1], "-") {
		args = args[1:]
	}
	return strings.Join(append([]string{path.Base(args[0])}, args[1:]...), " ")
}

// programName is the first word of a command line.
func programName(command string) string {
	name, _, _ := strings.Cut(command, " ")
	return name
}

// directoryLabel names a shell by its directory: its base name, or ~ in a home directory.
func directoryLabel(dir string) string {
	dir = path.Clean(dir)
	if dir == "/" {
		return "/"
	}
	if parent := path.Dir(dir); parent == "/home" || dir == "/root" {
		return "~"
	}
	return path.Base(dir)
}

// cut shortens s to n characters, ending it with "…" when it was longer.
func cut(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return strings.TrimRight(string(r[:n-1]), " ") + "…"
}

// terminalWatch keeps what the terminals of each running sandbox do, so a teammate's status
// can count them without asking every guest per request.
type terminalWatch struct {
	mu   sync.Mutex
	last map[string]watchedTerminals // sandbox ID →
}

type watchedTerminals struct {
	env  string
	at   time.Time
	list []Terminal
}

// terminalsFresh is how long an environment's terminal list answers from what was last
// seen. The watcher looks every terminalsEvery while a UI tab is open.
const (
	terminalsEvery = 10 * time.Second
	terminalsFresh = 15 * time.Second
	terminalsAsk   = 5 * time.Second // the longest a guest gets to answer the watcher
)

// summary is what a change of publishes a terminals event: the labels, programs and states,
// not the times, which change whenever a program prints.
func summary(list []Terminal) string {
	var b strings.Builder
	for _, t := range list {
		b.WriteString(t.Name + "\x00" + t.Label + "\x00" + t.Program + "\x00" + string(t.State) + "\x00" + t.Path + "\x01")
	}
	return b.String()
}

// noteTerminals keeps list as what the terminals of sandbox id do, and announces a change.
func (m *Manager) noteTerminals(envID, id string, list []Terminal, at time.Time) {
	w := &m.terminals
	w.mu.Lock()
	if w.last == nil {
		w.last = map[string]watchedTerminals{}
	}
	prev, had := w.last[id]
	w.last[id] = watchedTerminals{env: envID, at: at, list: list}
	w.mu.Unlock()
	if !had || summary(prev.list) != summary(list) {
		m.publishTerminals(envID, id)
	}
}

// forgetTerminals drops what was seen of sandbox id, whose guest is gone.
func (m *Manager) forgetTerminals(id string) {
	w := &m.terminals
	w.mu.Lock()
	prev, had := w.last[id]
	delete(w.last, id)
	w.mu.Unlock()
	if had && len(prev.list) > 0 {
		m.publishTerminals(prev.env, id)
	}
}

func (m *Manager) publishTerminals(envID, id string) {
	if m.Bus != nil {
		m.Bus.Publish(events.Event{Topic: events.TopicTerminals, EnvironmentID: envID, SandboxID: id})
	}
}

// sessions asks the guest of sandbox id for its terminals and keeps the answer.
func (m *Manager) sessions(ctx context.Context, envID, id string) ([]Terminal, error) {
	list, err := m.Hub.Sessions(ctx, id)
	if err != nil {
		return nil, err
	}
	now := time.Now()
	out := describeTerminals(list, now)
	m.noteTerminals(envID, id, out, now)
	return out, nil
}

// EnvironmentTerminals returns the terminals of the environment's running sandboxes. It
// answers from what the watcher saw in the last terminalsFresh and asks the other guests.
func (m *Manager) EnvironmentTerminals(ctx context.Context, envID string) ([]SandboxTerminals, error) {
	recs, err := m.Store.Sandboxes(ctx, envID)
	if err != nil {
		return nil, err
	}
	out := []SandboxTerminals{}
	var wg sync.WaitGroup
	var mu sync.Mutex
	now := time.Now()
	for _, rec := range recs {
		if !Public(rec) {
			continue
		}
		if _, ok := m.Hub.Connected(rec.ID); !ok {
			continue
		}
		m.terminals.mu.Lock()
		seen, ok := m.terminals.last[rec.ID]
		m.terminals.mu.Unlock()
		if ok && now.Sub(seen.at) < terminalsFresh {
			out = append(out, SandboxTerminals{SandboxID: rec.ID, Terminals: seen.list})
			continue
		}
		wg.Add(1)
		go func() {
			defer wg.Done()
			ctx, cancel := context.WithTimeout(ctx, terminalsAsk)
			defer cancel()
			list, err := m.sessions(ctx, envID, rec.ID)
			if err != nil {
				return // a guest that doesn't answer has no terminals to show
			}
			mu.Lock()
			out = append(out, SandboxTerminals{SandboxID: rec.ID, Terminals: list})
			mu.Unlock()
		}()
	}
	wg.Wait()
	slices.SortFunc(out, func(a, b SandboxTerminals) int { return strings.Compare(a.SandboxID, b.SandboxID) })
	return out, nil
}

// WatchTerminals asks the guests of running sandboxes what their terminals run every
// terminalsEvery while a UI tab is open, and publishes events.TopicTerminals on a change.
func (m *Manager) WatchTerminals(ctx context.Context) {
	if m.Bus == nil {
		return
	}
	tick := time.NewTicker(terminalsEvery)
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
		}
		if m.Bus.Watched() {
			m.watchTerminals(ctx)
		}
	}
}

func (m *Manager) watchTerminals(ctx context.Context) {
	envs, err := m.Store.Environments(ctx)
	if err != nil {
		return
	}
	connected := map[string]bool{}
	for _, id := range m.Hub.ConnectedIDs() {
		connected[id] = true
	}
	watched := map[string]bool{}
	var wg sync.WaitGroup
	for _, env := range envs {
		recs, err := m.Store.Sandboxes(ctx, env.ID)
		if err != nil {
			continue
		}
		for _, rec := range recs {
			if !Public(rec) || !connected[rec.ID] {
				continue
			}
			watched[rec.ID] = true
			wg.Add(1)
			go func() {
				defer wg.Done()
				ctx, cancel := context.WithTimeout(ctx, terminalsAsk)
				defer cancel()
				_, _ = m.sessions(ctx, env.ID, rec.ID)
			}()
		}
	}
	wg.Wait()
	m.terminals.mu.Lock()
	var gone []string
	for id := range m.terminals.last {
		if !watched[id] {
			gone = append(gone, id)
		}
	}
	m.terminals.mu.Unlock()
	for _, id := range gone {
		m.forgetTerminals(id)
	}
}
