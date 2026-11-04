// Package msbdb checks microsandbox's own catalog, $MSB_HOME/db/msb.db (by default
// ~/.microsandbox/db/msb.db), and repairs it when it is damaged. msb keeps its sandboxes,
// images, volumes and snapshot index there; a damaged file fails every msb call with a raw
// SQLite error.
//
// The package reads the file with Studio's own SQLite driver, so it works before the SDK has
// opened the file, and it has no SDK dependency.
package msbdb

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	_ "modernc.org/sqlite"
)

// Path returns msb's catalog: $MSB_HOME/db/msb.db, or ~/.microsandbox/db/msb.db.
func Path() (string, error) {
	home := os.Getenv("MSB_HOME")
	if home == "" {
		h, err := os.UserHomeDir()
		if err != nil {
			return "", err
		}
		home = filepath.Join(h, ".microsandbox")
	}
	return filepath.Join(home, "db", "msb.db"), nil
}

// Damaged is a catalog that fails SQLite's integrity check or can't be read as a database.
type Damaged struct {
	Path   string
	Detail string
}

func (d *Damaged) Error() string {
	return fmt.Sprintf("microsandbox's database %s is damaged: %s", d.Path, d.Detail)
}

// damage reports whether err is SQLite saying the file is no database or a damaged one.
func damage(err error) bool {
	msg := err.Error()
	return strings.Contains(msg, "file is not a database") ||
		strings.Contains(msg, "database disk image is malformed")
}

// Check runs SQLite's quick integrity check on the catalog at path. It returns a *Damaged
// for a damaged catalog, nil for a sound or missing one (msb creates a missing one), and
// other errors, such as a locked file, as they are.
func Check(ctx context.Context, path string) error {
	if _, err := os.Stat(path); errors.Is(err, os.ErrNotExist) {
		return nil
	}
	// Read only, so checking can't change the catalog: SQLite would otherwise drop a WAL
	// file it can't use.
	db, err := open(path, "mode=ro")
	if err != nil {
		return err
	}
	defer db.Close()
	rows, err := db.QueryContext(ctx, "PRAGMA quick_check(5)")
	if err != nil {
		return classify(path, err)
	}
	defer rows.Close()
	var problems []string
	for rows.Next() {
		var s string
		if err := rows.Scan(&s); err != nil {
			return classify(path, err)
		}
		if s != "ok" {
			problems = append(problems, s)
		}
	}
	if err := rows.Err(); err != nil {
		return classify(path, err)
	}
	if len(problems) > 0 {
		return &Damaged{Path: path, Detail: strings.Join(problems, "; ")}
	}
	return nil
}

func classify(path string, err error) error {
	if damage(err) {
		return &Damaged{Path: path, Detail: err.Error()}
	}
	return err
}

func open(path string, query ...string) (*sql.DB, error) {
	db, err := sql.Open("sqlite", "file:"+path+"?"+strings.Join(append(query, "_pragma=busy_timeout(5000)"), "&"))
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	return db, nil
}

// Repair is what Repair did.
type Repair struct {
	// Aside is the damaged catalog, moved there with its -wal and -shm files.
	Aside string
	// Salvaged is whether the new catalog holds the rows that could still be read; if not,
	// msb starts a new, empty one.
	Salvaged bool
	// Rows is how many rows were salvaged; Partial names the tables that lost rows.
	Rows    int
	Partial []string
	// Why says why nothing was salvaged.
	Why string
}

// Summary describes the repair in a sentence.
func (r Repair) Summary() string {
	if !r.Salvaged {
		return fmt.Sprintf("Studio moved the damaged database aside to %s and msb started a new one, "+
			"so msb has lost its list of sandboxes (nothing could be salvaged: %s)", r.Aside, r.Why)
	}
	s := fmt.Sprintf("Studio moved the damaged database aside to %s and rebuilt it from the %d rows it could read", r.Aside, r.Rows)
	if len(r.Partial) > 0 {
		s += "; rows were lost from " + strings.Join(r.Partial, ", ")
	}
	return s
}

// suffixes are the files of a catalog in WAL mode.
var suffixes = []string{"", "-wal", "-shm"}

