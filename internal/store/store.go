// Package store is the SQLite catalog. Every query that touches environment-owned
// data takes an environment ID, so rows can never leak across environments.
package store

import (
	"context"
	"crypto/rand"
	"database/sql"
	"embed"
	"encoding/base32"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"net"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	_ "modernc.org/sqlite"
)

//go:embed migrations/*.sql
var migrations embed.FS

// ErrNotFound is returned when a row does not exist in the given environment.
var ErrNotFound = errors.New("not found")

// ErrExists is returned when a name is already taken.
var ErrExists = errors.New("already exists")

// ErrNewerSchema is returned by Open for a database that a newer Studio has migrated: this
// binary doesn't know its tables, so it would misread or damage them.
var ErrNewerSchema = errors.New("the database is from a newer version of Studio")

// Store wraps the catalog database.
type Store struct {
	db                  *sql.DB
	buildWorkerLockPath string
	buildWorkerMu       sync.Mutex
}

// Open opens (and migrates) the database at path. Use ":memory:" in tests.
func Open(ctx context.Context, path string) (*Store, error) {
	dsn := "file:" + path + "?_pragma=foreign_keys(1)&_pragma=journal_mode(WAL)&_pragma=busy_timeout(5000)"
	if path == ":memory:" {
		dsn = "file::memory:?_pragma=foreign_keys(1)"
	}
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	s := &Store{db: db}
	if err := s.migrateTo(ctx, 0); err != nil {
		db.Close()
		return nil, err
	}
	s.buildWorkerLockPath, err = buildWorkerLockPath(ctx, db)
	if err != nil {
		db.Close()
		return nil, fmt.Errorf("resolve build worker lock path: %w", err)
	}
	return s, nil
}

// DB is the catalog connection, for packages that keep their own tables in it (memory).
func (s *Store) DB() *sql.DB { return s.db }

// Backup writes a consistent copy of the database to path, which must not exist (VACUUM INTO).
func (s *Store) Backup(ctx context.Context, path string) error {
	_, err := s.db.ExecContext(ctx, "VACUUM INTO ?", path)
	return err
}

// Close closes the database without releasing an outstanding build worker lease.
func (s *Store) Close() error { return s.db.Close() }

// migrateTo applies the migrations up to version last, or all of them if last is 0. Each
// runs in its own transaction with foreign keys off, as SQLite's ALTER TABLE procedure
// asks, so a table can be rebuilt without cascading deletes; the keys are checked before
// the commit.
func (s *Store) migrateTo(ctx context.Context, last int) error {
	conn, err := s.db.Conn(ctx)
	if err != nil {
		return err
	}
	defer conn.Close()
	var current int
	if err := conn.QueryRowContext(ctx, "PRAGMA user_version").Scan(&current); err != nil {
		return err
	}
	names, err := fs.Glob(migrations, "migrations/*.sql")
	if err != nil {
		return err
	}
	sort.Strings(names)
	if current > len(names) {
		return fmt.Errorf("%w (schema %d; this one knows %d): run that version, or restore a backup of the database from before it", ErrNewerSchema, current, len(names))
	}
	if _, err := conn.ExecContext(ctx, "PRAGMA foreign_keys = OFF"); err != nil {
		return err
	}
	err = applyMigrations(ctx, conn, names, current, last)
	// The connection goes back to the pool; Open closes the database if this fails.
	_, onErr := conn.ExecContext(context.WithoutCancel(ctx), "PRAGMA foreign_keys = ON")
	return errors.Join(err, onErr)
}

func applyMigrations(ctx context.Context, conn *sql.Conn, names []string, current, last int) error {
	for i, name := range names {
		version := i + 1
		if version <= current || (last > 0 && version > last) {
			continue
		}
		body, err := migrations.ReadFile(name)
		if err != nil {
			return err
		}
		tx, err := conn.BeginTx(ctx, nil)
		if err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, string(body)); err != nil {
			tx.Rollback()
			return fmt.Errorf("migration %s: %w", name, err)
		}
		if err := foreignKeyCheck(ctx, tx); err != nil {
			tx.Rollback()
			return fmt.Errorf("migration %s: %w", name, err)
		}
		if _, err := tx.ExecContext(ctx, fmt.Sprintf("PRAGMA user_version = %d", version)); err != nil {
			tx.Rollback()
			return err
		}
		if err := tx.Commit(); err != nil {
			return err
		}
	}
	return nil
}

