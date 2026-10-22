package updates

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/sigstore/sigstore-go/pkg/bundle"
	"github.com/sigstore/sigstore-go/pkg/root"
)

// release is a fake GitHub release: an archive holding a studio that prints its version, and
// checksums.txt.
type release struct {
	tag     string
	archive []byte
	sums    string
	srv     *httptest.Server
}

func newRelease(t *testing.T, tag string) *release {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("the fake release's studio is a shell script")
	}
	name := "sandbox-studio_" + tag + "_" + runtime.GOOS + "_" + runtime.GOARCH
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	script := []byte("#!/bin/sh\necho \"studio " + tag + "\"\necho \"base image: x\"\n")
	for _, h := range []*tar.Header{
		{Name: name + "/", Typeflag: tar.TypeDir, Mode: 0o755},
		{Name: name + "/LICENSE", Typeflag: tar.TypeReg, Mode: 0o644, Size: 2},
		{Name: name + "/studio", Typeflag: tar.TypeReg, Mode: 0o755, Size: int64(len(script))},
	} {
		if err := tw.WriteHeader(h); err != nil {
			t.Fatal(err)
		}
		switch h.Name {
		case name + "/LICENSE":
			_, _ = tw.Write([]byte("ok"))
		case name + "/studio":
			_, _ = tw.Write(script)
		}
	}
	tw.Close()
	gz.Close()
	r := &release{tag: tag, archive: buf.Bytes()}
	sum := sha256.Sum256(r.archive)
	r.sums = hex.EncodeToString(sum[:]) + "  " + name + ".tar.gz\n"
	r.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		switch {
		case strings.HasSuffix(req.URL.Path, "/checksums.txt"):
			_, _ = io.WriteString(w, r.sums)
		case strings.HasSuffix(req.URL.Path, ".tar.gz"):
			_, _ = w.Write(r.archive)
		default:
			http.NotFound(w, req)
		}
	}))
	t.Cleanup(r.srv.Close)
	return r
}

// assets lists the release's files under their github.com URLs; the client below sends
// those to the fake server.
func (r *release) assets() []Asset {
	base := "https://github.com/" + Repository + "/releases/download/" + r.tag + "/"
	name := "sandbox-studio_" + r.tag + "_" + runtime.GOOS + "_" + runtime.GOARCH + ".tar.gz"
	return []Asset{{Name: name, URL: base + name}, {Name: "checksums.txt", URL: base + "checksums.txt"}}
}

func (r *release) client() *http.Client {
	target := r.srv.URL
	return &http.Client{Transport: roundTrip(func(req *http.Request) (*http.Response, error) {
		u := target + req.URL.Path
		out, err := http.NewRequestWithContext(req.Context(), req.Method, u, nil)
		if err != nil {
			return nil, err
		}
		return http.DefaultTransport.RoundTrip(out)
	})}
}

type roundTrip func(*http.Request) (*http.Response, error)

func (f roundTrip) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

type installFixture struct {
	in       *Installer
	exe, db  string
	restarts chan struct{}
	verified []string
}

