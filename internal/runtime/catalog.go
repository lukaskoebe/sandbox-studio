package runtime

import (
	"context"
	"errors"
	"fmt"
	"strings"

	msb "github.com/superradcompany/microsandbox/sdk/go"
)

// ErrCatalog marks a failure of msb's own database, ~/.microsandbox/db/msb.db: every msb call
// fails until it is fixed.
var ErrCatalog = errors.New("microsandbox's database can't be used")

// RepairOnRestart is how to fix a damaged database.
const RepairOnRestart = "Restart Studio: it moves the damaged database aside, keeping it, and rebuilds it from what it can still read"

// CatalogError is an msb call that failed on msb's database, with how to fix that.
type CatalogError struct {
	Err error
	Fix string
}

func (e *CatalogError) Error() string { return e.Err.Error() + ". " + e.Fix }

func (e *CatalogError) Unwrap() []error { return []error{ErrCatalog, e.Err} }

// catalogError returns err as a *CatalogError if msb failed on its database, else err.
func catalogError(err error) error {
	if err == nil {
		return nil
	}
	if _, done := errors.AsType[*CatalogError](err); done {
		return err
	}
	msg := err.Error()
	switch {
	case strings.Contains(msg, "file is not a database"), strings.Contains(msg, "database disk image is malformed"):
		return &CatalogError{Err: fmt.Errorf("microsandbox's database is damaged: %w", err),
			Fix: RepairOnRestart}
	case strings.Contains(msg, "database schema is newer than this msb"):
		v := MSBVersion()
		return &CatalogError{Err: fmt.Errorf("a newer msb than the %s Studio uses has upgraded microsandbox's database: %w", v, err),
			Fix: fmt.Sprintf("Run `msb self downgrade %s` with the msb that upgraded it (`~/.microsandbox/bin/msb --version` shows if that is Studio's); "+
				"it backs the database up first. Then restart Studio", v)}
	case strings.Contains(msg, "permits path substitution"), strings.Contains(msg, "mutation-granting ACL"),
		strings.Contains(msg, "is not owned by this account"), strings.Contains(msg, "cannot make catalog storage"):
		// msb 0.7.12 and later keep their database only where no other account can swap it.
		return &CatalogError{Err: fmt.Errorf("microsandbox refuses to use its database where other accounts could change it: %w", err),
			Fix: "Make the directory the message names writable only by you, such as with `chmod g-w,o-w` on it, " +
				"or start Studio with MSB_HOME set to a directory that only you can write to. Then restart Studio"}
	case strings.Contains(msg, "/db/msb.db"):
		return &CatalogError{Err: fmt.Errorf("microsandbox's database can't be opened: %w", err),
			Fix: "Check that ~/.microsandbox/db is yours and writable, and that the disk has room"}
	}
	return err
}

// CheckCatalog makes a cheap msb call that reads its database. It returns a *CatalogError if
// the database is unusable, nil if the call succeeds, and other failures as they are.
func CheckCatalog(ctx context.Context) error {
	_, err := msb.ListSandboxesWith(ctx, msb.WithListLimit(1))
	return catalogError(err)
}

// ReindexSnapshots rebuilds msb's index of the snapshots on disk, which hold the
// checkpoints, after its database was rebuilt. It returns how many it indexed.
func ReindexSnapshots(ctx context.Context) (uint32, error) {
	return msb.Snapshot.Reindex(ctx, "")
}
