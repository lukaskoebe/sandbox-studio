package doctor

import (
	"context"
	"errors"
	"io/fs"
	"strings"
	"testing"
)

// healthy returns probes for a Linux host on which every check passes.
func healthy() Probes {
	return Probes{
		GOOS: "linux", GOARCH: "amd64",
		MSBPath: func() (string, error) { return "/home/u/.microsandbox/bin/msb", nil },
		Run: func(_ context.Context, _ string, args ...string) (string, error) {
			if args[0] == "--version" {
				return "msb 0.7.7\n", nil
			}
			return "all good\n", nil
		},
		Stat:          func(string) (fs.FileInfo, error) { return nil, nil },
		Access:        func(string) error { return nil },
		KVMGroup:      func() (bool, bool, error) { return true, true, nil },
		ImageCached:   func(context.Context, string) (bool, error) { return true, nil },
		ImagePullable: func(context.Context, string) error { return nil },
		Writable:      func(string) error { return nil },
		DiskFree:      func(string) (uint64, error) { return 100 << 30, nil },
		PortFree:      func(string) error { return nil },
	}
}

var opts = Options{DataDir: "/data", Image: "ghcr.io/o/base@sha256:abc", MSBVersion: "0.7.7", Ports: []string{"127.0.0.1:7878"}}

func find(t *testing.T, r Report, id string) Check {
	t.Helper()
	for _, c := range r.Checks {
		if c.ID == id {
			return c
		}
	}
	t.Fatalf("no check %q in %+v", id, r.Checks)
	return Check{}
}

func TestHealthyHost(t *testing.T) {
	r := Run(context.Background(), healthy(), opts)
	if r.Status != OK || len(r.Problems()) != 0 {
		t.Fatalf("status %s, problems %+v", r.Status, r.Problems())
	}
	for _, id := range []string{"msb", "msb-doctor", "kvm", "image", "data-dir", "disk", "port:127.0.0.1:7878"} {
		find(t, r, id)
	}
}

func TestKVM(t *testing.T) {
	cases := []struct {
		name   string
		mod    func(*Probes)
		status Status
		fix    string
	}{
		{"missing", func(p *Probes) { p.Stat = func(string) (fs.FileInfo, error) { return nil, fs.ErrNotExist } }, Fail, "nested virtualization"},
		{"no access", func(p *Probes) {
			p.Access = func(string) error { return fs.ErrPermission }
			p.KVMGroup = func() (bool, bool, error) { return false, false, nil }
		}, Fail, "usermod -aG kvm"},
		{"seat ACL is explained too", func(p *Probes) {
			p.Access = func(string) error { return fs.ErrPermission }
			p.KVMGroup = func() (bool, bool, error) { return false, false, errors.New("no kvm group") }
		}, Fail, "systemd-logind"},
		{"group not active yet", func(p *Probes) {
			p.Access = func(string) error { return fs.ErrPermission }
			p.KVMGroup = func() (bool, bool, error) { return true, false, nil }
		}, Fail, "log out and back in"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			p := healthy()
			tc.mod(&p)
			c := find(t, Run(context.Background(), p, opts), "kvm")
			if c.Status != tc.status || !strings.Contains(strings.ToLower(c.Fix), strings.ToLower(tc.fix)) {
				t.Fatalf("got %+v", c)
			}
		})
	}
}