func newInstall(t *testing.T, r *release) *installFixture {
	t.Helper()
	dir := t.TempDir()
	f := &installFixture{exe: filepath.Join(dir, "bin", "studio"), db: filepath.Join(dir, "data", "studio.db"), restarts: make(chan struct{}, 1)}
	for _, p := range []string{f.exe, f.db} {
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(f.exe, []byte("old binary"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(f.db, []byte("new schema"), 0o600); err != nil {
		t.Fatal(err)
	}
	busy := 2
	f.in = &Installer{
		Client: r.client(), Dir: filepath.Join(dir, "data", "updates"), Exe: f.exe, Current: "v1.0.0", DB: f.db,
		Verify: func(_ context.Context, digest []byte, tag string) error {
			f.verified = append(f.verified, hex.EncodeToString(digest)+" "+tag)
			return nil
		},
		Idle: func(context.Context) (bool, string, error) {
			busy--
			return busy < 0, "a chat is running", nil
		},
		Backup:   func(_ context.Context, path string) error { return os.WriteFile(path, []byte("old schema"), 0o600) },
		Restart:  func() { f.restarts <- struct{}{} },
		IdlePoll: 5 * time.Millisecond,
	}
	return f
}

func (f *installFixture) wait(t *testing.T, phase string) *InstallProgress {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if p := f.in.Progress(); p != nil && p.Phase == phase {
			return p
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("never reached %s: %+v", phase, f.in.Progress())
	return nil
}

func read(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// An install downloads the archive, checks it against checksums.txt and its attestation,
// waits for Studio to be idle, backs up the database, swaps the binary and restarts.
func TestInstallSwapsInAVerifiedRelease(t *testing.T) {
	r := newRelease(t, "v1.1.0")
	f := newInstall(t, r)
	f.in.Start(Release{Tag: "v1.1.0", Assets: r.assets()})
	f.wait(t, PhaseWaiting)
	select {
	case <-f.restarts:
	case <-time.After(10 * time.Second):
		t.Fatalf("no restart: %+v", f.in.Progress())
	}
	sum := sha256.Sum256(r.archive)
	if len(f.verified) != 1 || f.verified[0] != hex.EncodeToString(sum[:])+" v1.1.0" {
		t.Errorf("verified %v", f.verified)
	}
	if got := read(t, f.exe); !strings.Contains(got, "studio v1.1.0") {
		t.Errorf("the binary is %q", got)
	}
	if got := read(t, f.exe+".old"); got != "old binary" {
		t.Errorf("the old binary is %q", got)
	}
	p, ok, err := readPending(f.in.Dir)
	if err != nil || !ok || p.State != StateSwapped || p.From != "v1.0.0" || p.To != "v1.1.0" || read(t, p.Backup) != "old schema" {
		t.Fatalf("record %+v %v %v", p, ok, err)
	}
	f.in.Start(Release{Tag: "v1.1.0", Assets: r.assets()})
	if len(f.restarts) != 0 {
		t.Error("a second start installed again")
	}
}

// Nothing changes when the download doesn't match checksums.txt or its attestation doesn't
// verify, or the release isn't newer.
func TestInstallRefusesWhatDoesntVerify(t *testing.T) {
	for name, tc := range map[string]struct {
		tag   string
		spoil func(*release, *installFixture)
		want  string
	}{
		"checksum": {"v1.1.0", func(r *release, _ *installFixture) { r.sums = strings.Repeat("0", 64) + r.sums[64:] }, "doesn't match checksums.txt"},
		"attestation": {"v1.1.0", func(_ *release, f *installFixture) {
			f.in.Verify = func(context.Context, []byte, string) error { return errors.New("no attestation") }
		}, "no attestation"},
		"downgrade":  {"v0.9.0", func(*release, *installFixture) {}, "not newer"},
		"prerelease": {"v1.1.0-rc.1", func(*release, *installFixture) {}, "not a release version"},
	} {
		t.Run(name, func(t *testing.T) {
			r := newRelease(t, tc.tag)
			f := newInstall(t, r)
			tc.spoil(r, f)
			f.in.Start(Release{Tag: tc.tag, Assets: r.assets()})
			p := f.wait(t, PhaseFailed)
			if !strings.Contains(p.Error, tc.want) {
				t.Errorf("error %q", p.Error)
			}
			if read(t, f.exe) != "old binary" || len(f.restarts) != 0 {
				t.Error("the binary changed")
			}
			entries, _ := os.ReadDir(filepath.Dir(f.exe))
			if len(entries) != 1 {
				t.Errorf("left %v next to the binary", entries)
			}
		})
	}
}

// The new binary counts its starts and marks the install once it serves; under a service
// manager, too many starts roll it back. The old binary, started before the new one served,
// restores the database.
func TestResumeInstall(t *testing.T) {
	log := slog.New(slog.DiscardHandler)
	setup := func(t *testing.T, supervised bool) Pending {
		dir := t.TempDir()
		p := Pending{Dir: filepath.Join(dir, "updates"), From: "v1.0.0", To: "v1.1.0", Exe: filepath.Join(dir, "studio"), Old: filepath.Join(dir, "studio.old"),
			Backup: filepath.Join(dir, "updates", "v1.1.0", "studio-v1.0.0.db"), DB: filepath.Join(dir, "studio.db"), Supervised: supervised, State: StateSwapped}
		if err := os.MkdirAll(filepath.Dir(p.Backup), 0o700); err != nil {
			t.Fatal(err)
		}
		for path, body := range map[string]string{p.Exe: "new binary", p.Old: "old binary", p.Backup: "old schema", p.DB: "new schema", p.DB + "-wal": "wal"} {
			if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
				t.Fatal(err)
			}
		}
		if err := writePending(p.Dir, p); err != nil {
			t.Fatal(err)
		}
		return p
	}

	t.Run("served", func(t *testing.T) {
		p := setup(t, false)
		r, err := ResumeInstall(p.Dir, "v1.1.0", log)
		if err != nil || r.Exit || !r.Waiting() {
			t.Fatalf("%+v %v", r, err)
		}
		if pr := r.Served(log); pr.Phase != PhaseInstalled || pr.From != "v1.0.0" {
			t.Errorf("progress %+v", pr)
		}
		if _, err := os.Stat(p.Old); !errors.Is(err, os.ErrNotExist) {
			t.Error("the old binary stayed")
		}
		r, _ = ResumeInstall(p.Dir, "v1.1.0", log)
		if r.Waiting() || r.Progress.Phase != PhaseInstalled {
			t.Errorf("after serving: %+v", r)
		}
	})

	t.Run("too many starts", func(t *testing.T) {
		p := setup(t, true)
		for i := 1; i <= maxAttempts; i++ {
			if r, err := ResumeInstall(p.Dir, "v1.1.0", log); err != nil || r.Exit {
				t.Fatalf("start %d: %+v %v", i, r, err)
			}
		}
		r, err := ResumeInstall(p.Dir, "v1.1.0", log)
		if err != nil || !r.Exit || r.Progress.Phase != PhaseFailed || !strings.Contains(r.Progress.Error, "without serving") {
			t.Fatalf("%+v %v", r, err)
		}
		if read(t, p.Exe) != "old binary" || read(t, p.DB) != "old schema" || read(t, p.Exe+".failed") != "new binary" {
			t.Error("the rollback didn't restore the binary and the database")
		}
		if _, err := os.Stat(p.DB + "-wal"); !errors.Is(err, os.ErrNotExist) {
			t.Error("the new binary's WAL stayed")
		}
	})

	t.Run("old binary", func(t *testing.T) {
		p := setup(t, false)
		r, err := ResumeInstall(p.Dir, "v1.0.0", log)
		if err != nil || r.Exit || r.Waiting() || r.Progress.Phase != PhaseFailed {
			t.Fatalf("%+v %v", r, err)
		}
		if read(t, p.DB) != "old schema" {
			t.Error("the database wasn't restored")
		}
	})

	t.Run("other version", func(t *testing.T) {
		p := setup(t, false)
		if r, err := ResumeInstall(p.Dir, "v2.0.0", log); err != nil || r.Progress != nil {
			t.Fatalf("%+v %v", r, err)
		}
		if _, ok, _ := readPending(p.Dir); ok {
			t.Error("the stale record stayed")
		}
	})
}

// Without a service manager the old process starts the new binary and watches it; one that
// exits before serving is rolled back and the old binary starts again.
func TestRestartUnsupervisedRollsBack(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the binaries are shell scripts")
	}
	dir := t.TempDir()
	marker := filepath.Join(dir, "old-started")
	p := Pending{Dir: filepath.Join(dir, "updates"), From: "v1.0.0", To: "v1.1.0", Exe: filepath.Join(dir, "studio"), Old: filepath.Join(dir, "studio.old"),
		Backup: filepath.Join(dir, "updates", "v1.1.0", "studio-v1.0.0.db"), DB: filepath.Join(dir, "studio.db"), State: StateSwapped}
	if err := os.MkdirAll(filepath.Dir(p.Backup), 0o700); err != nil {
		t.Fatal(err)
	}
	for path, body := range map[string]string{
		p.Exe: "#!/bin/sh\nexit 3\n", p.Old: "#!/bin/sh\necho \"$@\" > " + marker + "\n", p.Backup: "old schema", p.DB: "new schema",
	} {
		if err := os.WriteFile(path, []byte(body), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := writePending(p.Dir, p); err != nil {
		t.Fatal(err)
	}
	if err := RestartUnsupervised(p.Dir, []string{"-no-browser"}, slog.New(slog.DiscardHandler)); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(10 * time.Second)
	for {
		if b, err := os.ReadFile(marker); err == nil && strings.TrimSpace(string(b)) == "-no-browser" {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("the old binary didn't start")
		}
		time.Sleep(10 * time.Millisecond)
	}
	q, _, _ := readPending(p.Dir)
	if q.State != StateRolledBack || !strings.Contains(q.Error, "exited before serving") || read(t, p.DB) != "old schema" {
		t.Errorf("record %+v", q)
	}
}

// The release workflow's attestation of v0.5.1's Linux archive verifies against Sigstore's
// trusted root for that tag and digest only.
func TestVerifyBundlesOfARealRelease(t *testing.T) {
	rootJSON, err := os.ReadFile("testdata/trusted_root.json")
	if err != nil {
		t.Fatal(err)
	}
	tr, err := root.NewTrustedRootFromJSON(rootJSON)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile("testdata/attestations-v0.5.1-linux-amd64.json")
	if err != nil {
		t.Fatal(err)
	}
	var list struct {
		Attestations []struct {
			Bundle json.RawMessage `json:"bundle"`
		} `json:"attestations"`
	}
	if err := json.Unmarshal(raw, &list); err != nil {
		t.Fatal(err)
	}
	var bundles []*bundle.Bundle
	for _, a := range list.Attestations {
		var b bundle.Bundle
		if err := b.UnmarshalJSON(a.Bundle); err != nil {
			t.Fatal(err)
		}
		bundles = append(bundles, &b)
	}
	digest, _ := hex.DecodeString("b2ba4f74498b20c1628657aade45cbcf45c5dbc0f2e7b51ba9caf46f166f55f6")
	if err := verifyBundles(tr, bundles, digest, Repository, "v0.5.1"); err != nil {
		t.Fatalf("the real attestation: %v", err)
	}
	other := bytes.Clone(digest)
	other[0] ^= 1
	for name, err := range map[string]error{
		"another tag":        verifyBundles(tr, bundles, digest, Repository, "v0.5.2"),
		"another digest":     verifyBundles(tr, bundles, other, Repository, "v0.5.1"),
		"another repository": verifyBundles(tr, bundles, digest, "someone/sandbox-studio", "v0.5.1"),
	} {
		if err == nil {
			t.Errorf("%s verified", name)
		}
	}
}
