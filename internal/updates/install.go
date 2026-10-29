package updates

import (
	"archive/tar"
	"archive/zip"
	"bufio"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/sigstore/sigstore-go/pkg/root"
)

// The phases of an automatic install, as the UI shows them.
const (
	PhaseDownloading = "downloading"
	PhaseWaiting     = "waiting"    // for Studio to be idle
	PhaseRestarting  = "restarting" // the binary is swapped; Studio restarts into it
	PhaseInstalled   = "installed"  // the new binary started and serves
	PhaseFailed      = "failed"     // nothing changed, or the old binary came back
)

// InstallProgress is an automatic install, or how the last one ended.
type InstallProgress struct {
	Version string    `json:"version" doc:"The release being installed"`
	From    string    `json:"from,omitempty" doc:"The version it replaces, once installed or rolled back"`
	Phase   string    `json:"phase" enum:"downloading,waiting,restarting,installed,failed"`
	Detail  string    `json:"detail,omitempty" doc:"What the install waits for"`
	Error   string    `json:"error,omitempty" doc:"Why it failed"`
	At      time.Time `json:"at"`
}

// Limits of the downloads.
const (
	maxArchive   = 512 << 20
	maxBinary    = 512 << 20
	maxChecksums = 64 << 10
	// healthWait is how long a new binary has to serve requests before the old one comes back.
	healthWait = 2 * time.Minute
	// maxAttempts is how often a supervised new binary may start without serving.
	maxAttempts = 3
	// retryFailed is how long a release whose install failed is left alone.
	retryFailed = 24 * time.Hour
)

// Installer downloads, verifies and swaps in a release, then has Studio restart into it.
type Installer struct {
	Client  *http.Client
	Repo    string // Repository when empty
	APIURL  string // https://api.github.com when empty
	Dir     string // <data>/updates: downloads, the database backup and the install record
	Exe     string // the running binary, with symlinks resolved
	Current string
	// GOOS and GOARCH pick the release's archive; the runtime's when empty.
	GOOS, GOARCH string
	// Context lives as long as the server; installs run in it.
	Context context.Context
	// Verify checks a download's attestation; nil checks it with Sigstore (verifyAttestation).
	Verify func(ctx context.Context, digest []byte, tag string) error
	// TrustedRoot is Sigstore's trusted root; nil fetches it with TUF.
	TrustedRoot func(ctx context.Context) (root.TrustedMaterial, error)
	// Idle reports whether a restart would cut nothing off, and if not, what is busy.
	Idle func(ctx context.Context) (bool, string, error)
	// Backup writes a consistent copy of Studio's database to path (VACUUM INTO).
	Backup func(ctx context.Context, path string) error
	// DB is the database's path, which a rollback restores from the backup.
	DB string
	// Restart shuts Studio down so that it restarts into the swapped binary.
	Restart func()
	// IdlePoll is how often a waiting install checks for idle; two minutes when zero.
	IdlePoll time.Duration
	Log      *slog.Logger

	mu       sync.Mutex
	progress *InstallProgress
	cancel   context.CancelFunc
}

// Progress returns the install under way, or the last one's outcome.
func (in *Installer) Progress() *InstallProgress {
	in.mu.Lock()
	defer in.mu.Unlock()
	if in.progress == nil {
		return nil
	}
	p := *in.progress
	return &p
}

// SetProgress records an outcome from an earlier run, read at start (Resume).
func (in *Installer) SetProgress(p *InstallProgress) {
	in.mu.Lock()
	in.progress = p
	in.mu.Unlock()
}

