package msbdb

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"
)

var now = time.Date(2026, 10, 19, 16, 0, 0, 0, time.UTC)

// catalog writes a database shaped like msb's: a migration table, a small table with an
// AUTOINCREMENT key and an index, and a table of many rows that fills the end of the file.
func catalog(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "msb.db")
	db, err := sql.Open("sqlite", "file:"+path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	for _, q := range []string{
		"PRAGMA user_version = 7",
		"CREATE TABLE seaql_migrations (version TEXT PRIMARY KEY, applied_at INTEGER NOT NULL)",
		"INSERT INTO seaql_migrations VALUES ('m1', 1), ('m2', 2)",
		"CREATE TABLE sandbox (id INTEGER PRIMARY KEY AUTOINCREMENT, name TEXT NOT NULL UNIQUE)",
		"CREATE INDEX sandbox_name ON sandbox (name)",
		"INSERT INTO sandbox (name) VALUES ('ss-a'), ('ss-b')",
		"DELETE FROM sandbox WHERE name = 'ss-b'",
		"CREATE TABLE layer (id INTEGER PRIMARY KEY, digest TEXT NOT NULL)",
	} {
		if _, err := db.Exec(q); err != nil {
			t.Fatal(q, err)
		}
	}
	// One transaction: a commit for each row takes minutes on Windows, where each one syncs.
	tx, err := db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	for i := range 3000 {
		if _, err := tx.Exec("INSERT INTO layer (digest) VALUES (?)", strings.Repeat("d", 200)+string(rune('a'+i%26))); err != nil {
			t.Fatal(err)
		}
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	return path
}

// damage overwrites the last n bytes of the file.
func damageTail(t *testing.T, path string, n int) {
	t.Helper()
	f, err := os.OpenFile(path, os.O_RDWR, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	st, _ := f.Stat()
	if _, err := f.WriteAt(bytes.Repeat([]byte{0xA5}, n), st.Size()-int64(n)); err != nil {
		t.Fatal(err)
	}
}

func TestCheckPassesASoundOrMissingCatalog(t *testing.T) {
	ctx := context.Background()
	if err := Check(ctx, filepath.Join(t.TempDir(), "msb.db")); err != nil {
		t.Fatalf("missing: %v", err)
	}
	if err := Check(ctx, catalog(t)); err != nil {
		t.Fatalf("sound: %v", err)
	}
}

func TestFixSalvagesTheReadableRows(t *testing.T) {
	ctx := context.Background()
	path := catalog(t)
	damageTail(t, path, 64<<10)
	var d *Damaged
	if err := Check(ctx, path); !errors.As(err, &d) {
		t.Fatalf("Check = %v, want *Damaged", err)
	}
	before, _ := os.ReadFile(path)

	r, err := Fix(ctx, path, now)
	if err != nil {
		t.Fatal(err)
	}
	if !r.Salvaged || r.Why != "" {
		t.Fatalf("repair = %+v, want salvaged", r)
	}
	if r.Aside != path+".broken-20261019T160000Z" {
		t.Fatalf("aside = %s", r.Aside)
	}
	if kept, _ := os.ReadFile(r.Aside); !bytes.Equal(kept, before) {
		t.Fatal("the damaged file was not kept as it was")
	}
	if err := Check(ctx, path); err != nil {
		t.Fatalf("Check after Fix = %v", err)
	}
	if strings.Join(r.Partial, ",") != "layer" {
		t.Fatalf("partial = %v, want layer", r.Partial)
	}
	db, err := sql.Open("sqlite", "file:"+path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var migrations, layers, version int
	var sandbox string
	for q, dest := range map[string]any{
		"SELECT count(*) FROM seaql_migrations":  &migrations,
		"SELECT group_concat(name) FROM sandbox": &sandbox,
		"SELECT count(*) FROM layer":             &layers,
		"PRAGMA user_version":                    &version,
	} {
		if err := db.QueryRow(q).Scan(dest); err != nil {
			t.Fatal(q, err)
		}
	}
	if migrations != 2 || sandbox != "ss-a" || layers == 0 || layers >= 3000 || version != 7 {
		t.Fatalf("migrations %d, sandbox %q, layers %d, version %d", migrations, sandbox, layers, version)
	}
	// The counter came along, so a new sandbox doesn't reuse the deleted one's id.
	var id int
	if err := db.QueryRow("INSERT INTO sandbox (name) VALUES ('ss-c') RETURNING id").Scan(&id); err != nil || id != 3 {
		t.Fatalf("new id = %d, %v; want 3", id, err)
	}
	if _, err := db.Exec("INSERT INTO sandbox (name) VALUES ('ss-a')"); err == nil {
		t.Fatal("the unique constraint did not come along")
	}
	if left, _ := filepath.Glob(filepath.Join(filepath.Dir(path), "msb.db.salvage-*")); len(left) > 0 {
		t.Fatalf("left behind: %v", left)
	}
}

func TestFixLeavesAnUnreadableCatalogToMSB(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "msb.db")
	if err := os.WriteFile(path, bytes.Repeat([]byte("not sqlite"), 1000), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path+"-wal", []byte("wal"), 0o644); err != nil {
		t.Fatal(err)
	}
	var d *Damaged
	if err := Check(ctx, path); !errors.As(err, &d) || !strings.Contains(d.Error(), "not a database") {
		t.Fatalf("Check = %v", err)
	}
	r, err := Fix(ctx, path, now)
	if err != nil {
		t.Fatal(err)
	}
	if r.Salvaged || r.Why == "" {
		t.Fatalf("repair = %+v, want nothing salvaged and a reason", r)
	}
	for _, s := range []string{"", "-wal"} {
		if _, err := os.Stat(path + s); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("%s is still there", path+s)
		}
		if _, err := os.Stat(r.Aside + s); err != nil {
			t.Fatalf("%s was not kept: %v", r.Aside+s, err)
		}
	}
	if !strings.Contains(r.Summary(), "lost its list of sandboxes") {
		t.Fatalf("summary = %s", r.Summary())
	}
}

func TestFixWithoutTheMigrationsStartsOver(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "msb.db")
	db, err := sql.Open("sqlite", "file:"+path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec("CREATE TABLE sandbox (id INTEGER PRIMARY KEY)"); err != nil {
		t.Fatal(err)
	}
	db.Close()
	r, err := Fix(ctx, path, now)
	if err != nil {
		t.Fatal(err)
	}
	if r.Salvaged || !strings.Contains(r.Why, "migrations") {
		t.Fatalf("repair = %+v", r)
	}
}

// msb's own processes write the catalog while Studio checks it.
func TestCheckPassesACatalogInUse(t *testing.T) {
	ctx := context.Background()
	path := catalog(t)
	db, err := sql.Open("sqlite", "file:"+path+"?_pragma=journal_mode(wal)")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := db.Exec("INSERT INTO sandbox (name) VALUES ('ss-live')"); err != nil {
		t.Fatal(err)
	}
	tx, err := db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	if _, err := tx.Exec("INSERT INTO layer (digest) VALUES ('open')"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(path + "-wal"); err != nil {
		t.Fatalf("no WAL: %v", err)
	}
	if err := Check(ctx, path); err != nil {
		t.Fatalf("Check = %v", err)
	}
}

// constrain gives table the schema after its rows went in, the way a damaged page can
// read back as a row its constraints refuse.
func constrain(t *testing.T, path string, rows []string, table, schema string) {
	t.Helper()
	db, err := sql.Open("sqlite", "file:"+path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	for _, q := range append(rows,
		"PRAGMA writable_schema = ON",
		"UPDATE sqlite_master SET sql = '"+strings.ReplaceAll(schema, "'", "''")+"' WHERE name = '"+table+"'",
	) {
		if _, err := db.Exec(q); err != nil {
			t.Fatal(q, err)
		}
	}
}

// A run of two writers left maintenance_lease with a NULL key and a duplicated volume: the
// rebuild leaves the lease out and the refused row behind, and keeps every sandbox.
func TestFixSkipsRowsTheConstraintsRefuse(t *testing.T) {
	ctx := context.Background()
	path := catalog(t)
	constrain(t, path, []string{
		"CREATE TABLE maintenance_lease (name TEXT, holder_pid INTEGER)",
		"INSERT INTO maintenance_lease VALUES (NULL, 42), ('install_exclusive', 7)",
	}, "maintenance_lease", "CREATE TABLE maintenance_lease (name TEXT NOT NULL PRIMARY KEY, holder_pid INTEGER)")
	constrain(t, path, []string{
		"CREATE TABLE volume (name TEXT)",
		"INSERT INTO volume VALUES ('data'), ('data'), (NULL), ('cache')",
	}, "volume", "CREATE TABLE volume (name TEXT NOT NULL UNIQUE)")

	r, err := Fix(ctx, path, now)
	if err != nil || !r.Salvaged {
		t.Fatalf("repair = %+v, %v; want salvaged", r, err)
	}
	if strings.Join(r.Partial, ",") != "volume" {
		t.Fatalf("partial = %v, want volume", r.Partial)
	}
	db, err := sql.Open("sqlite", "file:"+path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var leases, layers int
	var volumes string
	for q, dest := range map[string]any{
		"SELECT count(*) FROM maintenance_lease":           &leases,
		"SELECT group_concat(name) FROM volume ORDER BY 1": &volumes,
		"SELECT count(*) FROM layer":                       &layers,
	} {
		if err := db.QueryRow(q).Scan(dest); err != nil {
			t.Fatal(q, err)
		}
	}
	if leases != 0 || volumes != "data,cache" || layers != 3000 {
		t.Fatalf("leases %d, volumes %q, layers %d", leases, volumes, layers)
	}
	if names, err := Sandboxes(ctx, path); err != nil || strings.Join(names, ",") != "ss-a" {
		t.Fatalf("sandboxes = %v, %v", names, err)
	}
}

// An older Studio couldn't salvage a catalog and msb started an empty one; Restore rebuilds
// it from the moved-aside file and keeps the empty one aside in turn.
func TestRestoreRebuildsFromTheMovedAsideCatalog(t *testing.T) {
	ctx := context.Background()
	path := catalog(t)
	aside := path + ".broken-20261018T120000Z"
	if err := moveAside(path, aside); err != nil {
		t.Fatal(err)
	}
	// What msb made in its place.
	fresh, err := sql.Open("sqlite", "file:"+path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := fresh.Exec("CREATE TABLE sandbox (id INTEGER PRIMARY KEY, name TEXT NOT NULL)"); err != nil {
		t.Fatal(err)
	}
	fresh.Close()
	if found, err := Asides(path); err != nil || len(found) != 1 || found[0] != aside {
		t.Fatalf("asides = %v, %v", found, err)
	}

	r, err := Restore(ctx, path, aside, now)
	if err != nil || !r.Salvaged {
		t.Fatalf("restore = %+v, %v", r, err)
	}
	if names, err := Sandboxes(ctx, path); err != nil || strings.Join(names, ",") != "ss-a" {
		t.Fatalf("sandboxes = %v, %v", names, err)
	}
	if _, err := os.Stat(aside); err != nil {
		t.Fatalf("the moved-aside catalog is gone: %v", err)
	}
	if r.Aside != path+".replaced-20261019T160000Z" {
		t.Fatalf("replaced = %s", r.Aside)
	}
	if _, err := os.Stat(r.Aside); err != nil {
		t.Fatalf("the replaced catalog is gone: %v", err)
	}

	// From a file that can't be salvaged, the catalog in place stays.
	junk := filepath.Join(t.TempDir(), "junk.db")
	if err := os.WriteFile(junk, bytes.Repeat([]byte("x"), 4096), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := Restore(ctx, path, junk, now.Add(time.Minute)); err == nil {
		t.Fatal("restored from junk")
	}
	if names, err := Sandboxes(ctx, path); err != nil || strings.Join(names, ",") != "ss-a" {
		t.Fatalf("after a failed restore: %v, %v", names, err)
	}
}

// wideCatalog is catalog with enough tables that SQLite's list of them, which starts on the
// first page, goes on over several more.
func wideCatalog(t *testing.T) string {
	t.Helper()
	path := catalog(t)
	db, err := sql.Open("sqlite", "file:"+path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	for i := range 40 {
		var cols []string
		for c := range 30 {
			cols = append(cols, fmt.Sprintf("column_number_%d TEXT", c))
		}
		name := fmt.Sprintf("table_%02d", i)
		for _, q := range []string{
			"CREATE TABLE " + name + " (id INTEGER PRIMARY KEY, " + strings.Join(cols, ", ") + ")",
			"CREATE INDEX " + name + "_c0 ON " + name + " (column_number_0)",
			"INSERT INTO " + name + " (column_number_0) VALUES ('row of " + name + "')",
		} {
			if _, err := db.Exec(q); err != nil {
				t.Fatal(q, err)
			}
		}
	}
	var pages int
	if err := db.QueryRow("SELECT count(*) FROM dbstat WHERE name = 'sqlite_schema'").Scan(&pages); err == nil && pages < 4 {
		t.Fatalf("the list of tables is on %d pages", pages)
	}
	return path
}

// overwrite writes n bytes of b at off.
func overwrite(t *testing.T, path string, off int64, b byte, n int) {
	t.Helper()
	f, err := os.OpenFile(path, os.O_RDWR, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if _, err := f.WriteAt(bytes.Repeat([]byte{b}, n), off); err != nil {
		t.Fatal(err)
	}
}

// salvaged checks that the catalog at path holds every row wideCatalog wrote.
func salvaged(t *testing.T, path string) {
	t.Helper()
	if err := Check(context.Background(), path); err != nil {
		t.Fatalf("Check = %v", err)
	}
	db, err := sql.Open("sqlite", "file:"+path+"?mode=ro")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var migrations, layers, tables, indexes int
	var sandbox, last string
	for q, dest := range map[string]any{
		"SELECT count(*) FROM seaql_migrations":                                           &migrations,
		"SELECT group_concat(name) FROM sandbox":                                          &sandbox,
		"SELECT count(*) FROM layer":                                                      &layers,
		"SELECT count(*) FROM sqlite_master WHERE name LIKE 'table_%' AND type = 'table'": &tables,
		"SELECT count(*) FROM sqlite_master WHERE name LIKE 'table_%' AND type = 'index'": &indexes,
		"SELECT column_number_0 FROM table_39":                                            &last,
	} {
		if err := db.QueryRow(q).Scan(dest); err != nil {
			t.Fatal(q, err)
		}
	}
	if migrations != 2 || sandbox != "ss-a" || layers != 3000 || tables != 40 || indexes != 40 || last != "row of table_39" {
		t.Fatalf("migrations %d, sandbox %q, layers %d, tables %d, indexes %d, last %q", migrations, sandbox, layers, tables, indexes, last)
	}
}

// A first page overwritten whole leaves SQLite unable to read the file ("file is not a
// database"), though the list of tables goes on over later pages: Fix writes a new first page
// pointing at them and salvages every table.
func TestFixRebuildsAFirstPageOverwritten(t *testing.T) {
	ctx := context.Background()
	for name, damage := range map[string]func(path string){
		"zeroed":     func(path string) { overwrite(t, path, 0, 0, 4096) },
		"garbled":    func(path string) { overwrite(t, path, 0, 0xA5, 4096) },
		"header":     func(path string) { overwrite(t, path, 0, 0, 100) },
		"list start": func(path string) { overwrite(t, path, 100, 0xA5, 4096-100) },
	} {
		t.Run(name, func(t *testing.T) {
			path := wideCatalog(t)
			damage(path)
			var d *Damaged
			if err := Check(ctx, path); !errors.As(err, &d) {
				t.Fatalf("Check = %v, want *Damaged", err)
			}
			before, _ := os.ReadFile(path)
			r, err := Fix(ctx, path, now)
			if err != nil || !r.Salvaged {
				t.Fatalf("repair = %+v, %v; want salvaged", r, err)
			}
			if kept, _ := os.ReadFile(r.Aside); !bytes.Equal(kept, before) {
				t.Fatal("the damaged file was not kept as it was")
			}
			salvaged(t, path)
		})
	}
}

// The case reported: Studio couldn't salvage the catalog, msb started an empty one, and
// restore-msb-db failed with "its schema can't be read: file is not a database".
func TestRestoreRebuildsAFirstPageOverwritten(t *testing.T) {
	ctx := context.Background()
	path := wideCatalog(t)
	overwrite(t, path, 0, 0, 4096)
	aside := path + ".broken-20261018T120000Z"
	if err := moveAside(path, aside); err != nil {
		t.Fatal(err)
	}
	if r, err := Restore(ctx, path, aside, now); err != nil || !r.Salvaged {
		t.Fatalf("restore = %+v, %v", r, err)
	}
	salvaged(t, path)
}

// A catalog in WAL mode whose main file lost its first page: the copy of it in the -wal file
// is read when it is there.
func TestFixReadsTheFirstPageFromTheWAL(t *testing.T) {
	ctx := context.Background()
	path := wideCatalog(t)
	db, err := sql.Open("sqlite", "file:"+path+"?_pragma=journal_mode(wal)&_pragma=wal_autocheckpoint(0)")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := db.Exec("CREATE TABLE late (id INTEGER PRIMARY KEY, what TEXT)"); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec("INSERT INTO late (what) VALUES ('only in the wal')"); err != nil {
		t.Fatal(err)
	}
	// Copy the files while the WAL holds the commits, as a crash would leave them.
	dir := t.TempDir()
	broken := filepath.Join(dir, "msb.db")
	for _, s := range suffixes {
		if err := copyFile(path+s, broken+s); err != nil {
			t.Fatal(err)
		}
	}
	overwrite(t, broken, 0, 0, 4096)
	r, err := Fix(ctx, broken, now)
	if err != nil || !r.Salvaged {
		t.Fatalf("repair = %+v, %v; want salvaged", r, err)
	}
	salvaged(t, broken)
	check, err := sql.Open("sqlite", "file:"+broken+"?mode=ro")
	if err != nil {
		t.Fatal(err)
	}
	defer check.Close()
	var what string
	if err := check.QueryRow("SELECT what FROM late").Scan(&what); err != nil || what != "only in the wal" {
		t.Fatalf("late = %q, %v", what, err)
	}
}

func TestVMName(t *testing.T) {
	for args, want := range map[string]string{
		"/home/u/.microsandbox/bin/msb machine --name studio-abc-g1 --sandbox-id 3": "studio-abc-g1",
		"msb machine --restore --name studio-x-g2":                                  "studio-x-g2",
		"vim --name studio-abc-g1":                                                  "",
		"msb machine --name":                                                        "",
	} {
		if got, _ := vmName(strings.Fields(args)); got != want {
			t.Errorf("vmName(%q) = %q, want %q", args, got, want)
		}
	}
}

// A catalog in use keeps its newest rows in the -wal file; the backup has them, in one file,
// and the oldest backups go.
func TestBackupCopiesACatalogInUseWithItsWAL(t *testing.T) {
	ctx := context.Background()
	path := catalog(t)
	db, err := sql.Open("sqlite", "file:"+path+"?_pragma=journal_mode(wal)&_pragma=wal_autocheckpoint(0)")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := db.Exec("INSERT INTO sandbox (name) VALUES ('ss-new-g2')"); err != nil {
		t.Fatal(err)
	}
	if info, err := os.Stat(path + "-wal"); err != nil || info.Size() == 0 {
		t.Fatalf("no WAL: %v", err)
	}
	var made []string
	for i := range 4 {
		out, err := Backup(ctx, path, now.Add(time.Duration(i)*time.Minute), 3)
		if err != nil {
			t.Fatal(err)
		}
		made = append(made, out)
	}
	names, err := Sandboxes(ctx, made[3])
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(names, ",") != "ss-a,ss-new-g2" {
		t.Fatalf("backup knows %v", names)
	}
	if _, err := os.Stat(made[3] + "-wal"); err == nil {
		t.Error("the backup has a -wal file; it should be one file")
	}
	if _, err := os.Stat(made[0]); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("the oldest backup is still there: %v", err)
	}
	saved, err := Saved(path)
	if err != nil || len(saved) != 3 || saved[0] != made[3] {
		t.Fatalf("Saved = %v, %v; want the three backups, newest first", saved, err)
	}
}

func TestBackupKeepsNoDamagedCatalog(t *testing.T) {
	path := catalog(t)
	damageTail(t, path, 4096)
	if _, err := Backup(context.Background(), path, now, 3); err == nil {
		t.Fatal("a damaged catalog was backed up")
	}
	if found, _ := filepath.Glob(path + BackupPrefix + "*"); len(found) != 0 {
		t.Fatalf("backups %v", found)
	}
}

func TestKeepBeforeCopiesOncePerVersion(t *testing.T) {
	ctx := context.Background()
	path := catalog(t)
	out, err := KeepBefore(ctx, path, "0.7.15", 2)
	if err != nil || out != path+BeforePrefix+"0.7.15" {
		t.Fatalf("KeepBefore = %q, %v", out, err)
	}
	if names, err := Sandboxes(ctx, out); err != nil || strings.Join(names, ",") != "ss-a" {
		t.Fatalf("the copy knows %v, %v", names, err)
	}
	if again, err := KeepBefore(ctx, path, "0.7.15", 2); again != "" || err != nil {
		t.Errorf("KeepBefore again = %q, %v", again, err)
	}
	for i, v := range []string{"0.7.16", "0.7.17"} {
		old := time.Now().Add(time.Duration(i-3) * time.Hour)
		if err := os.Chtimes(path+BeforePrefix+"0.7.15", old, old); err != nil && i == 0 {
			t.Fatal(err)
		}
		if _, err := KeepBefore(ctx, path, v, 2); err != nil {
			t.Fatal(err)
		}
	}
	found, _ := filepath.Glob(path + BeforePrefix + "*")
	if len(found) != 2 || slices.Contains(found, path+BeforePrefix+"0.7.15") {
		t.Errorf("copies %v, want the two newest", found)
	}
	// Neither a restore nor the backups' rotation takes them for their own.
	if asides, _ := Asides(path); len(asides) != 0 {
		t.Errorf("asides %v", asides)
	}
	if none, err := KeepBefore(ctx, filepath.Join(t.TempDir(), "msb.db"), "0.7.15", 2); none != "" || err != nil {
		t.Errorf("KeepBefore without a catalog = %q, %v", none, err)
	}
}