// foreignKeyCheck fails if any row breaks a foreign key.
func foreignKeyCheck(ctx context.Context, tx *sql.Tx) error {
	rows, err := tx.QueryContext(ctx, "PRAGMA foreign_key_check")
	if err != nil {
		return err
	}
	defer rows.Close()
	if rows.Next() {
		var table, parent string
		var rowid sql.NullInt64
		var fk int
		if err := rows.Scan(&table, &rowid, &parent, &fk); err != nil {
			return err
		}
		return fmt.Errorf("row %d of %s breaks its foreign key to %s", rowid.Int64, table, parent)
	}
	return rows.Err()
}

// NewID returns a short, lowercase, URL- and filename-safe random ID.
func NewID() string {
	b := make([]byte, 8)
	rand.Read(b)
	return strings.ToLower(strings.TrimRight(base32.StdEncoding.EncodeToString(b), "="))
}

func now() int64 { return time.Now().Unix() }

// --- environments ---------------------------------------------------------------------

// Environment is an isolated set of teammates, sandboxes, rules, secrets and memory.
type Environment struct {
	ID        string    `json:"id"`
	Name      string    `json:"name"`
	CreatedAt time.Time `json:"createdAt"`
	// BlockTrackers: the browser VMs' requests to the tracker list are denied (docs/network.md).
	BlockTrackers bool `json:"blockTrackers"`
	// SecurityLevel decides what happens to a request no rule covers (docs/network.md).
	SecurityLevel string `json:"securityLevel" enum:"locked,guarded,assisted,open" doc:"What happens when no rule matches: locked asks, guarded allows the curated list and asks, assisted also asks an AI reviewer, open allows public addresses"`
	// CuratedOff lists the curated list entries the environment turned off.
	CuratedOff []string `json:"curatedOff" doc:"IDs of the curated list entries turned off in this environment"`
}

// environmentCols selects an environment; scanEnvironment reads them in this order.
const environmentCols = "id, name, created_at, block_trackers, security_level, curated_off"

func scanEnvironment(row interface{ Scan(...any) error }) (Environment, error) {
	var e Environment
	var created int64
	var off string
	if err := row.Scan(&e.ID, &e.Name, &created, &e.BlockTrackers, &e.SecurityLevel, &off); err != nil {
		return e, err
	}
	e.CreatedAt = time.Unix(created, 0)
	e.CuratedOff = []string{}
	return e, json.Unmarshal([]byte(off), &e.CuratedOff)
}

// CreateEnvironment inserts an environment, which starts at the guarded level.
func (s *Store) CreateEnvironment(ctx context.Context, name string) (Environment, error) {
	e := Environment{ID: NewID(), Name: name, CreatedAt: time.Unix(now(), 0), BlockTrackers: true, SecurityLevel: LevelGuarded, CuratedOff: []string{}}
	_, err := s.db.ExecContext(ctx, "INSERT INTO environments (id, name, created_at, security_level) VALUES (?, ?, ?, ?)", e.ID, e.Name, e.CreatedAt.Unix(), e.SecurityLevel)
	if err != nil && strings.Contains(err.Error(), "UNIQUE") {
		return e, fmt.Errorf("an environment named %q: %w", name, ErrExists)
	}
	return e, err
}

