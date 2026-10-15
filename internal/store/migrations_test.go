package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"io/fs"
	"slices"
	"sort"
	"strings"
	"testing"
)

// The pre-018 schema with personas: everything a persona owned must survive the rename to
// teammates, including memory scopes and the persona keys of stored JSON.
func TestMigrateTeammates(t *testing.T) {
	ctx := context.Background()
	db, err := sql.Open("sqlite", "file::memory:?_pragma=foreign_keys(1)")
	if err != nil {
		t.Fatal(err)
	}
	db.SetMaxOpenConns(1)
	s := &Store{db: db}
	t.Cleanup(func() { s.Close() })
	if err := s.migrateTo(ctx, 17); err != nil {
		t.Fatal(err)
	}
	for _, q := range []string{
		`INSERT INTO environments (id, name, created_at) VALUES ('env', 'Default', 1)`,
		`INSERT INTO environments (id, name, created_at) VALUES ('other', 'Other', 1)`,
		`INSERT INTO providers (id, environment_id, name, kind, created_at, updated_at) VALUES ('prov', 'env', 'Claude', 'claude_subscription', 1, 1)`,
		`INSERT INTO personas (id, environment_id, name, role, soul, harness, provider_id, git_name, git_email, created_at, updated_at)
		 VALUES ('p1', 'env', 'Ada', 'reviewer', 'Be brief.', 'claude', 'prov', 'Ada', 'ada@example.com', 1, 1)`,
		`INSERT INTO sandboxes (id, environment_id, name, cpus, memory_mib, workspace_mib, docker_mib, created_at, persona_id) VALUES ('sb1', 'env', 'api', 1, 512, 1024, 0, 1, 'p1')`,
		`INSERT INTO sandboxes (id, environment_id, name, cpus, memory_mib, workspace_mib, docker_mib, created_at, persona_id, kind) VALUES ('br1', 'env', 'browser-p1', 1, 512, 1024, 0, 1, 'p1', 'browser')`,
		`INSERT INTO rules (id, environment_id, host, ports, action, created_at, persona_id) VALUES ('r1', 'env', 'example.com', '443', 'allow', 1, 'p1')`,
		`INSERT INTO approvals (id, environment_id, sandbox_id, kind, subject, payload, created_at, updated_at) VALUES
		 ('a1', 'env', 'sb1', 'browser.action', 's1', '{"personaId":"p1","personaName":"Ada","sandboxId":"sb1","action":"click"}', 1, 1),
		 ('a2', 'env', 'sb1', 'browser.credential', 's2', '{"personaId":"p1","personaName":"Ada","credential":"GH"}', 1, 1),
		 ('a3', 'env', 'sb1', 'memory.share', 's3', '{"personaId":"p1","personaName":"Ada","text":"t","entityIds":[]}', 1, 1),
		 ('a4', 'env', 'sb1', 'git.push', 's4', '{"persona":"Ada","sandbox":"api","commits":[]}', 1, 1),
		 ('a5', 'env', NULL, 'memory.conflict', 's5', '{"conflictId":"c1","factA":{"id":"f1","scope":"persona:p1"},"factB":{"id":"f2","scope":"shared"}}', 1, 1),
		 ('a6', 'env', 'sb1', 'network', 's6', '{"host":"example.com","port":443}', 1, 1)`,
		`INSERT INTO memory_sources (id, environment_id, scope, kind, author_persona, created_at) VALUES ('s1', 'env', 'persona:p1', 'user', 'p1', 1)`,
		`INSERT INTO memory_facts (id, environment_id, scope, kind, text, observed_at, tier, author_persona, created_at, updated_at) VALUES
		 ('f1', 'env', 'persona:p1', 'fact', 'Ada deploys on Fridays', 1, 'user', 'p1', 1, 1),
		 ('f2', 'env', 'shared', 'fact', 'Deploys happen on Mondays', 1, 'user', '', 1, 1)`,
		`INSERT INTO memory_fact_sources (fact_id, source_id) VALUES ('f1', 's1')`,
		`INSERT INTO memory_pages (id, environment_id, scope, slug, title, kind, compiled, always_load, tier, author_persona, created_at, updated_at)
		 VALUES ('pg1', 'env', 'persona:p1', 'self', 'Self', 'persona-self', 'I prefer small commits.', 1, 'user', 'p1', 1, 1)`,
		`INSERT INTO memory_timeline (id, environment_id, page_id, at, text, fact_id, source_id, author_persona, created_at) VALUES ('t1', 'env', 'pg1', 1, 'joined', 'f1', 's1', 'p1', 1)`,
		`INSERT INTO memory_chunks (environment_id, scope, fact_id, text) VALUES ('env', 'persona:p1', 'f1', 'Ada deploys on Fridays')`,
		`INSERT INTO memory_chunks (environment_id, scope, page_id, title, text) VALUES ('env', 'persona:p1', 'pg1', 'Self', 'I prefer small commits.')`,
		`INSERT INTO memory_chunks (environment_id, scope, timeline_id, text) VALUES ('env', 'persona:p1', 't1', 'joined')`,
		`INSERT INTO memory_conflicts (id, environment_id, fact_a, fact_b, verdict, created_at, approval_id) VALUES ('c1', 'env', 'f1', 'f2', 'contradiction', 1, 'a5')`,
		`INSERT INTO memory_dream_runs (id, environment_id, scope, trigger, status, window_start, window_end, started_at, stats)
		 VALUES ('d1', 'env', 'shared', 'manual', 'done', 1, 2, 1, '{"facts":2,"model":"haiku","persona":"p1"}'),
		        ('d2', 'env', 'persona:p1', 'manual', 'running', 1, 2, 1, '{"facts":1}')`,
		`INSERT INTO memory_agent_sessions (id, environment_id, persona_id, sandbox_id, harness, session_ref, started_at, last_event_at) VALUES ('ms1', 'env', 'p1', 'sb1', 'claude', 'ref', 1, 1)`,
		`INSERT INTO memory_session_log (environment_id, session_id, persona_id, sandbox_id, at, kind, summary) VALUES ('env', 'ms1', 'p1', 'sb1', 1, 'context', 'pack')`,
		`INSERT INTO secrets (id, environment_id, name, value, hosts, placeholder, created_at, updated_at, studio_only) VALUES ('sec', 'env', 'FORGE', x'00', '', 'ph', 1, 1, 1)`,
		`INSERT INTO forges (id, environment_id, name, kind, base_url, secret_id, created_at, updated_at) VALUES ('fg', 'env', 'gh', 'github', 'https://github.com', 'sec', 1, 1)`,
		`INSERT INTO git_pushes (id, environment_id, sandbox_id, persona_name, forge_id, forge_name, owner, repo, ref, old_sha, new_sha, approval_id, state, created_at, updated_at)
		 VALUES ('gp1', 'env', 'sb1', 'Ada', 'fg', 'gh', 'acme', 'api', 'refs/heads/x', '0', '1', 'a4', 'pending', 1, 1)`,
		`INSERT INTO browser_patterns (id, environment_id, persona_id, action, origin, verdict, created_at) VALUES ('bp1', 'env', 'p1', 'click', 'https://example.com', 'allow', 1)`,
		`INSERT INTO browser_actions (environment_id, persona_id, session_id, actor, action, outcome, at) VALUES ('env', 'p1', 'bs1', 'agent', 'open', 'ok', 1)`,
	} {
		if _, err := db.ExecContext(ctx, q); err != nil {
			t.Fatalf("%s: %v", q, err)
		}
	}

	if err := s.migrateTo(ctx, 0); err != nil {
		t.Fatal(err)
	}

	// Nothing in the schema names personas any more.
	var leftover []string
	rows, err := db.QueryContext(ctx, "SELECT name FROM sqlite_schema WHERE name LIKE '%persona%' OR sql LIKE '%persona%'")
	if err != nil {
		t.Fatal(err)
	}
	for rows.Next() {
		var n string
		if err := rows.Scan(&n); err != nil {
			t.Fatal(err)
		}
		leftover = append(leftover, n)
	}
	rows.Close()
	if len(leftover) > 0 {
		t.Fatalf("schema still names personas: %v", leftover)
	}
	var fkOn int
	if err := db.QueryRowContext(ctx, "PRAGMA foreign_keys").Scan(&fkOn); err != nil || fkOn != 1 {
		t.Fatalf("foreign keys after migrating: %d %v", fkOn, err)
	}

	// The store reads everything through its teammate names.
	tm, err := s.Teammate(ctx, "env", "p1")
	if err != nil || tm.Name != "Ada" || tm.Soul != "Be brief." || tm.ProviderID != "prov" {
		t.Fatalf("teammate %+v %v", tm, err)
	}
	if sb, err := s.Sandbox(ctx, "env", "sb1"); err != nil || sb.TeammateID != "p1" {
		t.Fatalf("sandbox %+v %v", sb, err)
	}
	if br, err := s.BrowserSandbox(ctx, "env", "p1"); err != nil || br.ID != "br1" {
		t.Fatalf("browser %+v %v", br, err)
	}
	if rules, err := s.Rules(ctx, "env"); err != nil || len(rules) != 1 || rules[0].TeammateID != "p1" {
		t.Fatalf("rules %+v %v", rules, err)
	}
	if ps, err := s.BrowserPatterns(ctx, "env", "p1"); err != nil || len(ps) != 1 || ps[0].TeammateID != "p1" {
		t.Fatalf("patterns %+v %v", ps, err)
	}
	if as, err := s.BrowserActions(ctx, "env", "p1", "", 10); err != nil || len(as) != 1 {
		t.Fatalf("actions %+v %v", as, err)
	}
	if gp, err := s.GitPush(ctx, "env", "gp1"); err != nil || gp.TeammateName != "Ada" {
		t.Fatalf("push %+v %v", gp, err)
	}

	// Approval payloads use the new keys and keep everything else.
	payload := func(id string) map[string]any {
		t.Helper()
		a, err := s.Approval(ctx, "env", id)
		if err != nil {
			t.Fatal(err)
		}
		var m map[string]any
		if err := json.Unmarshal(a.Payload, &m); err != nil {
			t.Fatal(err)
		}
		if strings.Contains(string(a.Payload), "persona") {
			t.Errorf("approval %s still names a persona: %s", id, a.Payload)
		}
		return m
	}
	for _, id := range []string{"a1", "a2", "a3"} {
		if m := payload(id); m["teammateId"] != "p1" || m["teammateName"] != "Ada" {
			t.Errorf("approval %s: %v", id, m)
		}
	}
	if m := payload("a1"); m["action"] != "click" || m["sandboxId"] != "sb1" {
		t.Errorf("browser.action lost fields: %v", m)
	}
	if m := payload("a4"); m["teammate"] != "Ada" || m["sandbox"] != "api" {
		t.Errorf("git.push: %v", m)
	}
	if m := payload("a5"); m["factA"].(map[string]any)["scope"] != "teammate:p1" || m["factB"].(map[string]any)["scope"] != "shared" {
		t.Errorf("memory.conflict: %v", m)
	}
	if m := payload("a6"); m["host"] != "example.com" {
		t.Errorf("network: %v", m)
	}

	// Memory: scopes, authors, the page kind, links and the search index.
	one := func(q string, want any) {
		t.Helper()
		var got any
		if err := db.QueryRowContext(ctx, q).Scan(&got); err != nil {
			t.Fatalf("%s: %v", q, err)
		}
		if b, ok := got.([]byte); ok {
			got = string(b)
		}
		if got != want {
			t.Errorf("%s = %v, want %v", q, got, want)
		}
	}
	one("SELECT scope || ' ' || author_teammate FROM memory_sources WHERE id = 's1'", "teammate:p1 p1")
	one("SELECT scope || ' ' || author_teammate FROM memory_facts WHERE id = 'f1'", "teammate:p1 p1")
	one("SELECT scope || ' ' || kind || ' ' || author_teammate || ' ' || compiled FROM memory_pages WHERE id = 'pg1'", "teammate:p1 teammate-self p1 I prefer small commits.")
	one("SELECT page_id || ' ' || author_teammate FROM memory_timeline WHERE id = 't1'", "pg1 p1")
	one("SELECT COUNT(*) FROM memory_chunks WHERE scope = 'teammate:p1'", int64(3))
	one("SELECT COUNT(*) FROM memory_fact_sources", int64(1))
	one("SELECT c.page_id FROM memory_fts f JOIN memory_chunks c ON c.id = f.rowid WHERE memory_fts MATCH 'commits'", "pg1")
	one("SELECT json_extract(stats, '$.teammate') || ' ' || json_extract(stats, '$.model') FROM memory_dream_runs WHERE id = 'd1'", "p1 haiku")
	one("SELECT scope FROM memory_dream_runs WHERE id = 'd2'", "teammate:p1")
	one("SELECT teammate_id FROM memory_agent_sessions WHERE id = 'ms1'", "p1")
	one("SELECT teammate_id FROM memory_session_log WHERE session_id = 'ms1'", "p1")

	// Constraints and triggers work under their new names.
	if _, err := db.ExecContext(ctx, `INSERT INTO memory_pages (id, environment_id, scope, slug, title, kind, tier, created_at, updated_at) VALUES ('pg2', 'env', 'shared', 'x', 'X', 'persona-self', 'user', 1, 1)`); err == nil {
		t.Error("the old page kind is still allowed")
	}
	if _, err := db.ExecContext(ctx, `UPDATE sandboxes SET teammate_id = NULL WHERE id = 'sb1'`); err == nil || !strings.Contains(err.Error(), "sandbox teammate is immutable") {
		t.Errorf("changing the owner: %v", err)
	}
	if _, err := db.ExecContext(ctx, `INSERT INTO rules (id, environment_id, host, ports, action, created_at, teammate_id) VALUES ('r2', 'other', 'x', '', 'allow', 1, 'p1')`); err == nil {
		t.Error("a rule took a teammate of another environment")
	}
	if _, err := db.ExecContext(ctx, `DELETE FROM memory_pages WHERE id = 'pg1'`); err != nil {
		t.Fatal(err)
	}
	one("SELECT COUNT(*) FROM memory_timeline", int64(0))
}

