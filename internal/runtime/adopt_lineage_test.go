package runtime

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestRebindLineageBindsTheAncestryToTheNewRecord(t *testing.T) {
	dir := t.TempDir()
	old := CatalogID
	t.Cleanup(func() { CatalogID = old })
	CatalogID = func(_ context.Context, name string) (int64, error) {
		if name != "ss-x-g1" {
			t.Errorf("asked for %q", name)
		}
		return 7, nil
	}
	r := &Runtime{}
	// Without ancestry there is nothing to do, and no id is asked for.
	if err := r.rebindLineage(context.Background(), dir, "other"); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, lineageFile)
	if err := os.WriteFile(path, []byte(`{"sandbox_id":1,"snapshot_id":"snap_04ce"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := r.rebindLineage(context.Background(), dir, "ss-x-g1"); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(path)
	if string(b) != `{"sandbox_id":7,"snapshot_id":"snap_04ce"}` {
		t.Errorf("ancestry is %s", b)
	}
	if fi, _ := os.Stat(path); fi.Mode().Perm() != 0o600 {
		t.Errorf("ancestry is %o, want 600", fi.Mode().Perm())
	}
	if entries, _ := os.ReadDir(dir); len(entries) != 1 {
		t.Errorf("left %d files", len(entries))
	}
	CatalogID = nil
	if err := r.rebindLineage(context.Background(), dir, "ss-x-g1"); err == nil {
		t.Error("rebound without a way to read the id")
	}
}
