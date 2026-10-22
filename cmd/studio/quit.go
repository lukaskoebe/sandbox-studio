package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"path/filepath"
	"time"

	"github.com/lukaskoebe/sandbox-studio/internal/paths"
	"github.com/lukaskoebe/sandbox-studio/internal/store"
)

// quitWait is how long `studio quit` waits for Studio to let go of its data directory: a
// shutdown waits up to 45 s for template builds to record their cleanup.
const quitWait = 90 * time.Second

// runQuit is `studio quit`: it asks the running Studio to shut down as Ctrl-C does, with the
// local owner's control credential, and waits until Studio released its data directory.
func runQuit(ctx context.Context, w io.Writer, addr string) int {
	p, err := paths.Default()
	if err != nil {
		fmt.Fprintln(w, err)
		return 1
	}
	lock := filepath.Join(p.Data, "studio.lock")
	if release, err := store.TryLock(lock); err == nil {
		_ = release()
		fmt.Fprintln(w, "Studio isn't running.")
		return 0
	}
	if err := requestQuit(ctx, addr); err != nil {
		fmt.Fprintln(w, err)
		return 1
	}
	deadline := time.Now().Add(quitWait)
	for time.Now().Before(deadline) {
		if release, err := store.TryLock(lock); err == nil {
			_ = release()
			fmt.Fprintln(w, "Studio stopped. Sandboxes keep running.")
			return 0
		}
		time.Sleep(250 * time.Millisecond)
	}
	fmt.Fprintf(w, "Studio is still shutting down after %s.\n", quitWait)
	return 1
}

// requestQuit sends POST /api/auth/quit with the control credential.
func requestQuit(ctx context.Context, addr string) error {
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		return err
	}
	if ip := net.ParseIP(host); ip == nil || !ip.IsLoopback() {
		return errors.New("quit requires a loopback server address")
	}
	control, err := controlCredential(ctx)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, "http://"+addr+"/api/auth/quit", nil)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+control)
	transport := &http.Transport{Proxy: nil}
	defer transport.CloseIdleConnections()
	resp, err := (&http.Client{Transport: transport, Timeout: 5 * time.Second}).Do(req)
	if err != nil {
		return fmt.Errorf("connect to Studio on %s (is it running with another -addr?): %w", addr, err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusAccepted {
		return fmt.Errorf("Studio refused to stop (HTTP %d); it may be older than this binary", resp.StatusCode)
	}
	return nil
}