// Forges from before SSH agent auth keep their secrets; their ssh forges sign with a key,
// and the studio-only check still guards every secret a forge holds.
func TestMigrateForgeAgent(t *testing.T) {
	ctx := context.Background()
	db, err := sql.Open("sqlite", "file::memory:?_pragma=foreign_keys(1)")
	if err != nil {
		t.Fatal(err)
	}
	db.SetMaxOpenConns(1)
	s := &Store{db: db}
	t.Cleanup(func() { s.Close() })
	if err := s.migrateTo(ctx, 20); err != nil {
		t.Fatal(err)
	}
	for _, q := range []string{
		`INSERT INTO environments (id, name, created_at) VALUES ('env', 'Default', 1)`,
		`INSERT INTO secrets (id, environment_id, name, value, hosts, placeholder, created_at, updated_at, studio_only) VALUES ('s1', 'env', 'GH', x'00', '', 'ph1', 1, 1, 1), ('s2', 'env', 'BOX', x'00', '', 'ph2', 1, 1, 1)`,
		`INSERT INTO forges (id, environment_id, name, kind, base_url, username, secret_id, public_key, host_key, created_at, updated_at)
		 VALUES ('f1', 'env', 'github', 'github', 'https://github.com', '', 's1', '', '', 1, 1),
		        ('f2', 'env', 'box', 'ssh', 'ssh://git@box.example.net', 'git', 's2', 'ssh-ed25519 AAAA studio', 'ssh-ed25519 BBBB', 1, 1)`,
	} {
		if _, err := db.ExecContext(ctx, q); err != nil {
			t.Fatalf("%s: %v", q, err)
		}
	}
	if err := s.migrateTo(ctx, 0); err != nil {
		t.Fatal(err)
	}

	var auth, secret, key string
	if err := db.QueryRowContext(ctx, "SELECT auth, secret_id, public_key || ' ' || host_key FROM forges WHERE id = 'f2'").Scan(&auth, &secret, &key); err != nil {
		t.Fatal(err)
	}
	if auth != "key" || secret != "s2" || key != "ssh-ed25519 AAAA studio ssh-ed25519 BBBB" {
		t.Fatalf("ssh forge after migrating: %q %q %q", auth, secret, key)
	}
	if err := db.QueryRowContext(ctx, "SELECT auth, secret_id FROM forges WHERE id = 'f1'").Scan(&auth, &secret); err != nil {
		t.Fatal(err)
	}
	if auth != "" || secret != "s1" {
		t.Fatalf("token forge after migrating: %q %q", auth, secret)
	}

	// A plain secret can't be a forge's key, an agent forge has no secret, and token kinds
	// have no auth.
	if _, err := db.ExecContext(ctx, `INSERT INTO secrets (id, environment_id, name, value, hosts, placeholder, created_at, updated_at) VALUES ('plain', 'env', 'PLAIN', x'00', '', 'ph3', 1, 1)`); err != nil {
		t.Fatal(err)
	}
	for _, q := range []string{
		`INSERT INTO forges (id, environment_id, name, kind, base_url, auth, secret_id, created_at, updated_at) VALUES ('x1', 'env', 'x1', 'ssh', 'ssh://git@x1.example.net', 'key', 'plain', 1, 1)`,
		`INSERT INTO forges (id, environment_id, name, kind, base_url, auth, secret_id, created_at, updated_at) VALUES ('x2', 'env', 'x2', 'ssh', 'ssh://git@x2.example.net', 'agent', 's1', 1, 1)`,
		`INSERT INTO forges (id, environment_id, name, kind, base_url, auth, created_at, updated_at) VALUES ('x3', 'env', 'x3', 'github', 'https://github.com', 'agent', 1, 1)`,
	} {
		if _, err := db.ExecContext(ctx, q); err == nil {
			t.Errorf("accepted %s", q)
		}
	}
	if _, err := db.ExecContext(ctx, `INSERT INTO forges (id, environment_id, name, kind, base_url, auth, created_at, updated_at) VALUES ('x4', 'env', 'x4', 'ssh', 'ssh://git@x4.example.net', 'agent', 1, 1)`); err != nil {
		t.Fatalf("agent forge without a secret: %v", err)
	}
}

