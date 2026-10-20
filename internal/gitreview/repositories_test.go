package gitreview

import (
	"context"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/lukaskoebe/sandbox-studio/internal/store"
)

// TestListRepositories: each API lists the user's repositories page by page, up to
// MaxRepositories, and leaves out paths the virtual remote can't serve.
func TestListRepositories(t *testing.T) {
	ctx := context.Background()
	ff := newFakeForge(t)
	client := *ff.srv.Client()
	client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }

	// 61 repositories take two pages of 50 on Forgejo.
	ff.repos = 60
	got, err := (&Forgejo{BaseURL: ff.srv.URL, Client: &client}).ListRepositories(ctx, forgeToken)
	if err != nil || len(got) != 61 || got[0] != (Repo{Path: "owner/repo", Description: "about owner/repo", Private: true}) || got[60].Path != "owner/r60" {
		t.Fatalf("forgejo: %d repositories, %v (%+v)", len(got), err, got[:min(len(got), 2)])
	}
	if _, err := (&Forgejo{BaseURL: ff.srv.URL, Client: &client}).ListRepositories(ctx, "wrong"); err == nil {
		t.Fatal("forgejo listed with a wrong token")
	}

	// A list longer than the cap stops at it.
	ff.repos = 1000
	got, err = (&GitHub{BaseURL: ff.srv.URL, Client: &client}).ListRepositories(ctx, forgeToken)
	if err != nil || len(got) != MaxRepositories || got[0].Path != "owner/repo" {
		t.Fatalf("github: %d repositories, %v", len(got), err)
	}

	got, err = (&GitLab{BaseURL: ff.srv.URL, Client: &client}).ListRepositories(ctx, forgeToken)
	if err != nil || !reflect.DeepEqual(got, []Repo{{Path: "group/sub/proj", Private: true}}) {
		t.Fatalf("gitlab: %+v, %v", got, err)
	}

	if _, err := (Plain{}).ListRepositories(ctx, forgeToken); !errors.Is(err, errNoAPI) {
		t.Fatalf("plain: %v", err)
	}
}

func TestServiceRepositories(t *testing.T) {
	h := newHarness(t)
	got, err := h.svc.Repositories(h.ctx, h.f)
	if err != nil || len(got) != 1 || got[0].Path != "owner/repo" {
		t.Fatalf("forgejo forge: %+v, %v", got, err)
	}
	for _, kind := range []string{KindHTTPS, KindSSH} {
		if _, err := h.svc.Repositories(h.ctx, store.Forge{Kind: kind, BaseURL: h.forge.srv.URL}); !errors.Is(err, ErrInvalid) {
			t.Fatalf("%s forge: %v; want ErrInvalid", kind, err)
		}
	}
}

// TestLocalRepositories: a local forge lists the repositories in its folder and one level
// down, working copies and bare ones, without hidden folders and plain folders.
func TestLocalRepositories(t *testing.T) {
	folder := t.TempDir()
	mkdir := func(parts ...string) {
		t.Helper()
		if err := os.MkdirAll(filepath.Join(append([]string{folder}, parts...)...), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	touch := func(parts ...string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(append([]string{folder}, parts...)...), nil, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	mkdir("app", ".git")
	mkdir("bare.git", "objects")
	touch("bare.git", "HEAD")
	mkdir("team", "api", ".git")
	mkdir("team", "deep", "nested", ".git") // two levels down: not listed
	mkdir(".hidden", ".git")
	mkdir("notes")
	touch("file.txt")

	got, err := (&Service{}).Repositories(context.Background(), store.Forge{Kind: KindLocal, BaseURL: folder})
	want := []Repo{{Path: "app"}, {Path: "bare"}, {Path: "team/api"}}
	if err != nil || !reflect.DeepEqual(got, want) {
		t.Fatalf("local: %+v, %v; want %+v", got, err, want)
	}
	if _, err := (&Service{}).Repositories(context.Background(), store.Forge{Kind: KindLocal, BaseURL: filepath.Join(folder, "gone")}); !errors.Is(err, ErrInvalid) {
		t.Fatalf("a missing folder: %v", err)
	}
}

func TestCheckRepoPath(t *testing.T) {
	for _, c := range []struct{ kind, in, path, name string }{
		{KindGitHub, "owner/repo", "owner/repo", "repo"},
		{KindGitHub, "/owner/repo.git/", "owner/repo", "repo"},
		{KindGitLab, "group/sub/proj", "group/sub/proj", "proj"},
		{KindLocal, "app", "app", "app"},
	} {
		path, name, err := CheckRepoPath(c.kind, c.in)
		if err != nil || path != c.path || name != c.name {
			t.Errorf("%s %q = %q, %q, %v", c.kind, c.in, path, name, err)
		}
	}
	for _, c := range []struct{ kind, in string }{
		{KindGitHub, "repo"}, {KindGitHub, "a/b/c"}, {KindLocal, "../etc"}, {KindLocal, ".hidden"},
		{KindLocal, "a/-b"}, {KindLocal, ""}, {KindLocal, "a b"},
	} {
		if _, _, err := CheckRepoPath(c.kind, c.in); !errors.Is(err, ErrInvalid) {
			t.Errorf("%s %q: %v; want ErrInvalid", c.kind, c.in, err)
		}
	}
}
