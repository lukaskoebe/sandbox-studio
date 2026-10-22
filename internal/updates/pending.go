package updates

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"time"
)

// The states of an install record.
const (
	StateSwapped    = "swapped"     // the new binary is in place and hasn't served yet
	StateInstalled  = "installed"   // the new binary served
	StateRolledBack = "rolled-back" // the old binary and the database backup came back
)

// ExitRestart is the exit status that has a service manager start Studio again: systemd's
// unit restarts it on failure (internal/autostart).
const ExitRestart = 75

const pendingFile = "install.json"

// Pending is the record of an install from the swap on, in the updates directory. The
// binary that starts next reads it (ResumeInstall): the new one marks it installed once it
// serves, and whichever binary sees the new one fail puts the old binary and the database
// back.
type Pending struct {
	Dir        string    `json:"dir"` // the updates directory, which holds the record
	From       string    `json:"from"`
	To         string    `json:"to"`
	Exe        string    `json:"exe"`
	Old        string    `json:"old"`
	Backup     string    `json:"backup"`
	DB         string    `json:"db"`
	Supervised bool      `json:"supervised"`
	State      string    `json:"state"`
	Attempts   int       `json:"attempts"`
	At         time.Time `json:"at"`
	Error      string    `json:"error,omitempty"`
}

// Supervised reports whether a service manager restarts Studio when it exits: systemd sets
// INVOCATION_ID. launchd's agent doesn't restart it (internal/autostart).
func Supervised() bool { return os.Getenv("INVOCATION_ID") != "" }

// progress is how the UI shows a record.
func (p Pending) progress() *InstallProgress {
	pr := &InstallProgress{Version: p.To, From: p.From, At: p.At, Error: p.Error}
	switch p.State {
	case StateInstalled:
		pr.Phase = PhaseInstalled
	case StateRolledBack:
		pr.Phase = PhaseFailed
	default:
		pr.Phase = PhaseRestarting
	}
	return pr
}

func readPending(dir string) (Pending, bool, error) {
	var p Pending
	data, err := os.ReadFile(filepath.Join(dir, pendingFile))
	if errors.Is(err, os.ErrNotExist) {
		return p, false, nil
	}
	if err != nil {
		return p, false, err
	}
	return p, true, json.Unmarshal(data, &p)
}

func writePending(dir string, p Pending) error {
	data, err := json.MarshalIndent(p, "", "  ")
	if err != nil {
		return err
	}
	tmp := filepath.Join(dir, pendingFile+".tmp")
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, filepath.Join(dir, pendingFile))
}

// Resume is what a starting Studio learned from the install record.
type Resume struct {
	// Progress is the record as the UI shows it; nil without one.
	Progress *InstallProgress
	// Exit is set when the record rolled back an install this binary is: Studio exits with
	// ExitRestart, or starts the old binary when nothing supervises it, instead of serving.
	Exit bool
	// Exe is the binary to start again after Exit.
	Exe string
	// waiting is the record of this binary's install, until it serves.
	waiting *Pending
	dir     string
}

// ResumeInstall reads the install record before Studio opens its database. A new binary
// counts its start; under a service manager, one that has started maxAttempts times without
// serving rolls back. The old binary, started while its replacement hadn't served, puts its
// database back. A record of another version is stale and removed.
func ResumeInstall(dir, version string, log *slog.Logger) (Resume, error) {
	r := Resume{dir: dir}
	p, ok, err := readPending(dir)
	if err != nil || !ok {
		return r, err
	}
	switch {
	case p.State == StateInstalled && version == p.To:
		// The old binary may have run until after the new one served (the watcher).
		os.Remove(p.Old)
	case p.State != StateSwapped:
	case version == p.To:
		p.Attempts++
		if p.Supervised && p.Attempts > maxAttempts {
			p = rollback(p, fmt.Sprintf("Studio %s started %d times without serving", p.To, maxAttempts), log)
			r.Exit, r.Exe = true, p.Exe
			break
		}
		if err := writePending(dir, p); err != nil {
			return r, err
		}
		r.waiting = &p
	case version == p.From:
		p = rollback(p, fmt.Sprintf("Studio %s started again before %s served", p.From, p.To), log)
	default:
		return r, os.Remove(filepath.Join(dir, pendingFile))
	}
	r.Progress = p.progress()
	return r, nil
}

// Waiting reports whether this binary is a new one that hasn't served yet.
func (r Resume) Waiting() bool { return r.waiting != nil }

// Served marks this binary's install as done, once it serves requests.
func (r Resume) Served(log *slog.Logger) *InstallProgress {
	if r.waiting == nil {
		return r.Progress
	}
	p := *r.waiting
	p.State, p.Error, p.At = StateInstalled, "", time.Now().UTC()
	if err := writePending(r.dir, p); err != nil {
		log.Warn("record the installed update", "err", err)
	}
	// On Windows the watcher may still run from the old binary; the next start removes it.
	os.Remove(p.Old)
	// The backup before this install stays; those of earlier ones go.
	if entries, err := os.ReadDir(r.dir); err == nil {
		for _, e := range entries {
			if e.IsDir() && e.Name() != p.To && e.Name() != "tuf" {
				os.RemoveAll(filepath.Join(r.dir, e.Name()))
			}
		}
	}
	log.Info("Studio updated", "from", p.From, "to", p.To)
	return p.progress()
}