// Environments lists environments by creation time.
func (s *Store) Environments(ctx context.Context) ([]Environment, error) {
	rows, err := s.db.QueryContext(ctx, "SELECT "+environmentCols+" FROM environments ORDER BY created_at, name")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Environment{}
	for rows.Next() {
		e, err := scanEnvironment(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

// Environment returns one environment.
func (s *Store) Environment(ctx context.Context, id string) (Environment, error) {
	e, err := scanEnvironment(s.db.QueryRowContext(ctx, "SELECT "+environmentCols+" FROM environments WHERE id = ?", id))
	if errors.Is(err, sql.ErrNoRows) {
		return e, ErrNotFound
	}
	return e, err
}

// --- sandboxes ------------------------------------------------------------------------

// Sandbox is the catalog record of a sandbox; runtime state lives in microsandbox.
type Sandbox struct {
	ID            string `json:"id"`
	EnvironmentID string `json:"environmentId"`
	BuildJobID    string `json:"-"`
	TemplateID    string `json:"templateId,omitempty"`
	TeammateID    string `json:"teammateId,omitempty" doc:"The teammate that owns the sandbox; empty for unowned sandboxes"`
	Name          string `json:"name"`
	Generation    int    `json:"generation"`
	CPUs          int    `json:"cpus"`
	MemoryMiB     int    `json:"memoryMiB"`
	MaxMemoryMiB  int    `json:"maxMemoryMiB"`
	// LiveCPUs and LiveMemoryMiB are what the running VM has while a resize waits for its next
	// start, which CPUs and MemoryMiB are; 0 when the VM has those.
	LiveCPUs      int       `json:"liveCpus,omitempty" doc:"The CPUs the running VM has while a change waits for its next start; absent when it has cpus"`
	LiveMemoryMiB int       `json:"liveMemoryMiB,omitempty" doc:"The memory the running VM has while a change waits for its next start; absent when it has memoryMiB"`
	WorkspaceMiB  int       `json:"workspaceMiB"`
	DockerMiB     int       `json:"dockerMiB"`
	HomeMiB       int       `json:"homeMiB,omitempty" doc:"The size of the home disk at /home/agent; absent for a sandbox whose home is on its root disk, as with a template built before home disks"`
	CreatedAt     time.Time `json:"createdAt"`
	DNSPort       int       `json:"-"` // loopback port of the sandbox's Studio resolver
	// Kind is empty for ordinary sandboxes, SandboxKindBrowser for a teammate's browser VM and
	// a system kind (system.go) for the VMs of subscription logins. Studio drives those and
	// never shows them as sandboxes.
	Kind string `json:"-"`
	// ForkedFromID is the sandbox this one was forked from, at ForkedFromGeneration, at
	// ForkedAt; empty for sandboxes that weren't forked. There is no foreign key: the source
	// may be deleted, and its delete event keeps its name.
	ForkedFromID         string    `json:"-"`
	ForkedFromGeneration int       `json:"-"`
	ForkedAt             time.Time `json:"-"`
	// StartedAt is when the VM last started, nil before its first start after the column
	// was added. It stays after a stop: show it only while the sandbox runs.
	StartedAt *time.Time `json:"startedAt,omitempty"`
	// BaseImage is the OCI reference of the base the VM was made from: its template's base, or
	// the default image. BaseVersion is the Studio release that brought it, empty when unknown.
	// Both are empty for sandboxes whose base isn't known yet.
	BaseImage   string `json:"-"`
	BaseVersion string `json:"-"`
}

// SandboxKindBrowser marks a teammate's browser VM (internal/browser).
const SandboxKindBrowser = "browser"

const sandboxCols = "id, environment_id, IFNULL(build_job_id, ''), IFNULL(template_id, ''), name, generation, cpus, memory_mib, max_memory_mib, workspace_mib, docker_mib, created_at, dns_port, IFNULL(teammate_id, ''), kind, IFNULL(forked_from_sandbox_id, ''), IFNULL(forked_from_generation, 0), IFNULL(forked_at, 0), IFNULL(started_at, 0), live_cpus, live_memory_mib, base_image, base_version, home_mib"

// firstDNSPort is where per-sandbox resolver ports start; each sandbox takes the lowest port no
// other sandbox has and nothing on the host listens on.
const firstDNSPort = 17100

func scanSandbox(row interface{ Scan(...any) error }) (Sandbox, error) {
	var sb Sandbox
	var created, forkedAt, startedAt int64
	err := row.Scan(&sb.ID, &sb.EnvironmentID, &sb.BuildJobID, &sb.TemplateID, &sb.Name, &sb.Generation, &sb.CPUs, &sb.MemoryMiB, &sb.MaxMemoryMiB, &sb.WorkspaceMiB, &sb.DockerMiB, &created, &sb.DNSPort, &sb.TeammateID, &sb.Kind,
		&sb.ForkedFromID, &sb.ForkedFromGeneration, &forkedAt, &startedAt, &sb.LiveCPUs, &sb.LiveMemoryMiB, &sb.BaseImage, &sb.BaseVersion, &sb.HomeMiB)
	sb.CreatedAt = time.Unix(created, 0)
	if forkedAt != 0 {
		sb.ForkedAt = time.Unix(forkedAt, 0)
	}
	if startedAt != 0 {
		started := time.Unix(startedAt, 0)
		sb.StartedAt = &started
	}
	return sb, err
}

// CreateSandbox inserts sb, assigning its ID, generation, DNS port and creation time.
func (s *Store) CreateSandbox(ctx context.Context, sb Sandbox) (Sandbox, error) {
	if sb.MaxMemoryMiB == 0 {
		sb.MaxMemoryMiB = sb.MemoryMiB
	}
	// Ordinary sandbox creation cannot claim build-job ownership. Build workers use
	// ReserveBuildSandbox, which validates the job and inserts both sides atomically.
	sb.BuildJobID = ""
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return sb, err
	}
	defer tx.Rollback()
	sb, err = insertSandbox(ctx, tx, sb)
	if err != nil {
		return sb, err
	}
	if err := tx.Commit(); err != nil {
		return sb, err
	}
	return sb, nil
}

func insertSandbox(ctx context.Context, tx *sql.Tx, sb Sandbox) (Sandbox, error) {
	if sb.MaxMemoryMiB == 0 {
		sb.MaxMemoryMiB = sb.MemoryMiB
	}
	sb.BuildJobID = ""
	if sb.TemplateID != "" {
		// A template sandbox runs its template's base, whatever the caller says.
		var state string
		sb.BaseVersion = ""
		err := tx.QueryRowContext(ctx, "SELECT state, base_ref FROM templates WHERE environment_id = ? AND id = ?", sb.EnvironmentID, sb.TemplateID).Scan(&state, &sb.BaseImage)
		if errors.Is(err, sql.ErrNoRows) {
			return sb, ErrNotFound
		}
		if err != nil {
			return sb, err
		}
		if state != TemplateStateReady {
			return sb, ErrConflict
		}
	}
	if sb.TeammateID != "" {
		err := tx.QueryRowContext(ctx, "SELECT 1 FROM teammates WHERE environment_id = ? AND id = ?", sb.EnvironmentID, sb.TeammateID).Scan(new(int))
		if errors.Is(err, sql.ErrNoRows) {
			return sb, fmt.Errorf("teammate %s: %w", sb.TeammateID, ErrNotFound)
		}
		if err != nil {
			return sb, err
		}
	}
	sb.ID, sb.Generation, sb.CreatedAt = NewID(), 1, time.Unix(now(), 0)
	if sb.ForkedFromID != "" {
		// A fork is made when its record is: the two times are the same.
		sb.ForkedAt = sb.CreatedAt
	}
	port, err := freeDNSPort(ctx, tx)
	if err != nil {
		return sb, err
	}
	sb.DNSPort = port
	_, err = tx.ExecContext(ctx, "INSERT INTO sandboxes (id, environment_id, build_job_id, template_id, name, generation, cpus, memory_mib, max_memory_mib, workspace_mib, docker_mib, created_at, dns_port, teammate_id, kind, forked_from_sandbox_id, forked_from_generation, forked_at, base_image, base_version, home_mib) VALUES (?, ?, NULL, NULLIF(?, ''), ?, ?, ?, ?, ?, ?, ?, ?, ?, NULLIF(?, ''), ?, NULLIF(?, ''), NULLIF(?, 0), NULLIF(?, 0), ?, ?, ?)",
		sb.ID, sb.EnvironmentID, sb.TemplateID, sb.Name, sb.Generation, sb.CPUs, sb.MemoryMiB, sb.MaxMemoryMiB, sb.WorkspaceMiB, sb.DockerMiB, sb.CreatedAt.Unix(), sb.DNSPort, sb.TeammateID, sb.Kind,
		sb.ForkedFromID, sb.ForkedFromGeneration, forkedAtUnix(sb.ForkedAt), sb.BaseImage, sb.BaseVersion, sb.HomeMiB)
	if err != nil && strings.Contains(err.Error(), "UNIQUE") {
		return sb, fmt.Errorf("a sandbox named %q: %w", sb.Name, ErrExists)
	}
	return sb, err
}

func forkedAtUnix(t time.Time) int64 {
	if t.IsZero() {
		return 0
	}
	return t.Unix()
}

// Sandboxes lists the sandboxes of an environment.
func (s *Store) Sandboxes(ctx context.Context, envID string) ([]Sandbox, error) {
	rows, err := s.db.QueryContext(ctx, "SELECT "+sandboxCols+" FROM sandboxes WHERE environment_id = ? ORDER BY name", envID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Sandbox{}
	for rows.Next() {
		sb, err := scanSandbox(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, sb)
	}
	return out, rows.Err()
}

// AllSandboxes lists sandboxes across environments; only for startup reconciliation.
func (s *Store) AllSandboxes(ctx context.Context) ([]Sandbox, error) {
	rows, err := s.db.QueryContext(ctx, "SELECT "+sandboxCols+" FROM sandboxes ORDER BY created_at")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Sandbox{}
	for rows.Next() {
		sb, err := scanSandbox(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, sb)
	}
	return out, rows.Err()
}

type dnsPortQueryer interface {
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
}

func freeDNSPort(ctx context.Context, q dnsPortQueryer) (int, error) {
	rows, err := q.QueryContext(ctx, "SELECT dns_port FROM sandboxes")
	if err != nil {
		return 0, err
	}
	defer rows.Close()
	taken := map[int]bool{}
	for rows.Next() {
		var p int
		if err := rows.Scan(&p); err != nil {
			return 0, err
		}
		taken[p] = true
	}
	port := firstDNSPort
	for taken[port] || !portFree(port) {
		port++
	}
	return port, rows.Err()
}

// portFree reports whether nothing on the host listens on the loopback port, over UDP or TCP.
// Another Studio on the same machine (a development one, say) numbers its sandboxes' resolvers
// from the same first port, and a resolver that can't bind fails its sandbox's start.
func portFree(port int) bool {
	addr := net.JoinHostPort("127.0.0.1", strconv.Itoa(port))
	udp, err := net.ListenPacket("udp", addr)
	if err != nil {
		return false
	}
	_ = udp.Close()
	tcp, err := net.Listen("tcp", addr)
	if err != nil {
		return false
	}
	_ = tcp.Close()
	return true
}

// LookupSandbox finds a sandbox in any environment. Only the gateway uses it, to identify
// a sandbox that has already authenticated with its own credentials.
func (s *Store) LookupSandbox(ctx context.Context, id string) (Sandbox, error) {
	sb, err := scanSandbox(s.db.QueryRowContext(ctx, "SELECT "+sandboxCols+" FROM sandboxes WHERE id = ?", id))
	if errors.Is(err, sql.ErrNoRows) {
		return sb, ErrNotFound
	}
	return sb, err
}

// Sandbox returns one sandbox of an environment.
func (s *Store) Sandbox(ctx context.Context, envID, id string) (Sandbox, error) {
	sb, err := scanSandbox(s.db.QueryRowContext(ctx, "SELECT "+sandboxCols+" FROM sandboxes WHERE environment_id = ? AND id = ?", envID, id))
	if errors.Is(err, sql.ErrNoRows) {
		return sb, ErrNotFound
	}
	return sb, err
}

// MarkSandboxStarted records when a sandbox's VM last started. The VM has the CPUs and memory
// it was configured with, so a resize that waited for the start is done.
func (s *Store) MarkSandboxStarted(ctx context.Context, envID, id string, at time.Time) error {
	res, err := s.db.ExecContext(ctx, "UPDATE sandboxes SET started_at = ?, live_cpus = 0, live_memory_mib = 0 WHERE environment_id = ? AND id = ?", at.Unix(), envID, id)
	if err != nil {
		return err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if n == 0 {
		return ErrNotFound
	}
	return nil
}

// SetSandboxBase records the base image a sandbox runs, and the release that brought it, for a
// sandbox made before Studio recorded them.
func (s *Store) SetSandboxBase(ctx context.Context, envID, id, image, version string) error {
	res, err := s.db.ExecContext(ctx, "UPDATE sandboxes SET base_image = ?, base_version = ? WHERE environment_id = ? AND id = ?", image, version, envID, id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

// SandboxSize is what a resize records: the CPUs and memory a sandbox's VM starts with, its
// memory ceiling, and what the running VM has while the change waits for its next start (0
// when it has the former).
type SandboxSize struct {
	CPUs, MemoryMiB, MaxMemoryMiB int
	LiveCPUs, LiveMemoryMiB       int
}

// SetSandboxResources records a sandbox's size.
func (s *Store) SetSandboxResources(ctx context.Context, envID, id string, size SandboxSize) error {
	res, err := s.db.ExecContext(ctx, "UPDATE sandboxes SET cpus = ?, memory_mib = ?, max_memory_mib = ?, live_cpus = ?, live_memory_mib = ? WHERE environment_id = ? AND id = ?",
		size.CPUs, size.MemoryMiB, size.MaxMemoryMiB, size.LiveCPUs, size.LiveMemoryMiB, envID, id)
	if err != nil {
		return err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if n == 0 {
		return ErrNotFound
	}
	return nil
}

// DeleteSandbox removes a sandbox record.
func (s *Store) DeleteSandbox(ctx context.Context, envID, id string) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var owner string
	err = tx.QueryRowContext(ctx, "SELECT IFNULL(build_job_id, '') FROM sandboxes WHERE environment_id = ? AND id = ?", envID, id).Scan(&owner)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrNotFound
	}
	if err != nil {
		return err
	}
	if owner != "" {
		return ErrConflict
	}
	res, err := tx.ExecContext(ctx, "DELETE FROM sandboxes WHERE environment_id = ? AND id = ? AND build_job_id IS NULL", envID, id)
	if err != nil {
		return err
	}
	if err := exactlyOneRow(res); err != nil {
		return err
	}
	return tx.Commit()
}
