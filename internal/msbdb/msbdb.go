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

// Fix moves the damaged catalog at path aside, never deleting it, and puts in its place a
// new catalog with every row that can still be read. When none can be read safely, path is
// left free for msb to create an empty catalog. It must run before any msb call opens path.
func Fix(ctx context.Context, path string, now time.Time) (Repair, error) {
	aside := path + ".broken-" + now.UTC().Format("20060102T150405Z")
	for _, s := range suffixes {
		if err := os.Rename(path+s, aside+s); err != nil && !errors.Is(err, os.ErrNotExist) {
			return Repair{}, fmt.Errorf("move the damaged database aside: %w", err)
		}
	}
	r := Repair{Aside: aside}
	// Read a copy, so opening it (which may replay its WAL) leaves the moved-aside files as
	// they were.
	work, err := os.MkdirTemp(filepath.Dir(path), "msb.db.salvage-")
	if err != nil {
		return r, err
	}
	defer os.RemoveAll(work)
	src := filepath.Join(work, "src.db")
	for _, s := range suffixes {
		if err := copyFile(aside+s, src+s); err != nil && !errors.Is(err, os.ErrNotExist) {
			return r, err
		}
	}
	out := filepath.Join(work, "new.db")
	r.Rows, r.Partial, err = salvage(ctx, src, out)
	if err != nil {
		r.Why = err.Error()
		return r, nil
	}
	if err := os.Rename(out, path); err != nil {
		return r, err
	}
	r.Salvaged = true
	return r, nil
}

// salvage copies the schema and every readable row of src into a new catalog at out. A
// table that stops reading part way keeps the rows before; its name is in partial. The
// migration table must come through whole: without it msb would migrate its tables again.
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
		return 0, nil, fmt.Errorf("its schema can't be read: %w", err)
	}
	for q.Next() {
		var o object
		if err := q.Scan(&o.kind, &o.name, &o.sql); err != nil {
			q.Close()
			return 0, nil, fmt.Errorf("its schema can't be read: %w", err)
		}
		schema = append(schema, o)
	}
	q.Close()
	if err := q.Err(); err != nil {
		return 0, nil, fmt.Errorf("its schema can't be read: %w", err)
	}
	if len(schema) == 0 {
		return 0, nil, errors.New("it has no tables")
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
// whole is false if reading stopped early; err is for failures to write.
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
	for q.Next() {
		if err := q.Scan(ptrs...); err != nil {
			break
		}
		if _, err := ins.ExecContext(ctx, vals...); err != nil {
			return 0, false, fmt.Errorf("copy a row of %s: %w", table, err)
		}
		n++
	}
	whole = q.Err() == nil
	return n, whole, tx.Commit()
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