// Secrets created before scopes keep their values, placeholders and owners when the table
// is rebuilt, and the providers' and forges' triggers still find them.
func TestMigrateSecretScopes(t *testing.T) {
	ctx := context.Background()
	db, err := sql.Open("sqlite", "file::memory:?_pragma=foreign_keys(1)")
	if err != nil {
		t.Fatal(err)
	}
	db.SetMaxOpenConns(1)
	s := &Store{db: db}
	t.Cleanup(func() { s.Close() })
	if err := s.migrateTo(ctx, 20); err != nil {
		t.Fatal(err)
	}
	for _, q := range []string{
		`INSERT INTO environments (id, name, created_at) VALUES ('env', 'Default', 1)`,
		`INSERT INTO secrets (id, environment_id, name, value, hosts, placeholder, note, created_at, updated_at) VALUES ('key', 'env', 'PROVIDER_A_KEY', x'01', 'api.anthropic.com', 'studio-key', 'n', 1, 2)`,
		`INSERT INTO secrets (id, environment_id, name, value, hosts, placeholder, created_at, updated_at, studio_only) VALUES ('tok', 'env', 'FORGE_GH_TOKEN', x'02', 'github.com', 'studio-tok', 1, 1, 1)`,
		`INSERT INTO providers (id, environment_id, name, kind, secret_id, created_at, updated_at) VALUES ('prov', 'env', 'A', 'anthropic_api', 'key', 1, 1)`,
		`INSERT INTO forges (id, environment_id, name, kind, base_url, secret_id, created_at, updated_at) VALUES ('fg', 'env', 'gh', 'github', 'https://github.com', 'tok', 1, 1)`,
	} {
		if _, err := db.ExecContext(ctx, q); err != nil {
			t.Fatalf("%s: %v", q, err)
		}
	}
	if err := s.migrateTo(ctx, 0); err != nil {
		t.Fatal(err)
	}
	list, err := s.Secrets(ctx, "env")
	if err != nil || len(list) != 2 {
		t.Fatalf("secrets: %+v %v", list, err)
	}
	if k := list[1]; k.ID != "key" || string(k.Sealed) != "\x01" || k.Placeholder != "studio-key" || k.Note != "n" || k.UpdatedAt.Unix() != 2 || k.TeammateID != "" || k.SandboxID != "" {
		t.Fatalf("provider key: %+v", k)
	}
	if f := list[0]; f.ID != "tok" || !f.StudioOnly {
		t.Fatalf("forge token: %+v", f)
	}
	if p, err := s.SecretProvider(ctx, "env", "key"); err != nil || p != "A" {
		t.Fatalf("provider of the key: %q %v", p, err)
	}
	// The triggers naming secrets still work.
	if _, err := db.ExecContext(ctx, `INSERT INTO forges (id, environment_id, name, kind, base_url, secret_id, created_at, updated_at) VALUES ('fg2', 'env', 'gh2', 'github', 'https://github.com', 'key', 1, 1)`); err == nil {
		t.Fatal("a forge took a secret that isn't studio-only")
	}
	var leftover int
	if err := db.QueryRowContext(ctx, "SELECT COUNT(*) FROM sqlite_schema WHERE name = 'secrets_old'").Scan(&leftover); err != nil || leftover != 0 {
		t.Fatalf("secrets_old left behind: %d %v", leftover, err)
	}
}