func TestMacAndWindows(t *testing.T) {
	p := healthy()
	p.GOOS, p.GOARCH = "darwin", "amd64"
	if c := find(t, Run(context.Background(), p, opts), "hvf"); c.Status != Fail || !strings.Contains(c.Message, "Intel") {
		t.Fatalf("intel mac: %+v", c)
	}
	p.GOARCH = "arm64"
	p.HVF = func() (bool, error) { return true, nil }
	if c := find(t, Run(context.Background(), p, opts), "hvf"); c.Status != OK {
		t.Fatalf("apple silicon: %+v", c)
	}
	p.HVF = func() (bool, error) { return false, nil }
	if c := find(t, Run(context.Background(), p, opts), "hvf"); c.Status != Fail {
		t.Fatalf("no hvf: %+v", c)
	}

	p = healthy()
	p.GOOS = "windows"
	p.WHP = func() (bool, error) { return false, nil }
	if c := find(t, Run(context.Background(), p, opts), "whp"); c.Status != Fail || !strings.Contains(c.Fix, "HypervisorPlatform") {
		t.Fatalf("whp off: %+v", c)
	}
	p.WHP = func() (bool, error) { return true, nil }
	if c := find(t, Run(context.Background(), p, opts), "whp"); c.Status != OK {
		t.Fatalf("whp on: %+v", c)
	}
	p.JobBlocksBreakaway = func() (bool, error) { return true, nil }
	if c := find(t, Run(context.Background(), p, opts), "job"); c.Status != Fail || !strings.Contains(c.Message, "Access is denied") {
		t.Fatalf("job blocks breakaway: %+v", c)
	}
	p.JobBlocksBreakaway = func() (bool, error) { return false, nil }
	if c := find(t, Run(context.Background(), p, opts), "job"); c.Status != OK {
		t.Fatalf("job allows breakaway: %+v", c)
	}
}

func TestMSB(t *testing.T) {
	p := healthy()
	p.MSBPath = func() (string, error) { return "", errors.New("not installed") }
	r := Run(context.Background(), p, opts)
	if c := find(t, r, "msb"); c.Status != Warn {
		t.Fatalf("missing msb: %+v", c)
	}
	if r.Status != Warn {
		t.Fatalf("report status %s", r.Status)
	}

	p = healthy()
	p.Run = func(_ context.Context, _ string, args ...string) (string, error) {
		if args[0] == "--version" {
			return "msb 0.7.6", nil
		}
		return "line1\nKVM: permission denied\n", errors.New("exit status 1")
	}
	r = Run(context.Background(), p, opts)
	if c := find(t, r, "msb"); c.Status != Warn || !strings.Contains(c.Message, "0.7.6") {
		t.Fatalf("version mismatch: %+v", c)
	}
	if c := find(t, r, "msb-doctor"); c.Status != Fail || !strings.Contains(c.Message, "permission denied") {
		t.Fatalf("msb doctor: %+v", c)
	}
	if r.Status != Fail {
		t.Fatalf("report status %s", r.Status)
	}
}

func TestImage(t *testing.T) {
	p := healthy()
	p.ImageCached = func(context.Context, string) (bool, error) { return false, nil }
	if c := find(t, Run(context.Background(), p, opts), "image"); c.Status != OK || !strings.Contains(c.Message, "pulled") {
		t.Fatalf("pullable: %+v", c)
	}
	// A saved login for the registry is what the pull uses, and an expired one breaks it.
	p.RegistryLogins = func() ([]string, error) { return []string{"docker.io", "ghcr.io"}, nil }
	if c := find(t, Run(context.Background(), p, opts), "image"); c.Status != Warn || !strings.Contains(c.Fix, "docker logout ghcr.io") {
		t.Fatalf("saved login: %+v", c)
	}
	p.RegistryLogins = nil
	p.ImagePullable = func(context.Context, string) error { return errors.New("offline") }
	if c := find(t, Run(context.Background(), p, opts), "image"); c.Status != Warn {
		t.Fatalf("offline: %+v", c)
	}
	dev := opts
	dev.Image = "sandbox-studio-base:dev"
	if c := find(t, Run(context.Background(), p, dev), "image"); c.Status != Fail || !strings.Contains(c.Fix, "make image") {
		t.Fatalf("dev image: %+v", c)
	}
}

func TestDataAndPorts(t *testing.T) {
	p := healthy()
	p.Writable = func(string) error { return fs.ErrPermission }
	p.DiskFree = func(string) (uint64, error) { return 2 << 30, nil }
	p.PortFree = func(string) error { return errors.New("address already in use") }
	o := opts
	o.Ports = []string{"127.0.0.1:7878", "127.0.0.1:7879"}
	o.Owned = map[string]bool{"127.0.0.1:7879": true}
	r := Run(context.Background(), p, o)
	if c := find(t, r, "data-dir"); c.Status != Fail {
		t.Fatalf("data dir: %+v", c)
	}
	if c := find(t, r, "disk"); c.Status != Warn || !strings.Contains(c.Message, "2.0 GB") {
		t.Fatalf("disk: %+v", c)
	}
	if c := find(t, r, "port:127.0.0.1:7878"); c.Status != Fail {
		t.Fatalf("busy port: %+v", c)
	}
	if c := find(t, r, "port:127.0.0.1:7879"); c.Status != OK {
		t.Fatalf("owned port: %+v", c)
	}
}

