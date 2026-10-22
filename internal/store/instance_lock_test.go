package store

import (
	"errors"
	"path/filepath"
	"testing"
)

func TestTryLockHoldsOffASecondHolder(t *testing.T) {
	path := filepath.Join(t.TempDir(), "studio.lock")
	release, err := TryLock(path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := TryLock(path); !errors.Is(err, ErrLocked) {
		t.Fatalf("second TryLock = %v, want ErrLocked", err)
	}
	if err := release(); err != nil {
		t.Fatal(err)
	}
	again, err := TryLock(path)
	if err != nil {
		t.Fatalf("TryLock after release: %v", err)
	}
	if err := again(); err != nil {
		t.Fatal(err)
	}
}