// Sandboxes and checkpoints from before lineage get a create event each, and a checkpoint
// event for each ready checkpoint. Build and browser VMs get none.
func TestMigrateSandboxLineage(t *testing.T) {
	ctx := context.Background()
	db, err := sql.Open("sqlite", "file::memory:?_pragma=foreign_keys(1)")
	if err != nil {
		t.Fatal(err)
	}
	db.SetMaxOpenConns(1)
	s := &Store{db: db}
	t.Cleanup(func() { s.Close() })
	if err := s.migrateTo(ctx, 22); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, `INSERT INTO environments (id, name, created_at) VALUES ('env', 'Default', 1)`); err != nil {
		t.Fatal(err)
	}
	tpl := createReadyTemplate(t, ctx, s, "env", "1")
	for _, q := range []string{
		`INSERT INTO sandboxes (id, environment_id, name, cpus, memory_mib, workspace_mib, docker_mib, created_at, template_id) VALUES ('a', 'env', 'api', 1, 512, 1024, 0, 10, '` + tpl.ID + `')`,
		`INSERT INTO sandboxes (id, environment_id, name, cpus, memory_mib, workspace_mib, docker_mib, created_at, generation) VALUES ('b', 'env', 'web', 1, 512, 1024, 0, 20, 2)`,
		`INSERT INTO sandboxes (id, environment_id, name, cpus, memory_mib, workspace_mib, docker_mib, created_at, kind) VALUES ('br', 'env', 'browser-x', 1, 512, 1024, 0, 15, 'browser')`,
		`INSERT INTO checkpoints (id, environment_id, sandbox_id, name, state, generation, created_at) VALUES ('cp1', 'env', 'a', 'snap', 'ready', 1, 30), ('cp2', 'env', 'a', 'pending', 'creating', 1, 31)`,
	} {
		if _, err := db.ExecContext(ctx, q); err != nil {
			t.Fatalf("%s: %v", q, err)
		}
	}
	if err := s.migrateTo(ctx, 0); err != nil {
		t.Fatal(err)
	}

	rows, err := db.QueryContext(ctx, "SELECT sandbox_id, kind, generation, name, template_id, checkpoint_name FROM sandbox_events ORDER BY id")
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for rows.Next() {
		var id, kind, name, template, checkpoint string
		var generation int
		if err := rows.Scan(&id, &kind, &generation, &name, &template, &checkpoint); err != nil {
			t.Fatal(err)
		}
		got = append(got, fmt.Sprint(id, kind, generation, name, template, checkpoint))
	}
	rows.Close()
	want := []string{
		fmt.Sprint("a", "create", 1, "api", tpl.ID, ""),
		fmt.Sprint("b", "create", 1, "web", "", ""),
		fmt.Sprint("a", "checkpoint", 1, "api", "", "snap"),
	}
	if fmt.Sprint(got) != fmt.Sprint(want) {
		t.Fatalf("backfilled events = %v, want %v", got, want)
	}
	events, err := s.SandboxEvents(ctx, "env", "a")
	if err != nil || len(events) != 2 || events[0].Kind != EventCreate || events[1].CheckpointID != "cp1" {
		t.Fatalf("events of a = %+v, %v", events, err)
	}
	if sb, err := s.Sandbox(ctx, "env", "a"); err != nil || sb.ForkedFromID != "" || !sb.ForkedAt.IsZero() {
		t.Fatalf("sandbox after migrating = %+v, %v", sb, err)
	}
}