// expendable are tables a rebuilt catalog starts empty. maintenance_lease holds the leases
// msb's runtimes take to coordinate their maintenance sweeps; a lease expires on its own and
// the next runtime takes a new one, so its rows are never worth losing the others for. Two
// processes writing at once damage it first: every runtime writes it when it starts.
var expendable = map[string]bool{"maintenance_lease": true}

// moveAside renames the catalog at path, with its -wal and -shm files, to aside.
func moveAside(path, aside string) error {
	for _, s := range suffixes {
		if err := os.Rename(path+s, aside+s); err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
	}
	return nil
}

// errUnreadable is a catalog whose list of tables can't be read.
var errUnreadable = errors.New("its schema can't be read")

// build salvages the catalog at from, which it leaves as it was, into a new catalog it makes
// in work, and returns that catalog's path. When the list of tables can't be read, the first
// page, which holds SQLite's header and the start of that list, is likely damaged: build
// writes a new one on a copy, first keeping the -wal file and then without it.
func build(ctx context.Context, from, work string) (out string, rows int, partial []string, err error) {
	attempts := []struct{ firstPage, wal bool }{{false, true}, {true, true}, {true, false}}
	var first error
	for i, a := range attempts {
		// Read a copy, so opening it (which may replay its WAL) leaves from's files as they were.
		dir := filepath.Join(work, fmt.Sprint(i))
		if err := os.Mkdir(dir, 0o700); err != nil {
			return "", 0, nil, err
		}
		src := filepath.Join(dir, "src.db")
		for _, s := range suffixes {
			if s != "" && !a.wal {
				continue
			}
			if err := copyFile(from+s, src+s); err != nil && !errors.Is(err, os.ErrNotExist) {
				return "", 0, nil, err
			}
		}
		if a.firstPage {
			if err := rebuildFirstPage(src, a.wal); err != nil {
				return "", 0, nil, fmt.Errorf("%w (and its first page can't be rebuilt: %v)", first, err)
			}
		}
		out = filepath.Join(dir, "new.db")
		rows, partial, err = salvage(ctx, src, out)
		if err == nil {
			return out, rows, partial, nil
		}
		if first == nil {
			first = err
		} else if !errors.Is(err, errUnreadable) {
			first = fmt.Errorf("%w (with its first page rebuilt: %v)", first, err)
		}
		if !errors.Is(first, errUnreadable) {
			break
		}
	}
	return "", 0, nil, first
}

// workDir makes a directory next to path for build, on the same file system so its result
// can be renamed into place.
func workDir(path string) (string, error) {
	return os.MkdirTemp(filepath.Dir(path), "msb.db.salvage-")
}

// Fix moves the damaged catalog at path aside, never deleting it, and puts in its place a
// new catalog with every row that can still be read. When none can be read safely, path is
// left free for msb to create an empty catalog. It must run before any msb call opens path.
// The new catalog is built before the damaged one is moved, so path is missing only between
// two renames.
func Fix(ctx context.Context, path string, now time.Time) (Repair, error) {
	aside := path + ".broken-" + now.UTC().Format("20060102T150405Z")
	r := Repair{Aside: aside}
	work, err := workDir(path)
	if err != nil {
		return Repair{}, err
	}
	defer os.RemoveAll(work)
	out, rows, partial, built := build(ctx, path, work)
	if err := moveAside(path, aside); err != nil {
		return Repair{}, fmt.Errorf("move the damaged database aside: %w", err)
	}
	if built != nil {
		r.Why = built.Error()
		return r, nil
	}
	if err := os.Rename(out, path); err != nil {
		r.Why = err.Error()
		return r, nil
	}
	r.Salvaged, r.Rows, r.Partial = true, rows, partial
	return r, nil
}

// Asides lists the damaged catalogs Fix moved aside next to path, newest first.
func Asides(path string) ([]string, error) {
	found, err := filepath.Glob(path + ".broken-*")
	if err != nil {
		return nil, err
	}
	var asides []string
	for _, f := range found {
		if !strings.HasSuffix(f, "-wal") && !strings.HasSuffix(f, "-shm") {
			asides = append(asides, f)
		}
	}
	// The names end in a UTC time, so they sort by it.
	slices.Sort(asides)
	slices.Reverse(asides)
	return asides, nil
}