// Start installs the release in the background unless an install is under way, the release
// is installed, or its install failed less than a day ago.
func (in *Installer) Start(rel Release) {
	in.mu.Lock()
	defer in.mu.Unlock()
	if p := in.progress; p != nil && p.Version == rel.Tag {
		switch p.Phase {
		case PhaseDownloading, PhaseWaiting, PhaseRestarting, PhaseInstalled:
			return
		case PhaseFailed:
			if time.Since(p.At) < retryFailed {
				return
			}
		}
	}
	if p := in.progress; p != nil && (p.Phase == PhaseDownloading || p.Phase == PhaseWaiting || p.Phase == PhaseRestarting) {
		return
	}
	base := in.Context
	if base == nil {
		base = context.Background()
	}
	ctx, cancel := context.WithCancel(base)
	in.cancel = cancel
	in.progress = &InstallProgress{Version: rel.Tag, Phase: PhaseDownloading, At: time.Now().UTC()}
	go func() {
		defer cancel()
		if err := in.install(ctx, rel); err != nil {
			if ctx.Err() != nil && base.Err() == nil {
				err = errors.New("cancelled")
			}
			in.log().Warn("installing a Studio update", "version", rel.Tag, "err", err)
			in.set(func(p *InstallProgress) { p.Phase, p.Detail, p.Error = PhaseFailed, "", err.Error() })
		}
	}()
}

// Cancel stops an install that hasn't swapped the binary yet.
func (in *Installer) Cancel() {
	in.mu.Lock()
	defer in.mu.Unlock()
	if in.cancel != nil && in.progress != nil && in.progress.Phase != PhaseRestarting {
		in.cancel()
	}
}

func (in *Installer) set(fn func(*InstallProgress)) {
	in.mu.Lock()
	defer in.mu.Unlock()
	if in.progress != nil {
		fn(in.progress)
		in.progress.At = time.Now().UTC()
	}
}

func (in *Installer) log() *slog.Logger {
	if in.Log != nil {
		return in.Log
	}
	return slog.New(slog.DiscardHandler)
}

func (in *Installer) client() *http.Client {
	if in.Client != nil {
		return in.Client
	}
	return http.DefaultClient
}

