//go:build linux

package guest

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"testing"
)

func TestIndex(t *testing.T) {
	ws := t.TempDir()
	for _, f := range []string{"README.md", "notes/todo.txt", "node_modules/x/index.js", "app/.git/HEAD", "app/main.go", "app/build/out.bin", "broken/.git/HEAD", "broken/a.txt"} {
		p := filepath.Join(ws, f)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, nil, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Symlink("/etc", filepath.Join(ws, "etc")); err != nil {
		t.Fatal(err)
	}
	git := func(_ context.Context, dir string, args ...string) ([]byte, error) {
		if filepath.Base(dir) == "broken" {
			return nil, errors.New("not a repository")
		}
		if args[0] == "ls-files" {
			// build/ is ignored, so git doesn't list it.
			return []byte("main.go\x00web/app.ts\x00new.md\x00"), nil
		}
		return []byte(" M main.go\x00?? new.md\x00 D gone.go\x00"), nil
	}
	reply := index(context.Background(), ws, git)
	slices.Sort(reply.Paths)
	want := []string{"README.md", "app/main.go", "app/new.md", "app/web/app.ts", "broken/a.txt", "etc", "notes/todo.txt"}
	if !slices.Equal(reply.Paths, want) {
		t.Errorf("paths = %q, want %q", reply.Paths, want)
	}
	if !slices.Equal(reply.Changed, []string{"app/main.go", "app/new.md"}) {
		t.Errorf("changed = %q", reply.Changed)
	}
	if reply.Truncated {
		t.Error("truncated")
	}
}
