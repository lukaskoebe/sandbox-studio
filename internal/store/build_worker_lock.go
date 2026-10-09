package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"path/filepath"
	"sync"
)

// buildWorkerLockPath reads SQLite's main database filename, makes it
// absolute, and resolves symlinks so aliases of one catalog share a lock.
// SQLite reports an empty filename for in-memory databases.
func buildWorkerLockPath(ctx context.Context, db *sql.DB) (string, error) {
	rows, err := db.QueryContext(ctx, "PRAGMA database_list")
	if err != nil {
		return "", err
	}
	defer rows.Close()

	var filename string
	for rows.Next() {
		var sequence int
		var name, file string
		if err := rows.Scan(&sequence, &name, &file); err != nil {
			return "", err
		}
		if name == "main" {
			filename = file
			break
		}
	}
	if err := rows.Err(); err != nil {
		return "", err
	}
	if filename == "" {
		return "", nil
	}

	absolute, err := filepath.Abs(filename)
	if err != nil {
		return "", err
	}
	resolved, err := filepath.EvalSymlinks(absolute)
	if err != nil {
		return "", err
	}
	resolved, err = filepath.Abs(resolved)
	if err != nil {
		return "", err
	}
	return resolved + ".build-lock", nil
}

// AcquireBuildWorker takes a nonblocking process-wide worker lease for this
// catalog. The returned release function owns the lock independently of the
// Store and remains valid after Store.Close.
func (s *Store) AcquireBuildWorker(ctx context.Context) (func() error, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if s.buildWorkerLockPath == "" {
		if !s.buildWorkerMu.TryLock() {
			return nil, fmt.Errorf("another build worker holds the in-memory store lock: %w", ErrConflict)
		}
		release := onceBuildWorkerRelease(func() error {
			s.buildWorkerMu.Unlock()
			return nil
		})
		if err := ctx.Err(); err != nil {
			return nil, errors.Join(err, release())
		}
		return release, nil
	}

	release, err := tryBuildWorkerFileLock(s.buildWorkerLockPath)
	if err != nil {
		if isBuildWorkerFileLockConflict(err) {
			return nil, fmt.Errorf("another build worker holds the store lock: %w", ErrConflict)
		}
		return nil, fmt.Errorf("acquire build worker lock: %w", err)
	}
	release = onceBuildWorkerRelease(release)
	if err := ctx.Err(); err != nil {
		return nil, errors.Join(err, release())
	}
	return release, nil
}

func onceBuildWorkerRelease(release func() error) func() error {
	var once sync.Once
	var releaseErr error
	return func() error {
		once.Do(func() { releaseErr = release() })
		return releaseErr
	}
}