func TestSSHAgent(t *testing.T) {
	p := healthy()
	p.SSHAgent = func(context.Context) (int, string, error) {
		return 0, "Start the agent.", errors.New("SSH agent not available: SSH_AUTH_SOCK is not set in Studio's environment")
	}
	o := opts
	o.AgentForges = 2
	r := Run(context.Background(), p, o)
	if c := find(t, r, "ssh-agent"); c.Status != Warn || !strings.Contains(c.Message, "needed by 2 SSH forges") || c.Fix == "" {
		t.Fatalf("agent: %+v", c)
	}
	// A forge that can't reach its agent fails only that forge, not the host.
	if r.Status != Warn {
		t.Fatalf("status %s", r.Status)
	}
	p.SSHAgent = func(context.Context) (int, string, error) { return 2, "", nil }
	if c := find(t, Run(context.Background(), p, o), "ssh-agent"); c.Status != OK || c.Message != "The SSH agent holds 2 keys." {
		t.Fatalf("agent with keys: %+v", c)
	}
	o.AgentForges = 0
	for _, c := range Run(context.Background(), p, o).Checks {
		if c.ID == "ssh-agent" {
			t.Fatal("the agent is checked without agent forges")
		}
	}
}

func TestHostProbesRun(t *testing.T) {
	// The real probes must not panic; their results depend on the host.
	p := HostProbes()
	p.ImagePullable = nil // no network in tests
	r := Run(context.Background(), p, Options{DataDir: t.TempDir(), Ports: []string{"127.0.0.1:0"}})
	if c := find(t, r, "data-dir"); c.Status != OK {
		t.Fatalf("temp dir: %+v", c)
	}
}

// An embedding model that can't load is a warning with its fix; search still works.
func TestEmbedder(t *testing.T) {
	p := healthy()
	p.Embedder = func() (bool, string, string) { return false, "", "" }
	if c := find(t, Run(context.Background(), p, opts), "embedder"); c.Status != OK || !strings.Contains(c.Message, "first memory") {
		t.Fatalf("not loaded yet: %+v", c)
	}
	p.Embedder = func() (bool, string, string) {
		return false, "load llama.cpp: libstdc++.so.6: cannot open shared object file", "enable programs.nix-ld"
	}
	r := Run(context.Background(), p, opts)
	if c := find(t, r, "embedder"); r.Status != Warn || c.Status != Warn || !strings.Contains(c.Message, "keywords only") || c.Fix != "enable programs.nix-ld" {
		t.Fatalf("broken: %s %+v", r.Status, c)
	}
}

func TestMSBDB(t *testing.T) {
	p := healthy()
	p.MSBDB = func(context.Context) (string, string, string) { return "", "", "" }
	if c := find(t, Run(context.Background(), p, opts), "msb-db"); c.Status != OK {
		t.Fatalf("sound: %+v", c)
	}
	p.MSBDB = func(context.Context) (string, string, string) {
		return "", "Studio moved the damaged database aside to /h/msb.db.broken-x and rebuilt it", ""
	}
	if c := find(t, Run(context.Background(), p, opts), "msb-db"); c.Status != Warn || !strings.Contains(c.Message, "broken-x") || c.Fix == "" {
		t.Fatalf("repaired: %+v", c)
	}
	p.MSBDB = func(context.Context) (string, string, string) {
		return "microsandbox's database is damaged: file is not a database", "", "Restart Studio"
	}
	r := Run(context.Background(), p, opts)
	if c := find(t, r, "msb-db"); r.Status != Fail || c.Status != Fail || c.Fix != "Restart Studio" {
		t.Fatalf("damaged: %s %+v", r.Status, c)
	}
}