// install runs one install up to the restart.
func (in *Installer) install(ctx context.Context, rel Release) error {
	if v, ok := parse(rel.Tag); !ok || v.pre != "" {
		return fmt.Errorf("%s is not a release version", rel.Tag)
	}
	if !Newer(rel.Tag, in.Current) {
		return fmt.Errorf("%s is not newer than %s", rel.Tag, in.Current)
	}
	goos, goarch := in.GOOS, in.GOARCH
	if goos == "" {
		goos, goarch = runtime.GOOS, runtime.GOARCH
	}
	name := "sandbox-studio_" + rel.Tag + "_" + goos + "_" + goarch
	archive := name + ".tar.gz"
	exeName := "studio"
	if goos == "windows" {
		archive, exeName = name+".zip", "studio.exe"
	}
	prefix := "https://github.com/" + in.repo() + "/releases/download/" + rel.Tag + "/"
	asset := func(n string) (Asset, error) {
		for _, a := range rel.Assets {
			if a.Name == n {
				if a.URL != prefix+n {
					return a, fmt.Errorf("the release's %s is not on its release page", n)
				}
				return a, nil
			}
		}
		return Asset{}, fmt.Errorf("the release has no %s", n)
	}
	archiveAsset, err := asset(archive)
	if err != nil {
		return err
	}
	sumsAsset, err := asset("checksums.txt")
	if err != nil {
		return err
	}
	// The new binary goes next to the running one, so the swap is a rename.
	exeDir := filepath.Dir(in.Exe)
	probe, err := os.CreateTemp(exeDir, ".studio-update-*")
	if err != nil {
		return fmt.Errorf("Studio can't write to %s, where it is installed: %w", exeDir, err)
	}
	probe.Close()
	os.Remove(probe.Name())

	dir := filepath.Join(in.Dir, rel.Tag)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	sums, err := in.get(ctx, sumsAsset.URL, maxChecksums, "")
	if err != nil {
		return fmt.Errorf("download checksums.txt: %w", err)
	}
	want, err := checksum(sums, archive)
	if err != nil {
		return err
	}
	archivePath := filepath.Join(dir, archive)
	digest, err := in.download(ctx, archiveAsset.URL, archivePath)
	if err != nil {
		return fmt.Errorf("download %s: %w", archive, err)
	}
	if !bytes.Equal(digest, want) {
		return fmt.Errorf("%s doesn't match checksums.txt", archive)
	}
	verifyFn := in.Verify
	if verifyFn == nil {
		verifyFn = in.verifyAttestation
	}
	if err := verifyFn(ctx, digest, rel.Tag); err != nil {
		return fmt.Errorf("verify %s: %w", archive, err)
	}
	newExe := filepath.Join(exeDir, ".studio-"+rel.Tag+".new")
	if goos == "windows" {
		newExe += ".exe"
	}
	if err := extract(archivePath, name+"/"+exeName, newExe); err != nil {
		return fmt.Errorf("unpack %s: %w", archive, err)
	}
	// The programs installed next to studio come along when the archive has them.
	type companion struct{ path, next string }
	var companions []companion
	for _, c := range companionNames(goos) {
		if p := filepath.Join(exeDir, c); fileExists(p) {
			next := filepath.Join(exeDir, "."+c+"-"+rel.Tag+".new")
			if err := extract(archivePath, name+"/"+c, next); err == nil {
				companions = append(companions, companion{p, next})
				defer os.Remove(next)
			}
		}
	}
	// On macOS the app is a bundle, which comes along whole, as its signature covers its files.
	// An install with an earlier release's bare app gets the bundle in its place.
	var bundle *companion
	if goos == "darwin" && (dirExists(filepath.Join(exeDir, appBundle)) || fileExists(filepath.Join(exeDir, "studio-app"))) {
		next := filepath.Join(exeDir, "."+appBundle+"-"+rel.Tag+".new")
		if err := extractDir(archivePath, name+"/"+appBundle, next); err == nil {
			bundle = &companion{filepath.Join(exeDir, appBundle), next}
		}
		defer os.RemoveAll(next)
	}
	os.Remove(archivePath)
	keep := false
	defer func() {
		if !keep {
			os.Remove(newExe)
		}
	}()
	if err := checkVersion(ctx, newExe, rel.Tag); err != nil {
		return err
	}

	if err := in.waitIdle(ctx); err != nil {
		return err
	}
	in.set(func(p *InstallProgress) { p.Phase, p.Detail = PhaseRestarting, "" })
	backup := filepath.Join(dir, "studio-"+in.Current+".db")
	os.Remove(backup)
	if err := in.Backup(ctx, backup); err != nil {
		return fmt.Errorf("back up the database: %w", err)
	}
	pending := Pending{Dir: in.Dir, From: in.Current, To: rel.Tag, Exe: in.Exe, Old: oldExe(in.Exe), Backup: backup, DB: in.DB,
		Supervised: Supervised(), State: StateSwapped, At: time.Now().UTC()}
	if err := swap(in.Exe, newExe, pending.Old); err != nil {
		return err
	}
	keep = true
	if err := writePending(in.Dir, pending); err != nil {
		// Without the record nothing could roll back, so the old binary goes back now.
		if rerr := swapBack(pending); rerr != nil {
			return fmt.Errorf("record the install: %w; and put the old binary back: %v", err, rerr)
		}
		return fmt.Errorf("record the install: %w", err)
	}
	for _, c := range companions {
		// A running tray or app keeps its old binary until it starts again; a rollback leaves
		// the new one, which only starts, shows and stops studio.
		if err := swap(c.path, c.next, oldExe(c.path)); err != nil {
			in.log().Warn("updating "+filepath.Base(c.path), "err", err)
		}
	}
	if bundle != nil {
		if err := swapDir(bundle.path, bundle.next); err != nil {
			in.log().Warn("updating "+appBundle, "err", err)
		} else {
			os.Remove(filepath.Join(exeDir, "studio-app")) // studio app starts the bundle now
		}
	}
	in.log().Info("restarting into the Studio update", "version", rel.Tag)
	in.Restart()
	return nil
}

// waitIdle waits until nothing would be cut off by a restart.
func (in *Installer) waitIdle(ctx context.Context) error {
	poll := in.IdlePoll
	if poll <= 0 {
		poll = 2 * time.Minute
	}
	for {
		idle, busy, err := in.Idle(ctx)
		if err != nil {
			return fmt.Errorf("check whether Studio is busy: %w", err)
		}
		if idle {
			return nil
		}
		in.set(func(p *InstallProgress) { p.Phase, p.Detail = PhaseWaiting, busy })
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(poll):
		}
	}
}