// Rules from before rule hygiene were made by hand or through the API, so they are manual.
func TestMigrateRuleOrigin(t *testing.T) {
	ctx := context.Background()
	db, err := sql.Open("sqlite", "file::memory:?_pragma=foreign_keys(1)")
	if err != nil {
		t.Fatal(err)
	}
	db.SetMaxOpenConns(1)
	s := &Store{db: db}
	t.Cleanup(func() { s.Close() })
	if err := s.migrateTo(ctx, 24); err != nil {
		t.Fatal(err)
	}
	for _, q := range []string{
		`INSERT INTO environments (id, name, created_at) VALUES ('env', 'Default', 1)`,
		`INSERT INTO sandboxes (id, environment_id, name, cpus, memory_mib, workspace_mib, docker_mib, created_at) VALUES ('sb1', 'env', 'api', 1, 512, 1024, 0, 1)`,
		`INSERT INTO rules (id, environment_id, host, ports, action, created_at) VALUES ('r1', 'env', 'example.com', '443', 'allow', 1)`,
		`INSERT INTO rules (id, environment_id, sandbox_id, host, ports, action, created_at) VALUES ('r2', 'env', 'sb1', 'example.org', '', 'deny', 2)`,
	} {
		if _, err := db.ExecContext(ctx, q); err != nil {
			t.Fatalf("%s: %v", q, err)
		}
	}
	if err := s.migrateTo(ctx, 0); err != nil {
		t.Fatal(err)
	}

	for _, id := range []string{"r1", "r2"} {
		r, err := s.Rule(ctx, "env", id)
		if err != nil || r.Origin != OriginManual {
			t.Fatalf("rule %s after migrating: %+v, %v", id, r, err)
		}
	}
	if _, err := db.ExecContext(ctx, `INSERT INTO rules (id, environment_id, host, ports, action, created_at, origin) VALUES ('r3', 'env', 'x.example', '', 'allow', 1, 'someone')`); err == nil {
		t.Error("an unknown origin was accepted")
	}
}

