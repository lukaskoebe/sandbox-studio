package api

import (
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"testing"
)

func TestListHostFolders(t *testing.T) {
	h, _ := newTestServer(t)
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	for _, d := range []string{"git/app/.git", "git/bare.git/objects", "git/notes", "git/.cache", "Documents"} {
		if err := os.MkdirAll(filepath.Join(home, filepath.FromSlash(d)), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	for _, f := range []string{"git/bare.git/HEAD", "git/README.md"} {
		if err := os.WriteFile(filepath.Join(home, filepath.FromSlash(f)), nil, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	list := func(query string) HostFolder {
		t.Helper()
		rec := do(h, "GET", testOrigin+"/api/system/folders"+query, "")
		if rec.Code != http.StatusOK {
			t.Fatalf("%s: %d %s", query, rec.Code, rec.Body)
		}
		return decode[HostFolder](t, rec)
	}
	names := func(f HostFolder) (out []string) {
		for _, e := range f.Entries {
			n := e.Name
			if e.Git {
				n += " (git)"
			}
			out = append(out, n)
		}
		return out
	}

	// Browsing starts at home, whose parent is listed unless it is the root.
	got := list("")
	if got.Path != home || got.Short != "~" || got.Home != home || got.Git || len(got.Entries) != 2 {
		t.Fatalf("home: %+v", got)
	}
	if filepath.Dir(filepath.Dir(home)) != filepath.Dir(home) && got.Parent != filepath.Dir(home) {
		t.Fatalf("home's parent: %q", got.Parent)
	}

	// Only folders, without dot folders unless asked, and the repositories marked.
	git := list("?path=" + url.QueryEscape("~/git"))
	if want := []string{"app (git)", "bare.git (git)", "notes"}; !slices.Equal(names(git), want) {
		t.Fatalf("~/git: %v; want %v", names(git), want)
	}
	if git.Path != filepath.Join(home, "git") || git.Short != "~"+string(filepath.Separator)+"git" || git.Parent != home {
		t.Fatalf("~/git: %+v", git)
	}
	if want := []string{".cache", "app (git)", "bare.git (git)", "notes"}; !slices.Equal(names(list("?hidden=true&path="+url.QueryEscape(filepath.Join(home, "git")))), want) {
		t.Fatalf("with hidden folders: want %v", want)
	}
	if app := list("?path=" + url.QueryEscape(filepath.Join(home, "git", "app"))); !app.Git {
		t.Fatalf("a repository: %+v", app)
	}

	root, _ := filepath.Abs(string(filepath.Separator))
	for _, path := range []string{root, "git", filepath.Join(home, "nope"), filepath.Join(home, "git", "README.md")} {
		if rec := do(h, "GET", testOrigin+"/api/system/folders?path="+url.QueryEscape(path), ""); rec.Code != http.StatusUnprocessableEntity {
			t.Fatalf("%s: %d %s; want 422", path, rec.Code, rec.Body)
		}
	}
}
