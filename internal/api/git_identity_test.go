package api

import (
	"context"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/lukaskoebe/sandbox-studio/internal/store"
)

func TestHostGitIdentityReadsGlobalConfig(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is not installed")
	}
	cfg := filepath.Join(t.TempDir(), "gitconfig")
	t.Setenv("GIT_CONFIG_GLOBAL", cfg)
	t.Setenv("GIT_CONFIG_NOSYSTEM", "1")
	if got := HostGitIdentity(t.Context()); got != (GitIdentity{}) {
		t.Fatalf("without a config: %+v", got)
	}
	if err := os.WriteFile(cfg, []byte("[user]\n\tname = Ada Lovelace\n\temail = ada@example.com\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if got := HostGitIdentity(t.Context()); got != (GitIdentity{Name: "Ada Lovelace", Email: "ada@example.com"}) {
		t.Fatalf("with a config: %+v", got)
	}
}

func TestHostGitIdentityForTeammates(t *testing.T) {
	h, s := newTestServer(t)
	ctx := context.Background()
	const path = testOrigin + "/api/system/git-identity"
	if got := decode[GitIdentity](t, do(h, "GET", path, "")); got != (GitIdentity{}) {
		t.Fatalf("without a reader: %+v", got)
	}
	host := GitIdentity{Name: "Lukas", Email: "lukas@example.com"}
	s.HostGit = func(context.Context) GitIdentity { return host }
	if got := decode[GitIdentity](t, do(h, "GET", path, "")); got != host {
		t.Fatalf("host identity: %+v", got)
	}

	env := newEnvironment(t, s, "work")
	prov := createProvider(t, h, env, `{"name":"Anthropic","kind":"anthropic_api","apiKey":"`+providerKey+`"}`)
	teammates := testOrigin + "/api/environments/" + env.ID + "/teammates"
	rec := do(h, "POST", teammates,
		`{"name":"Ada","harness":"claude","providerId":"`+prov.ID+`","model":"claude-sonnet-4-5","gitName":"Ada L","gitEmail":"ada@agents.invalid","useHostGitIdentity":true}`)
	if rec.Code != http.StatusCreated {
		t.Fatalf("teammate: %d %s", rec.Code, rec.Body)
	}
	ada := decode[store.Teammate](t, rec)
	if !ada.UseHostGitIdentity || ada.GitName != "Ada L" {
		t.Fatalf("created: %+v", ada)
	}
	sb, err := s.Store.CreateSandbox(ctx, store.Sandbox{EnvironmentID: env.ID, Name: "ada-dev", CPUs: 1, TeammateID: ada.ID})
	if err != nil {
		t.Fatal(err)
	}
	gitConfig := func() string {
		t.Helper()
		files, _, err := s.HarnessConfig(ctx, sb)
		if err != nil {
			t.Fatal(err)
		}
		for _, f := range files {
			if f.Path == ".config/git/config" {
				return f.Content
			}
		}
		t.Fatal("no git config")
		return ""
	}
	if got := gitConfig(); !strings.Contains(got, "name = \"Lukas\"\n\temail = lukas@example.com\n") {
		t.Fatalf("host identity not used:\n%s", got)
	}

	// A host without an identity, or with one git config can't hold, leaves the teammate's.
	for _, id := range []GitIdentity{{Name: "Lukas"}, {Name: `Lukas "the" user`, Email: "lukas@example.com"}} {
		host = id
		if got := gitConfig(); !strings.Contains(got, "name = \"Ada L\"\n\temail = ada@agents.invalid\n") {
			t.Fatalf("host %+v: teammate identity not used:\n%s", id, got)
		}
	}

	// Turning it off is an edit like any other.
	host = GitIdentity{Name: "Lukas", Email: "lukas@example.com"}
	if rec := do(h, "PUT", teammates+"/"+ada.ID,
		`{"name":"Ada","harness":"claude","providerId":"`+prov.ID+`","model":"claude-sonnet-4-5","gitName":"Ada L","gitEmail":"ada@agents.invalid"}`); rec.Code != http.StatusOK {
		t.Fatalf("update: %d %s", rec.Code, rec.Body)
	}
	if got := gitConfig(); !strings.Contains(got, `name = "Ada L"`) {
		t.Fatalf("turned off, host identity still used:\n%s", got)
	}
}