// Migration 026 adds the teammate settings and rebuilds teammates so a provider can be left
// out. Teammates, their sandboxes, secrets and rules must come through with their triggers.
func TestMigrateTeammateSettings(t *testing.T) {
	ctx := context.Background()
	db, err := sql.Open("sqlite", "file::memory:?_pragma=foreign_keys(1)")
	if err != nil {
		t.Fatal(err)
	}
	db.SetMaxOpenConns(1)
	s := &Store{db: db}
	t.Cleanup(func() { s.Close() })
	if err := s.migrateTo(ctx, 25); err != nil {
		t.Fatal(err)
	}
	for _, q := range []string{
		`INSERT INTO environments (id, name, created_at) VALUES ('env', 'Work', 1), ('other', 'Other', 1)`,
		`INSERT INTO providers (id, environment_id, name, kind, created_at, updated_at) VALUES ('prov', 'env', 'Claude', 'claude_subscription', 1, 1)`,
		`INSERT INTO teammates (id, environment_id, name, role, soul, harness, provider_id, model, git_name, git_email, created_at, updated_at)
		 VALUES ('ada', 'env', 'Ada', 'reviewer', 'Be brief.', 'claude', 'prov', '', 'Ada', 'ada@example.com', 1, 1)`,
		`INSERT INTO sandboxes (id, environment_id, name, cpus, memory_mib, workspace_mib, docker_mib, created_at, teammate_id) VALUES ('sb1', 'env', 'api', 1, 512, 1024, 0, 1, 'ada')`,
		`INSERT INTO secrets (id, environment_id, teammate_id, name, value, hosts, placeholder, created_at, updated_at) VALUES ('sec', 'env', 'ada', 'TOKEN', x'00', 'api.example.com', 'ph', 1, 1)`,
		`INSERT INTO rules (id, environment_id, teammate_id, host, ports, action, created_at) VALUES ('r1', 'env', 'ada', 'example.com', '443', 'allow', 1)`,
	} {
		if _, err := db.ExecContext(ctx, q); err != nil {
			t.Fatalf("%s: %v", q, err)
		}
	}
	if err := s.migrateTo(ctx, 0); err != nil {
		t.Fatal(err)
	}

	tm, err := s.Teammate(ctx, "env", "ada")
	if err != nil || tm.ProviderID != "prov" || tm.Soul != "Be brief." || tm.Avatar != "" || tm.ApprovalScope != "environment" || tm.ApprovalPattern != "" {
		t.Fatalf("migrated teammate %+v %v", tm, err)
	}
	if sb, err := s.Sandbox(ctx, "env", "sb1"); err != nil || sb.TeammateID != "ada" {
		t.Fatalf("sandbox %+v %v", sb, err)
	}
	if sec, err := s.SecretByID(ctx, "env", "sec"); err != nil || sec.TeammateID != "ada" {
		t.Fatalf("secret %+v %v", sec, err)
	}
	if rules, err := s.Rules(ctx, "env"); err != nil || len(rules) != 1 || rules[0].TeammateID != "ada" {
		t.Fatalf("rules %+v %v", rules, err)
	}

	// The triggers still guard the new table: a sandbox can't take a teammate of another
	// environment, and an approval scope outside its list is refused.
	if _, err := db.ExecContext(ctx, `INSERT INTO sandboxes (id, environment_id, name, cpus, memory_mib, workspace_mib, docker_mib, created_at, teammate_id) VALUES ('sb2', 'other', 'x', 1, 512, 1024, 0, 1, 'ada')`); err == nil {
		t.Error("a sandbox took a teammate of another environment")
	}
	if _, err := db.ExecContext(ctx, `UPDATE teammates SET approval_scope = 'everywhere' WHERE id = 'ada'`); err == nil {
		t.Error("an unknown approval scope was stored")
	}
	// A teammate without a provider is allowed.
	if _, err := db.ExecContext(ctx, `INSERT INTO teammates (id, environment_id, name, harness, git_name, git_email, created_at, updated_at)
		VALUES ('none', 'env', 'Bob', 'claude', 'Bob', 'bob@example.com', 1, 1)`); err != nil {
		t.Fatalf("teammate without a provider: %v", err)
	}
	// An API-key provider may wait for its key, but a key of another environment is refused,
	// and a subscription never holds one.
	if _, err := db.ExecContext(ctx, `INSERT INTO providers (id, environment_id, name, kind, created_at, updated_at) VALUES ('keyless', 'env', 'Anthropic', 'anthropic_api', 1, 1)`); err != nil {
		t.Fatalf("API provider without a key: %v", err)
	}
	if _, err := db.ExecContext(ctx, `INSERT INTO secrets (id, environment_id, name, value, hosts, placeholder, created_at, updated_at) VALUES ('other-key', 'other', 'K', x'00', 'api.anthropic.com', 'ph-other', 1, 1)`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, `UPDATE providers SET secret_id = 'other-key' WHERE id = 'keyless'`); err == nil {
		t.Error("a provider took a key of another environment")
	}
	if _, err := db.ExecContext(ctx, `INSERT INTO secrets (id, environment_id, name, value, hosts, placeholder, created_at, updated_at) VALUES ('sub-key', 'env', 'S', x'00', 'x.example.com', 'ph-sub', 1, 1)`); err != nil {
		t.Fatal(err)
	}
	// Migration 033 lets a subscription hold its login's key (docs/teammates.md).
	if _, err := db.ExecContext(ctx, `UPDATE providers SET secret_id = 'sub-key' WHERE id = 'prov'`); err != nil {
		t.Errorf("a subscription login could not keep its key: %v", err)
	}
}