// download writes a URL to path and returns its sha256.
func (in *Installer) download(ctx context.Context, u, path string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "sandbox-studio/"+in.Current)
	resp, err := in.client().Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("%s answered %s", req.URL.Host, resp.Status)
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o600)
	if err != nil {
		return nil, err
	}
	h := sha256.New()
	n, err := io.Copy(io.MultiWriter(f, h), io.LimitReader(resp.Body, maxArchive+1))
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err == nil && n > maxArchive {
		err = errors.New("the download is too large")
	}
	if err != nil {
		os.Remove(path)
		return nil, err
	}
	return h.Sum(nil), nil
}

// checksum finds a file's sha256 in a checksums.txt.
func checksum(sums []byte, name string) ([]byte, error) {
	sc := bufio.NewScanner(bytes.NewReader(sums))
	for sc.Scan() {
		fields := strings.Fields(sc.Text())
		if len(fields) == 2 && strings.TrimPrefix(fields[1], "*") == name {
			d, err := hex.DecodeString(fields[0])
			if err != nil || len(d) != sha256.Size {
				return nil, fmt.Errorf("checksums.txt has a malformed line for %s", name)
			}
			return d, nil
		}
	}
	return nil, fmt.Errorf("checksums.txt has no line for %s", name)
}

// extract writes one regular file of a .tar.gz or .zip to dst, executable.
func extract(archive, member, dst string) error {
	out, err := os.OpenFile(dst, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o755)
	if err != nil {
		return err
	}
	err = extractTo(archive, member, out)
	if cerr := out.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		os.Remove(dst)
	}
	return err
}

// extractDir unpacks the directory member of a .tar.gz archive, its directories and regular
// files, into dst, which it replaces.
func extractDir(archive, member, dst string) error {
	if strings.HasSuffix(archive, ".zip") {
		return errors.New("only a .tar.gz archive has a bundle")
	}
	f, err := os.Open(archive)
	if err != nil {
		return err
	}
	defer f.Close()
	gz, err := gzip.NewReader(f)
	if err != nil {
		return err
	}
	if err := os.RemoveAll(dst); err != nil {
		return err
	}
	found := false
	tr := tar.NewReader(gz)
	for {
		h, err := tr.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return err
		}
		rel, ok := strings.CutPrefix(strings.TrimSuffix(h.Name, "/"), member)
		if !ok || (rel != "" && rel[0] != '/') {
			continue
		}
		rel = strings.TrimPrefix(rel, "/")
		if !filepath.IsLocal(filepath.FromSlash(rel)) && rel != "" {
			return fmt.Errorf("%s leaves the bundle", h.Name)
		}
		found = true
		path := filepath.Join(dst, filepath.FromSlash(rel))
		switch h.Typeflag {
		case tar.TypeDir:
			if err := os.MkdirAll(path, 0o755); err != nil {
				return err
			}
		case tar.TypeReg:
			if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
				return err
			}
			out, err := os.OpenFile(path, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, os.FileMode(h.Mode)&0o755)
			if err != nil {
				return err
			}
			n, err := io.Copy(out, io.LimitReader(tr, maxBinary+1))
			if err == nil && n > maxBinary {
				err = errors.New("a file of the bundle is too large")
			}
			if cerr := out.Close(); err == nil {
				err = cerr
			}
			if err != nil {
				return err
			}
		}
	}
	if !found {
		return fmt.Errorf("it has no %s", member)
	}
	return nil
}

