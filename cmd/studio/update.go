package main

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/lukaskoebe/sandbox-studio/internal/login"
	"github.com/lukaskoebe/sandbox-studio/internal/paths"
	"github.com/lukaskoebe/sandbox-studio/internal/store"
	"github.com/lukaskoebe/sandbox-studio/internal/updates"
	"github.com/lukaskoebe/sandbox-studio/internal/version"
)

// updateRestart ends run so that Studio restarts into a swapped binary, or into the old one
// again after a rollback (restartForUpdate).
type updateRestart struct{ resume updates.Resume }

func (*updateRestart) Error() string { return "restarting for an update" }

// updatesDir holds the downloads, the database backup and the install record.
func updatesDir(p paths.Paths) string { return filepath.Join(p.Data, "updates") }

// newInstaller returns the installer of automatic updates, or nil where Studio can't replace
// itself: a development build, or a binary it can't find.
func newInstaller(ctx context.Context, p paths.Paths, st *store.Store, logins *login.Service, restart func(), log *slog.Logger) *updates.Installer {
	if !releaseTag.MatchString(version.Version) || strings.HasSuffix(version.Version, "-dirty") {
		return nil
	}
	exe, err := os.Executable()
	if err == nil {
		exe, err = filepath.EvalSymlinks(exe)
	}
	if err != nil {
		log.Warn("automatic updates are unavailable: the running binary is unknown", "err", err)
		return nil
	}
	if err := os.MkdirAll(updatesDir(p), 0o700); err != nil {
		log.Warn("automatic updates are unavailable", "err", err)
		return nil
	}
	return &updates.Installer{
		Dir: updatesDir(p), Exe: exe, Current: version.Version, Context: ctx, DB: p.DB(), Backup: st.Backup, Restart: restart, Log: log,
		Idle: func(ctx context.Context) (bool, string, error) { return idleForUpdate(ctx, st, logins) },
	}
}

// idleForUpdate reports whether a restart would cut nothing off: no chat turn, template
// build or sign-in is running.
func idleForUpdate(ctx context.Context, st *store.Store, logins *login.Service) (bool, string, error) {
	chats, err := st.ChatsInState(ctx, store.ChatRunning)
	if err != nil {
		return false, "", err
	}
	if n := len(chats); n > 0 {
		return false, plural(n, "a chat is", "chats are") + " running", nil
	}
	jobs, err := st.RecoverableBuildJobs(ctx)
	if err != nil {
		return false, "", err
	}
	for _, j := range jobs {
		switch j.Status {
		case store.BuildQueued, store.BuildPreparing, store.BuildSettingUp, store.BuildExporting:
			return false, "a template build is running", nil
		}
	}
	if logins.Busy() {
		return false, "a sign-in is open", nil
	}
	return true, "", nil
}

func plural(n int, one, many string) string {
	if n == 1 {
		return one
	}
	return fmt.Sprintf("%d %s", n, many)
}

// markServed records that this binary serves, once its health check answers: the install
// that put it in place is done.
func markServed(addr string, resume updates.Resume, in *updates.Installer, served chan<- struct{}, log *slog.Logger) {
	if !resume.Waiting() {
		return
	}
	go func() {
		client := &http.Client{Timeout: 2 * time.Second}
		for range 60 {
			resp, err := client.Get("http://" + addr + "/api/auth/status")
			if err == nil {
				resp.Body.Close()
				if resp.StatusCode == http.StatusOK {
					p := resume.Served(log)
					if in != nil {
						in.SetProgress(p)
					}
					close(served)
					return
				}
			}
			time.Sleep(time.Second)
		}
	}()
}

// restartForUpdate runs after Studio shut down for an update. Under a service manager it
// exits for the manager to start the binary now in place; otherwise it starts the new
// binary itself and watches it until it serves, or rolls it back. It returns the exit status.
func restartForUpdate(resume updates.Resume, log *slog.Logger) int {
	if updates.Supervised() {
		return updates.ExitRestart
	}
	// The restarted Studio doesn't open the browser again.
	args := append([]string{"-no-browser"}, os.Args[1:]...)
	if resume.Exit {
		if err := updates.StartAgain(resume.Exe, args); err != nil {
			log.Error("starting Studio again after rolling back the update", "err", err)
			return 1
		}
		return 0
	}
	p, err := paths.Default()
	if err == nil {
		err = updates.RestartUnsupervised(updatesDir(p), args, log)
	}
	if err != nil {
		log.Error("restarting into the update", "err", err)
		return 1
	}
	return 0
}