// Migration 029 accepts the context_dependent verdict and keeps the verdicts cached before it.
func TestMigrateMemoryConsolidation(t *testing.T) {
	ctx := context.Background()
	db, err := sql.Open("sqlite", "file::memory:?_pragma=foreign_keys(1)")
	if err != nil {
		t.Fatal(err)
	}
	db.SetMaxOpenConns(1)
	s := &Store{db: db}
	t.Cleanup(func() { s.Close() })
	if err := s.migrateTo(ctx, 26); err != nil {
		t.Fatal(err)
	}
	for _, q := range []string{
		`INSERT INTO environments (id, name, created_at) VALUES ('env', 'Default', 1)`,
		`INSERT INTO memory_judgments (environment_id, pair_hash, model, prompt_version, verdict, reason, created_at)
		 VALUES ('env', 'h1', 'm', 1, 'contradiction', 'why', 1)`,
	} {
		if _, err := db.ExecContext(ctx, q); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.migrateTo(ctx, 0); err != nil {
		t.Fatal(err)
	}
	var verdict, contextA string
	if err := db.QueryRowContext(ctx, "SELECT verdict, context_a FROM memory_judgments WHERE pair_hash = 'h1'").Scan(&verdict, &contextA); err != nil || verdict != "contradiction" || contextA != "" {
		t.Fatalf("cached verdict %q %q: %v", verdict, contextA, err)
	}
	if _, err := db.ExecContext(ctx, `INSERT INTO memory_judgments (environment_id, pair_hash, model, prompt_version, verdict, reason, context_a, created_at)
		VALUES ('env', 'h2', 'm', 2, 'context_dependent', '', 'renart', 1)`); err != nil {
		t.Fatalf("context verdict refused: %v", err)
	}
	if _, err := db.ExecContext(ctx, `INSERT INTO memory_judgments (environment_id, pair_hash, model, prompt_version, verdict, reason, created_at)
		VALUES ('env', 'h3', 'm', 2, 'bogus', '', 1)`); err == nil {
		t.Fatal("unknown verdict accepted")
	}
}

// Migration 030 makes existing environments locked, which is what Studio did before levels, and
// new ones guarded. Existing approvals are not reverted.
func TestMigrateSecurityLevels(t *testing.T) {
	ctx := context.Background()
	db, err := sql.Open("sqlite", "file::memory:?_pragma=foreign_keys(1)")
	if err != nil {
		t.Fatal(err)
	}
	db.SetMaxOpenConns(1)
	s := &Store{db: db}
	t.Cleanup(func() { s.Close() })
	if err := s.migrateTo(ctx, 29); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, `INSERT INTO environments (id, name, created_at) VALUES ('env', 'Default', 1)`); err != nil {
		t.Fatal(err)
	}
	if err := s.migrateTo(ctx, 0); err != nil {
		t.Fatal(err)
	}
	var level, off string
	if err := db.QueryRowContext(ctx, "SELECT security_level, curated_off FROM environments WHERE id = 'env'").Scan(&level, &off); err != nil || level != LevelLocked || off != "[]" {
		t.Fatalf("existing environment: level %q, curated %q: %v", level, off, err)
	}
	if _, err := db.ExecContext(ctx, `INSERT INTO environments (id, name, created_at) VALUES ('new', 'New', 1)`); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRowContext(ctx, "SELECT security_level FROM environments WHERE id = 'new'").Scan(&level); err != nil || level != LevelGuarded {
		t.Fatalf("new environment: level %q: %v", level, err)
	}
	if _, err := db.ExecContext(ctx, `UPDATE environments SET security_level = 'paranoid' WHERE id = 'env'`); err == nil {
		t.Error("an unknown level accepted")
	}
}

