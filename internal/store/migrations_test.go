package store

import (
	"context"
	"database/sql"
	"encoding/json"
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
		rows.Scan(&n)
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
