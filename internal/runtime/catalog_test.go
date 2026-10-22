package runtime

import (
	"errors"
	"strings"
	"testing"
)

func TestCatalogErrorNamesTheFix(t *testing.T) {
	for _, tc := range []struct{ msg, want, fix string }{
		{"connect to /h/.microsandbox/db/msb.db: error returned from database: (code: 11) database disk image is malformed", "is damaged", "Restart Studio"},
		{"connect to /h/.microsandbox/db/msb.db: error returned from database: (code: 26) file is not a database", "is damaged", "Restart Studio"},
		{"runtime error: database schema is newer than this msb binary or has an unknown migration prefix; refusing to change an unrecognized catalog", "a newer msb", "msb self downgrade " + MSBVersion()},
		{"connect to /h/.microsandbox/db/msb.db: unable to open database file", "can't be opened", "writable"},
	} {
		err := catalogError(errors.New(tc.msg))
		var ce *CatalogError
		if !errors.As(err, &ce) || !errors.Is(err, ErrCatalog) {
			t.Fatalf("%s: %v is no catalog error", tc.msg, err)
		}
		if !strings.Contains(ce.Err.Error(), tc.want) || !strings.Contains(ce.Fix, tc.fix) || !strings.Contains(err.Error(), tc.msg) {
			t.Fatalf("%s: got %q / %q", tc.msg, ce.Err, ce.Fix)
		}
	}
	other := errors.New("sandbox not found")
	if catalogError(other) != other || catalogError(nil) != nil {
		t.Fatal("other errors must pass through")
	}
}

func TestCatalogBusy(t *testing.T) {
	for msg, want := range map[string]bool{
		"Database error: error returned from database: (code: 5) database is locked": true,
		"database table is locked":                            true,
		"pool timed out while waiting for an open connection": true,
		"database disk image is malformed":                    false,
		"sandbox not found: ss-a-g1":                          false,
	} {
		if got := catalogBusy(errors.New(msg)); got != want {
			t.Errorf("catalogBusy(%q) = %v, want %v", msg, got, want)
		}
	}
}
