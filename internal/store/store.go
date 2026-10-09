// Package store is the SQLite catalog. Every query that touches environment-owned
// data takes an environment ID, so rows can never leak across environments.
package store

import (
	"context"
	"crypto/rand"
	"database/sql"
	"embed"
	"encoding/base32"
	"errors"
	"fmt"
	"io/fs"
	"sort"
	"strings"
	"time"

	_ "modernc.org/sqlite"
)

//go:embed migrations/*.sql
var migrations embed.FS

// ErrNotFound is returned when a row does not exist in the given environment.
var ErrNotFound = errors.New("not found")

// ErrExists is returned when a name is already taken.
var ErrExists = errors.New("already exists")

// Store wraps the catalog database.
type Store struct{ db *sql.DB }

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
	if err := s.migrate(ctx); err != nil {
		db.Close()
		return nil, err
	}
	return s, nil
}

// Close closes the database.
func (s *Store) Close() error { return s.db.Close() }

func (s *Store) migrate(ctx context.Context) error {
	var current int
	if err := s.db.QueryRowContext(ctx, "PRAGMA user_version").Scan(&current); err != nil {
		return err
	}
	names, err := fs.Glob(migrations, "migrations/*.sql")
	if err != nil {
		return err
	}
	sort.Strings(names)
	for i, name := range names {
		version := i + 1
		if version <= current {
			continue
		}
		body, err := migrations.ReadFile(name)
		if err != nil {
			return err
		}
		tx, err := s.db.BeginTx(ctx, nil)
		if err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, string(body)); err != nil {
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

// NewID returns a short, lowercase, URL- and filename-safe random ID.
func NewID() string {
	b := make([]byte, 8)
	rand.Read(b)
	return strings.ToLower(strings.TrimRight(base32.StdEncoding.EncodeToString(b), "="))
}

func now() int64 { return time.Now().Unix() }

// --- environments ---------------------------------------------------------------------

// Environment is an isolated set of personas, sandboxes, rules, secrets and memory.
type Environment struct {
	ID        string    `json:"id"`
	Name      string    `json:"name"`
	CreatedAt time.Time `json:"createdAt"`
}

// CreateEnvironment inserts an environment.
func (s *Store) CreateEnvironment(ctx context.Context, name string) (Environment, error) {
	e := Environment{ID: NewID(), Name: name, CreatedAt: time.Unix(now(), 0)}
	_, err := s.db.ExecContext(ctx, "INSERT INTO environments (id, name, created_at) VALUES (?, ?, ?)", e.ID, e.Name, e.CreatedAt.Unix())
	if err != nil && strings.Contains(err.Error(), "UNIQUE") {
		return e, fmt.Errorf("an environment named %q: %w", name, ErrExists)
	}
	return e, err
}

// Environments lists environments by creation time.
func (s *Store) Environments(ctx context.Context) ([]Environment, error) {
	rows, err := s.db.QueryContext(ctx, "SELECT id, name, created_at FROM environments ORDER BY created_at, name")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Environment{}
	for rows.Next() {
		var e Environment
		var created int64
		if err := rows.Scan(&e.ID, &e.Name, &created); err != nil {
			return nil, err
		}
		e.CreatedAt = time.Unix(created, 0)
		out = append(out, e)
	}
	return out, rows.Err()
}

// Environment returns one environment.
func (s *Store) Environment(ctx context.Context, id string) (Environment, error) {
	var e Environment
	var created int64
	err := s.db.QueryRowContext(ctx, "SELECT id, name, created_at FROM environments WHERE id = ?", id).Scan(&e.ID, &e.Name, &created)
	if errors.Is(err, sql.ErrNoRows) {
		return e, ErrNotFound
	}
	e.CreatedAt = time.Unix(created, 0)
	return e, err
}

// --- sandboxes ------------------------------------------------------------------------

// Sandbox is the catalog record of a sandbox; runtime state lives in microsandbox.
type Sandbox struct {
	ID            string    `json:"id"`
	EnvironmentID string    `json:"environmentId"`
	Name          string    `json:"name"`
	Generation    int       `json:"generation"`
	CPUs          int       `json:"cpus"`
	MemoryMiB     int       `json:"memoryMiB"`
	MaxMemoryMiB  int       `json:"maxMemoryMiB"`
	WorkspaceMiB  int       `json:"workspaceMiB"`
	DockerMiB     int       `json:"dockerMiB"`
	CreatedAt     time.Time `json:"createdAt"`
	DNSPort       int       `json:"-"` // loopback port of the sandbox's Studio resolver
}

const sandboxCols = "id, environment_id, name, generation, cpus, memory_mib, max_memory_mib, workspace_mib, docker_mib, created_at, dns_port"

// firstDNSPort is where per-sandbox resolver ports start; each sandbox takes the lowest free one.
const firstDNSPort = 17100

func scanSandbox(row interface{ Scan(...any) error }) (Sandbox, error) {
	var sb Sandbox
	var created int64
	err := row.Scan(&sb.ID, &sb.EnvironmentID, &sb.Name, &sb.Generation, &sb.CPUs, &sb.MemoryMiB, &sb.MaxMemoryMiB, &sb.WorkspaceMiB, &sb.DockerMiB, &created, &sb.DNSPort)
	sb.CreatedAt = time.Unix(created, 0)
	return sb, err
}

// CreateSandbox inserts sb, assigning its ID, generation, DNS port and creation time.
func (s *Store) CreateSandbox(ctx context.Context, sb Sandbox) (Sandbox, error) {
	if sb.MaxMemoryMiB == 0 {
		sb.MaxMemoryMiB = sb.MemoryMiB
	}
	sb.ID, sb.Generation, sb.CreatedAt = NewID(), 1, time.Unix(now(), 0)
	port, err := s.freeDNSPort(ctx)
	if err != nil {
		return sb, err
	}
	sb.DNSPort = port
	_, err = s.db.ExecContext(ctx, "INSERT INTO sandboxes ("+sandboxCols+") VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)",
		sb.ID, sb.EnvironmentID, sb.Name, sb.Generation, sb.CPUs, sb.MemoryMiB, sb.MaxMemoryMiB, sb.WorkspaceMiB, sb.DockerMiB, sb.CreatedAt.Unix(), sb.DNSPort)
	if err != nil && strings.Contains(err.Error(), "UNIQUE") {
		return sb, fmt.Errorf("a sandbox named %q: %w", sb.Name, ErrExists)
	}
	return sb, err
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

func (s *Store) freeDNSPort(ctx context.Context) (int, error) {
	rows, err := s.db.QueryContext(ctx, "SELECT dns_port FROM sandboxes")
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
	for taken[port] {
		port++
	}
	return port, rows.Err()
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

// DeleteSandbox removes a sandbox record.
func (s *Store) DeleteSandbox(ctx context.Context, envID, id string) error {
	res, err := s.db.ExecContext(ctx, "DELETE FROM sandboxes WHERE environment_id = ? AND id = ?", envID, id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}