// Restore rebuilds the catalog at path from from, a damaged catalog Fix moved aside: when an
// earlier Studio could not salvage it, a newer one may. The catalog in place is moved aside to
// path.replaced-<time>, never deleted, and only once from was salvaged. msb must not have path
// open: Studio and every sandbox are stopped.
func Restore(ctx context.Context, path, from string, now time.Time) (Repair, error) {
	if _, err := os.Stat(from); err != nil {
		return Repair{}, err
	}
	work, err := workDir(path)
	if err != nil {
		return Repair{}, err
	}
	defer os.RemoveAll(work)
	out, rows, partial, err := build(ctx, from, work)
	if err != nil {
		return Repair{}, fmt.Errorf("%s can't be salvaged: %w", from, err)
	}
	replaced := path + ".replaced-" + now.UTC().Format("20060102T150405Z")
	if err := moveAside(path, replaced); err != nil {
		return Repair{}, fmt.Errorf("move the current database aside: %w", err)
	}
	r := Repair{Aside: replaced, Rows: rows, Partial: partial}
	if err := os.Rename(out, path); err != nil {
		if back := moveAside(replaced, path); back != nil {
			return r, fmt.Errorf("put the rebuilt database in place: %w, and the database in place could not be put back from %s: %v", err, replaced, back)
		}
		return r, fmt.Errorf("put the rebuilt database in place: %w", err)
	}
	r.Salvaged = true
	return r, nil
}

// Sandboxes lists the names of the sandboxes the catalog at path knows.
func Sandboxes(ctx context.Context, path string) ([]string, error) {
	db, err := open(path, "mode=ro")
	if err != nil {
		return nil, err
	}
	defer db.Close()
	q, err := db.QueryContext(ctx, "SELECT name FROM sandbox ORDER BY name")
	if err != nil {
		return nil, err
	}
	defer q.Close()
	var names []string
	for q.Next() {
		var n string
		if err := q.Scan(&n); err != nil {
			return nil, err
		}
		names = append(names, n)
	}
	return names, q.Err()
}

// salvage copies the schema and every readable row of src into a new catalog at out. A
// table that stops reading part way keeps the rows before, and a row its constraints refuse
// (a damaged page can read back as a NULL key or a duplicate) is left out; either way the
// table's name is in partial. Expendable tables are created empty. The migration table must
// come through whole: without it msb would migrate its tables again.
func salvage(ctx context.Context, src, out string) (rows int, partial []string, err error) {
	from, err := open(src)
	if err != nil {
		return 0, nil, err
	}
	defer from.Close()
	type object struct{ kind, name, sql string }
	var schema []object
	q, err := from.QueryContext(ctx, `SELECT type, name, sql FROM sqlite_master
		WHERE sql IS NOT NULL AND name NOT LIKE 'sqlite_%'
		ORDER BY CASE type WHEN 'table' THEN 0 WHEN 'index' THEN 1 ELSE 2 END, rowid`)
	if err != nil {
		return 0, nil, fmt.Errorf("%w: %w", errUnreadable, err)
	}
	for q.Next() {
		var o object
		if err := q.Scan(&o.kind, &o.name, &o.sql); err != nil {
			q.Close()
			return 0, nil, fmt.Errorf("%w: %w", errUnreadable, err)
		}
		schema = append(schema, o)
	}
	q.Close()
	if err := q.Err(); err != nil {
		return 0, nil, fmt.Errorf("%w: %w", errUnreadable, err)
	}
	if len(schema) == 0 {
		return 0, nil, fmt.Errorf("%w: it lists no tables", errUnreadable)
	}
	to, err := open(out)
	if err != nil {
		return 0, nil, err
	}
	defer to.Close()
	if _, err := to.ExecContext(ctx, "PRAGMA foreign_keys = OFF"); err != nil {
		return 0, nil, err
	}
	var version int
	if err := from.QueryRowContext(ctx, "PRAGMA user_version").Scan(&version); err == nil {
		if _, err := to.ExecContext(ctx, fmt.Sprintf("PRAGMA user_version = %d", version)); err != nil {
			return 0, nil, err
		}
	}
	migrations := false
	for _, o := range schema {
		if o.kind != "table" {
			continue
		}
		if _, err := to.ExecContext(ctx, o.sql); err != nil {
			return 0, nil, fmt.Errorf("table %s can't be created again: %w", o.name, err)
		}
		if expendable[o.name] {
			continue
		}
		n, whole, err := copyRows(ctx, from, to, o.name)
		if err != nil {
			return 0, nil, err
		}
		rows += n
		if !whole {
			partial = append(partial, o.name)
		}
		if o.name == "seaql_migrations" {
			if !whole {
				return 0, nil, errors.New("its list of migrations is damaged")
			}
			migrations = true
		}
	}
	if !migrations {
		return 0, nil, errors.New("it has no list of migrations")
	}
	// AUTOINCREMENT tables keep their counters in sqlite_sequence, which SQLite made in out
	// along with them.
	var seq int
	if err := to.QueryRowContext(ctx, "SELECT count(*) FROM sqlite_master WHERE name = 'sqlite_sequence'").Scan(&seq); err != nil {
		return 0, nil, err
	}
	if seq > 0 {
		if _, _, err := copyRows(ctx, from, to, "sqlite_sequence"); err != nil {
			return 0, nil, err
		}
	}
	for _, o := range schema {
		if o.kind == "table" {
			continue
		}
		if _, err := to.ExecContext(ctx, o.sql); err != nil {
			return 0, nil, fmt.Errorf("%s %s can't be created again: %w", o.kind, o.name, err)
		}
	}
	var check string
	if err := to.QueryRowContext(ctx, "PRAGMA quick_check").Scan(&check); err != nil || check != "ok" {
		return 0, nil, fmt.Errorf("the rebuilt database fails its check: %v %s", err, check)
	}
	return rows, partial, nil
}

