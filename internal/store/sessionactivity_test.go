package store

import (
	"context"
	"testing"
	"time"
)

// Home's pulse: chats' tool calls and browser actions come from their logs, terminals' output
// and previews' requests from what Studio counted, each by minute; the flags are the questions
// asked, the requests blocked, the pushes and the commands that failed.
func TestSessionActivityAndFlags(t *testing.T) {
	ctx := context.Background()
	s, err := Open(ctx, ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	env, err := s.CreateEnvironment(ctx, "work")
	if err != nil {
		t.Fatal(err)
	}
	t0 := int64(1_800_000_000) // a whole minute
	exec := func(q string, args ...any) {
		t.Helper()
		if _, err := s.db.ExecContext(ctx, q, args...); err != nil {
			t.Fatalf("%s: %v", q, err)
		}
	}
	// A sandbox named sb, so that the rows below can name it.
	sb, err := s.CreateSandbox(ctx, Sandbox{EnvironmentID: env.ID, Name: "box", CPUs: 1, MemoryMiB: 512, WorkspaceMiB: 1024, DockerMiB: 1024})
	if err != nil {
		t.Fatal(err)
	}
	exec("PRAGMA foreign_keys = OFF")
	exec("UPDATE sandboxes SET id = 'sb' WHERE id = ?", sb.ID)
	exec(`INSERT INTO chats (id, environment_id, sandbox_id, teammate_id, harness, created_at, updated_at) VALUES ('c1', ?, 'sb', 'ada', 'claude', ?, ?)`, env.ID, t0, t0)
	exec(`INSERT INTO chat_turns (id, chat_id, seq, started_at, ended_at) VALUES ('t1', 'c1', 1, ?, ?)`, t0, t0+300)
	item := func(id string, seq int, status, body string, at int64) {
		exec(`INSERT INTO chat_items (id, chat_id, turn_id, seq, kind, status, body, updated_at) VALUES (?, 'c1', 't1', ?, 'tool', ?, ?, ?)`, id, seq, status, body, at)
	}
	item("i1", 1, "completed", `{"kind":"read","title":"Read a.go"}`, t0+5)
	item("i2", 2, "completed", `{"kind":"edit","title":"Edit a.go"}`, t0+50)
	item("i3", 3, "failed", `{"kind":"execute","title":"go test ./..."}`, t0+70)
	item("i4", 4, "failed", `{"kind":"read","title":"Read gone.go"}`, t0+80)
	exec(`INSERT INTO chat_items (id, chat_id, turn_id, seq, kind, approval_id, body, updated_at) VALUES ('p1', 'c1', 't1', 5, 'permission', 'a1', '{}', ?)`, t0+90)
	exec(`INSERT INTO browser_actions (environment_id, teammate_id, session_id, actor, action, outcome, at) VALUES (?, 'ada', 's', 'agent', 'click', 'ok', ?), (?, 'ada', 's', 'agent', 'open', 'denied', ?)`,
		env.ID, t0+100, env.ID, t0+110)
	if err := s.AddSessionActivity(ctx, []ActivityCount{
		{EnvironmentID: env.ID, SandboxID: "sb", Kind: SpanPreview, Key: "8080", Minute: t0 / 60, Count: 3, Errors: 1},
		{EnvironmentID: env.ID, SandboxID: "sb", Kind: SpanPreview, Key: "8080", Minute: t0 / 60, Count: 2},
		{EnvironmentID: env.ID, SandboxID: "sb", Kind: SpanTerminal, Key: "main@1", Minute: t0/60 + 2, Count: 4},
	}); err != nil {
		t.Fatal(err)
	}
	approval := func(id, kind, subject, status, payload string, created int64, decided any) {
		exec(`INSERT INTO approvals (id, environment_id, sandbox_id, kind, subject, payload, status, attempts, created_at, updated_at, decided_at) VALUES (?, ?, 'sb', ?, ?, ?, ?, 2, ?, ?, ?)`,
			id, env.ID, kind, subject, payload, status, created, created, decided)
	}
	approval("a1", "chat.permission", "chat/c1/p1", StatusApproved, `{"title":"rm -rf node_modules"}`, t0+90, t0+150)
	approval("a2", "network.request", "evil.example:443", StatusDenied, `{"host":"evil.example"}`, t0+10, t0+200)
	approval("a3", "network.request", "tracker ads.example:443", StatusDenied, `{"host":"ads.example"}`, t0+10, t0+200)
	approval("a4", "network.request", "ok.example:443", StatusApproved, `{"host":"ok.example","decided":"open"}`, t0+10, t0+10)
	approval("a5", "git.push", "push main", StatusPending, "{}", t0+250, nil)
	exec(`INSERT INTO git_pushes (id, environment_id, sandbox_id, forge_id, forge_name, owner, repo, ref, old_sha, new_sha, approval_id, state, created_at, updated_at) VALUES ('g1', ?, 'sb', 'f', 'GitHub', 'o', 'api', 'refs/heads/passkeys', '0', '1', 'a5', 'pushed', ?, ?)`,
		env.ID, t0+260, t0+270)

	since, until := time.Unix(t0-60, 0), time.Unix(t0+600, 0)
	acts, err := s.SessionActivityIn(ctx, env.ID, since, until)
	if err != nil {
		t.Fatal(err)
	}
	type key struct {
		kind, key string
		at        int64
	}
	got := map[key][2]int{}
	for _, a := range acts {
		got[key{a.Kind, a.Key, a.At.Unix()}] = [2]int{a.Count, a.Errors}
	}
	want := map[key][2]int{
		{"chat", "c1", t0}:               {2, 0},
		{"chat", "c1", t0 + 60}:          {2, 2},
		{"browser", "ada", t0 + 60}:      {2, 1},
		{"preview", "8080", t0}:          {5, 1},
		{"terminal", "main@1", t0 + 120}: {4, 0},
	}
	if len(got) != len(want) {
		t.Errorf("activity %v, want %v", got, want)
	}
	for k, w := range want {
		if got[k] != w {
			t.Errorf("%v = %v, want %v", k, got[k], w)
		}
	}

	flags, err := s.SessionFlags(ctx, env.ID, since, until)
	if err != nil {
		t.Fatal(err)
	}
	var kinds []string
	for _, f := range flags {
		kinds = append(kinds, f.Kind+":"+f.Label)
		switch f.Label {
		case "rm -rf node_modules":
			if f.ChatID != "c1" || f.Until == nil || f.Until.Unix() != t0+150 || f.State != StatusApproved {
				t.Errorf("permission %+v", f)
			}
		case "evil.example":
			if f.At.Unix() != t0+200 || f.Count != 2 {
				t.Errorf("blocked %+v", f)
			}
		case "push main":
			if f.Until != nil || f.State != StatusPending {
				t.Errorf("waiting push %+v", f)
			}
		}
	}
	wantKinds := "[failed:go test ./... asked:rm -rf node_modules blocked:evil.example asked:push main push:passkeys → api]"
	if got := fmtList(kinds); got != wantKinds {
		t.Errorf("flags %s, want %s", got, wantKinds)
	}

	if err := s.PruneSessionActivity(ctx, time.Unix(t0+120, 0)); err != nil {
		t.Fatal(err)
	}
	acts, _ = s.SessionActivityIn(ctx, env.ID, since, until)
	for _, a := range acts {
		if a.Kind == "preview" {
			t.Errorf("pruned minute is back: %+v", a)
		}
	}
}

func fmtList(s []string) string {
	out := "["
	for i, v := range s {
		if i > 0 {
			out += " "
		}
		out += v
	}
	return out + "]"
}
