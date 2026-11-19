package main

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"net"
	"path/filepath"
	"strings"
	"testing"

	"github.com/lukaskoebe/sandbox-studio/internal/paths"
	"github.com/lukaskoebe/sandbox-studio/internal/store"
)

// A restart into a binary older than the running Studio's database is refused before it stops
// that Studio, which this one couldn't replace.
func TestRestartRefusesAnOlderBinary(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("SANDBOX_STUDIO_HOME", dir)
	p := paths.Paths{Data: dir}
	ctx := context.Background()
	st, err := store.Open(ctx, p.DB())
	if err != nil {
		t.Fatal(err)
	}
	st.Close()
	db, _ := sql.Open("sqlite", "file:"+p.DB())
	if _, err := db.ExecContext(ctx, "PRAGMA user_version = 100000"); err != nil {
		t.Fatal(err)
	}
	db.Close()
	release, err := store.TryLock(filepath.Join(dir, "studio.lock")) // the running Studio
	if err != nil {
		t.Fatal(err)
	}
	defer release()

	var out bytes.Buffer
	if code := runRestart(ctx, &out, "127.0.0.1:1"); code != 1 || !strings.Contains(out.String(), "older than the running one") || strings.Contains(out.String(), "Stopping") {
		t.Fatalf("restart = %d: %s", code, out.String())
	}
}

// A Studio that exits while starting ends the wait at once instead of after StartWait.
func TestAwaitServingEndsWhenStudioExits(t *testing.T) {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := l.Addr().String()
	l.Close() // nothing answers there
	exited := make(chan error, 1)
	exited <- errors.New("exit status 1")
	var out bytes.Buffer
	if awaitServing(context.Background(), &out, addr, t.TempDir(), exited) || !strings.Contains(out.String(), "stopped while starting (exit status 1)") {
		t.Fatalf("await: %s", out.String())
	}
}