func extractTo(archive, member string, out io.Writer) error {
	copyMember := func(r io.Reader) error {
		n, err := io.Copy(out, io.LimitReader(r, maxBinary+1))
		if err == nil && n > maxBinary {
			err = errors.New("the binary is too large")
		}
		return err
	}
	if strings.HasSuffix(archive, ".zip") {
		z, err := zip.OpenReader(archive)
		if err != nil {
			return err
		}
		defer z.Close()
		for _, f := range z.File {
			if f.Name == member && f.Mode().IsRegular() {
				r, err := f.Open()
				if err != nil {
					return err
				}
				defer r.Close()
				return copyMember(r)
			}
		}
		return fmt.Errorf("it has no %s", member)
	}
	f, err := os.Open(archive)
	if err != nil {
		return err
	}
	defer f.Close()
	gz, err := gzip.NewReader(f)
	if err != nil {
		return err
	}
	tr := tar.NewReader(gz)
	for {
		h, err := tr.Next()
		if errors.Is(err, io.EOF) {
			return fmt.Errorf("it has no %s", member)
		}
		if err != nil {
			return err
		}
		if h.Name == member && h.Typeflag == tar.TypeReg {
			return copyMember(tr)
		}
	}
}

// checkVersion runs `studio version` of the new binary, which must name the release.
func checkVersion(ctx context.Context, exe, tag string) error {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, exe, "version").Output()
	if err != nil {
		return fmt.Errorf("the new binary doesn't run: %w%s", err, nixLdHint(err))
	}
	if first, _, _ := strings.Cut(string(out), "\n"); strings.TrimSpace(first) != "studio "+tag {
		return fmt.Errorf("the new binary says %q, not studio %s", strings.TrimSpace(first), tag)
	}
	return nil
}

// oldExe is where the replaced binary stays until the new one serves.
// companionNames are the programs a release installs next to studio: the Studio app
// (cmd/studio-app) and, on Windows, the tray launcher (cmd/studiow).
func companionNames(goos string) []string {
	if goos == "windows" {
		return []string{"studiow.exe", "studio-app.exe"}
	}
	return []string{"studio-app"}
}

// appBundle is the Studio app on macOS, next to studio.
const appBundle = "Sandbox Studio.app"

func dirExists(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.IsDir()
}

// swapDir puts the directory next in path's place, and the old one away. A running app
// keeps its open binary.
func swapDir(path, next string) error {
	old := path + ".old"
	if err := os.RemoveAll(old); err != nil {
		return err
	}
	if err := os.Rename(path, old); err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("move the old one aside: %w", err)
	}
	if err := os.Rename(next, path); err != nil {
		if rerr := os.Rename(old, path); rerr != nil && !errors.Is(rerr, os.ErrNotExist) {
			return fmt.Errorf("move the new one in: %w; and put the old one back: %v", err, rerr)
		}
		return fmt.Errorf("move the new one in: %w", err)
	}
	return os.RemoveAll(old)
}

func fileExists(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.Mode().IsRegular()
}

func oldExe(exe string) string {
	if strings.EqualFold(filepath.Ext(exe), ".exe") {
		return strings.TrimSuffix(exe, filepath.Ext(exe)) + ".old.exe"
	}
	return exe + ".old"
}

// swap moves the running binary to old and the new one into its place. On Windows a running
// executable can be renamed but not replaced, which is why it moves first everywhere.
func swap(exe, newExe, old string) error {
	if err := os.Remove(old); err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("remove the binary an earlier update left: %w", err)
	}
	if err := os.Rename(exe, old); err != nil {
		return fmt.Errorf("move the running binary aside: %w", err)
	}
	if err := os.Rename(newExe, exe); err != nil {
		if rerr := os.Rename(old, exe); rerr != nil {
			return fmt.Errorf("move the new binary in: %w; and put the old one back: %v", err, rerr)
		}
		return fmt.Errorf("move the new binary in: %w", err)
	}
	return nil
}

// swapBack puts the old binary back. The failed one stays next to it as <exe>.failed, or
// is removed where it can't be renamed.
func swapBack(p Pending) error {
	if _, err := os.Stat(p.Old); err != nil {
		return fmt.Errorf("the old binary is gone: %w", err)
	}
	failed := p.Exe + ".failed"
	os.Remove(failed)
	if err := os.Rename(p.Exe, failed); err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("move the new binary aside: %w", err)
	}
	return os.Rename(p.Old, p.Exe)
}