// copyRows copies table's rows from one database to the other until one can't be read.
// A row the table's constraints refuse is skipped. whole is false if reading stopped early
// or a row was skipped; err is for other failures to write.
func copyRows(ctx context.Context, from, to *sql.DB, table string) (n int, whole bool, err error) {
	name := `"` + strings.ReplaceAll(table, `"`, `""`) + `"`
	q, err := from.QueryContext(ctx, "SELECT * FROM "+name)
	if err != nil {
		return 0, false, nil
	}
	defer q.Close()
	cols, err := q.Columns()
	if err != nil {
		return 0, false, nil
	}
	tx, err := to.BeginTx(ctx, nil)
	if err != nil {
		return 0, false, err
	}
	defer tx.Rollback() //nolint:errcheck // a no-op after Commit
	if table == "sqlite_sequence" {
		if _, err := tx.ExecContext(ctx, "DELETE FROM sqlite_sequence"); err != nil {
			return 0, false, err
		}
	}
	ins, err := tx.PrepareContext(ctx, "INSERT INTO "+name+" VALUES ("+strings.TrimSuffix(strings.Repeat("?,", len(cols)), ",")+")")
	if err != nil {
		return 0, false, err
	}
	defer ins.Close()
	vals := make([]any, len(cols))
	ptrs := make([]any, len(cols))
	for i := range vals {
		ptrs[i] = &vals[i]
	}
	skipped := false
	for q.Next() {
		if err := q.Scan(ptrs...); err != nil {
			break
		}
		if _, err := ins.ExecContext(ctx, vals...); err != nil {
			// SQLite undoes just the refused statement; the transaction goes on.
			if !refused(err) {
				return 0, false, fmt.Errorf("copy a row of %s: %w", table, err)
			}
			skipped = true
			continue
		}
		n++
	}
	whole = q.Err() == nil && !skipped
	return n, whole, tx.Commit()
}

// refused reports whether err is a row breaking a constraint: NOT NULL, UNIQUE, a primary
// key or a CHECK.
func refused(err error) bool {
	return strings.Contains(err.Error(), "constraint failed")
}

func copyFile(from, to string) error {
	in, err := os.Open(from)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(to, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		return err
	}
	return out.Close()
}