// Migration 031 accepts the gitlab forge kind and keeps every forge's columns.
func TestMigrateGitLabForgeKind(t *testing.T) {
	ctx := context.Background()
	db, err := sql.Open("sqlite", "file::memory:?_pragma=foreign_keys(1)")
	if err != nil {
		t.Fatal(err)
	}
	db.SetMaxOpenConns(1)
	s := &Store{db: db}
	t.Cleanup(func() { s.Close() })
	if err := s.migrateTo(ctx, 30); err != nil {
		t.Fatal(err)
	}
	for _, q := range []string{
		`INSERT INTO environments (id, name, created_at) VALUES ('env', 'Default', 1)`,
		`INSERT INTO secrets (id, environment_id, name, value, hosts, placeholder, created_at, updated_at, studio_only) VALUES ('sec', 'env', 'FORGE', x'00', '', 'ph', 1, 1, 1), ('sec2', 'env', 'GL', x'00', '', 'ph2', 1, 1, 1)`,
		`INSERT INTO forges (id, environment_id, name, kind, base_url, secret_id, agent_slow_at, agent_hint_dismissed, created_at, updated_at)
		 VALUES ('fg', 'env', 'gh', 'github', 'https://github.com', 'sec', 5, 1, 1, 1)`,
	} {
		if _, err := db.ExecContext(ctx, q); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.migrateTo(ctx, 0); err != nil {
		t.Fatal(err)
	}
	var slow int64
	var dismissed bool
	if err := db.QueryRowContext(ctx, "SELECT agent_slow_at, agent_hint_dismissed FROM forges WHERE id = 'fg'").Scan(&slow, &dismissed); err != nil || slow != 5 || !dismissed {
		t.Fatalf("forge after the rebuild: %d %v: %v", slow, dismissed, err)
	}
	if _, err := db.ExecContext(ctx, `INSERT INTO forges (id, environment_id, name, kind, base_url, secret_id, created_at, updated_at)
		VALUES ('gl', 'env', 'gl', 'gitlab', 'https://gitlab.com', 'sec2', 1, 1)`); err != nil {
		t.Fatalf("gitlab kind refused: %v", err)
	}
	if _, err := db.ExecContext(ctx, `INSERT INTO forges (id, environment_id, name, kind, base_url, secret_id, created_at, updated_at)
		VALUES ('bad', 'env', 'bad', 'gitea', 'https://gitea.example.com', NULL, 1, 1)`); err == nil {
		t.Fatal("an unknown forge kind was accepted")
	}
}

// Migration 033 widens the kinds of sandboxes to the system VMs of subscription logins. The
// sandboxes table is rebuilt, so its rows, indexes and owner triggers must come through.
func TestMigrateSystemSandboxKinds(t *testing.T) {
	ctx := context.Background()
	db, err := sql.Open("sqlite", "file::memory:?_pragma=foreign_keys(1)")
	if err != nil {
		t.Fatal(err)
	}
	db.SetMaxOpenConns(1)
	s := &Store{db: db}
	t.Cleanup(func() { s.Close() })
	names, err := fs.Glob(migrations, "migrations/*.sql")
	if err != nil {
		t.Fatal(err)
	}
	sort.Strings(names)
	// The file's version is its position in the sorted list, so the migrations before it are
	// that many.
	before := slices.Index(names, "migrations/033_system_sandboxes.sql")
	if before < 0 {
		t.Fatal("migration 033 is missing")
	}
	if err := s.migrateTo(ctx, before); err != nil {
		t.Fatal(err)
	}
	for _, q := range []string{
		`INSERT INTO environments (id, name, created_at) VALUES ('env', 'Work', 1)`,
		`INSERT INTO providers (id, environment_id, name, kind, created_at, updated_at) VALUES ('prov', 'env', 'Claude', 'claude_subscription', 1, 1)`,
		`INSERT INTO teammates (id, environment_id, name, role, soul, harness, provider_id, model, git_name, git_email, created_at, updated_at)
		 VALUES ('ada', 'env', 'Ada', '', '', 'claude', 'prov', '', 'Ada', 'ada@example.com', 1, 1)`,
		`INSERT INTO sandboxes (id, environment_id, name, cpus, memory_mib, workspace_mib, docker_mib, created_at) VALUES ('sb1', 'env', 'api', 1, 512, 1024, 0, 1)`,
		`INSERT INTO sandboxes (id, environment_id, name, cpus, memory_mib, workspace_mib, docker_mib, created_at, teammate_id, kind) VALUES ('br', 'env', 'browser-ada', 1, 512, 1024, 0, 1, 'ada', 'browser')`,
	} {
		if _, err := db.ExecContext(ctx, q); err != nil {
			t.Fatalf("%s: %v", q, err)
		}
	}
	if err := s.migrateTo(ctx, 0); err != nil {
		t.Fatal(err)
	}

	if sb, err := s.Sandbox(ctx, "env", "sb1"); err != nil || sb.Kind != "" || sb.Name != "api" {
		t.Fatalf("ordinary sandbox after the rebuild: %+v, %v", sb, err)
	}
	if sb, err := s.BrowserSandbox(ctx, "env", "ada"); err != nil || sb.ID != "br" {
		t.Fatalf("browser sandbox after the rebuild: %+v, %v", sb, err)
	}
	if _, err := db.ExecContext(ctx, `INSERT INTO sandboxes (id, environment_id, name, cpus, memory_mib, workspace_mib, docker_mib, created_at, kind) VALUES ('sys', 'env', 'login-p', 1, 512, 1024, 0, 1, 'codex-login')`); err != nil {
		t.Fatalf("a system sandbox was refused: %v", err)
	}
	if _, err := db.ExecContext(ctx, `INSERT INTO sandboxes (id, environment_id, name, cpus, memory_mib, workspace_mib, docker_mib, created_at, kind) VALUES ('bad', 'env', 'x', 1, 512, 1024, 0, 1, 'other')`); err == nil {
		t.Fatal("an unknown sandbox kind was accepted")
	}
	// A subscription provider can hold its login's secret now; the OpenAI-compatible rule still holds.
	for _, q := range []string{
		`INSERT INTO secrets (id, environment_id, name, value, hosts, placeholder, note, created_at, updated_at) VALUES ('tok', 'env', 'PROVIDER_MAX_KEY', x'00', 'api.anthropic.com', 'studio-tok', '', 1, 1)`,
		`INSERT INTO providers (id, environment_id, name, kind, secret_id, created_at, updated_at) VALUES ('max', 'env', 'Max', 'claude_subscription', 'tok', 1, 1)`,
	} {
		if _, err := db.ExecContext(ctx, q); err != nil {
			t.Fatalf("%s: %v", q, err)
		}
	}
	if _, err := db.ExecContext(ctx, `INSERT INTO providers (id, environment_id, name, kind, created_at, updated_at) VALUES ('compat', 'env', 'Local', 'openai_compatible', 1, 1)`); err == nil {
		t.Fatal("an OpenAI-compatible provider without a base URL was accepted")
	}
	if prov, err := s.Provider(ctx, "env", "max"); err != nil || prov.SecretID != "tok" {
		t.Fatalf("the subscription provider after the rebuild: %+v, %v", prov, err)
	}
	// The owner triggers still hold: a teammate of another environment is refused.
	if _, err := db.ExecContext(ctx, `INSERT INTO environments (id, name, created_at) VALUES ('other', 'Other', 1)`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, `INSERT INTO sandboxes (id, environment_id, name, cpus, memory_mib, workspace_mib, docker_mib, created_at, teammate_id) VALUES ('cross', 'other', 'x', 1, 512, 1024, 0, 1, 'ada')`); err == nil {
		t.Fatal("a sandbox took a teammate of another environment")
	}
}