// Watchdog rolls back a supervised new binary that doesn't serve within healthWait: it puts
// the old binary and the database back and exits with ExitRestart. The database stays open
// in this process, which on Linux, the only supervised host, doesn't stop the restore.
func (r Resume) Watchdog(served <-chan struct{}, log *slog.Logger) {
	if r.waiting == nil || !r.waiting.Supervised {
		return
	}
	p := *r.waiting
	go func() {
		select {
		case <-served:
		case <-time.After(healthWait):
			rollback(p, fmt.Sprintf("Studio %s didn't serve within %s", p.To, healthWait), log)
			os.Exit(ExitRestart)
		}
	}()
}

// rollback puts the old binary and the database backup back and records why.
func rollback(p Pending, reason string, log *slog.Logger) Pending {
	log.Warn("rolling back the Studio update", "from", p.From, "to", p.To, "reason", reason)
	p.State, p.Error, p.At = StateRolledBack, reason, time.Now().UTC()
	if _, err := os.Stat(p.Old); err == nil {
		if err := swapBack(p); err != nil {
			p.Error += "; putting the old binary back failed: " + err.Error()
		}
	}
	if err := restoreDB(p.Backup, p.DB); err != nil {
		p.Error += "; restoring the database failed: " + err.Error()
	}
	if err := writePending(p.Dir, p); err != nil {
		log.Warn("record the rollback", "err", err)
	}
	return p
}

// restoreDB copies the backup over the database and drops its WAL, so the old binary opens
// the database as it was before the new one migrated it. The backup stays.
func restoreDB(backup, db string) error {
	src, err := os.Open(backup)
	if err != nil {
		return err
	}
	defer src.Close()
	tmp := db + ".restore"
	dst, err := os.OpenFile(tmp, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	_, err = io.Copy(dst, src)
	if cerr := dst.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		os.Remove(tmp)
		return err
	}
	for _, f := range []string{db + "-wal", db + "-shm"} {
		if err := os.Remove(f); err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
	}
	return os.Rename(tmp, db)
}

// RestartUnsupervised starts the new binary after Studio shut down, when nothing supervises
// it, and watches it: once it serves, this process can exit. If it exits first or doesn't
// serve within healthWait, the old binary and the database come back and the old binary
// starts instead. args are Studio's own arguments.
func RestartUnsupervised(dir string, args []string, log *slog.Logger) error {
	p, ok, err := readPending(dir)
	if err != nil || !ok {
		return fmt.Errorf("read the install record: %v", err)
	}
	cmd, err := startDetached(p.Exe, args)
	if err != nil {
		p = rollback(p, "starting the new binary failed: "+err.Error(), log)
		return restartOld(p, args, log)
	}
	exited := make(chan error, 1)
	go func() { exited <- cmd.Wait() }()
	deadline := time.After(healthWait)
	tick := time.NewTicker(500 * time.Millisecond)
	defer tick.Stop()
	for {
		select {
		case err := <-exited:
			p = rollback(p, fmt.Sprintf("Studio %s exited before serving: %v", p.To, err), log)
			return restartOld(p, args, log)
		case <-deadline:
			_ = cmd.Process.Kill()
			<-exited
			p = rollback(p, fmt.Sprintf("Studio %s didn't serve within %s", p.To, healthWait), log)
			return restartOld(p, args, log)
		case <-tick.C:
			if q, ok, err := readPending(dir); err == nil && ok && q.State == StateInstalled {
				_ = cmd.Process.Release()
				return nil
			}
		}
	}
}

// StartAgain starts Studio's binary detached, for a rollback at start when nothing
// supervises Studio.
func StartAgain(exe string, args []string) error {
	cmd, err := startDetached(exe, args)
	if err != nil {
		return err
	}
	return cmd.Process.Release()
}

func restartOld(p Pending, args []string, log *slog.Logger) error {
	cmd, err := startDetached(p.Exe, args)
	if err != nil {
		return fmt.Errorf("%s, and starting the old binary failed: %w", p.Error, err)
	}
	_ = cmd.Process.Release()
	log.Warn("the Studio update was rolled back; the old version runs again", "version", p.From, "reason", p.Error)
	return nil
}

// startDetached starts a binary that outlives this process, with its output on ours.
func startDetached(exe string, args []string) (*exec.Cmd, error) {
	cmd := exec.Command(exe, args...)
	cmd.Stdout, cmd.Stderr = os.Stdout, os.Stderr
	detach(cmd)
	if err := cmd.Start(); err != nil {
		if retry := detachRetry(cmd); retry != nil {
			return retry, retry.Start()
		}
		return nil, err
	}
	return cmd, nil
}
