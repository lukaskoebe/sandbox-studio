package sshconfig

import (
	"bytes"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"golang.org/x/crypto/ssh"

	"github.com/lukaskoebe/sandbox-studio/internal/store"
)

func testService(t *testing.T) *Service {
	t.Helper()
	dir := t.TempDir()
	return &Service{
		Dir:        filepath.Join(dir, "data", "ssh"),
		UserConfig: filepath.Join(dir, "home", ".ssh", "config"),
		Exe:        "/opt/sandbox studio/studio",
		Addr:       "127.0.0.1:7878",
		Data:       "/home/u/sandbox studio",
	}
}

func TestHostsNameEachSandboxOnce(t *testing.T) {
	got := Hosts([]store.Sandbox{
		{ID: "b1", Name: "web"},
		{ID: "a1", Name: "api"},
		{ID: "c1", Name: "web"},
	})
	want := []Host{{"api.studio", "a1"}, {"web-b1.studio", "b1"}, {"web-c1.studio", "c1"}}
	if len(got) != len(want) {
		t.Fatalf("hosts = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("hosts = %v, want %v", got, want)
		}
	}
}

func TestEnsureKeepsTheKeys(t *testing.T) {
	s := testService(t)
	if err := s.Ensure(); err != nil {
		t.Fatal(err)
	}
	read := func(name string) []byte {
		t.Helper()
		b, err := os.ReadFile(filepath.Join(s.Dir, name))
		if err != nil {
			t.Fatal(err)
		}
		return b
	}
	host, client := read(hostKeyFile), read(clientKeyFile)
	if err := s.Ensure(); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(read(hostKeyFile), host) || !bytes.Equal(read(clientKeyFile), client) {
		t.Fatal("a second Ensure replaced the keys")
	}

	// The client key is the one authorized, and the host key the one known for *.studio.
	clientSigner, err := ssh.ParsePrivateKey(client)
	if err != nil {
		t.Fatal(err)
	}
	authorized, _, _, _, err := ssh.ParseAuthorizedKey(read(authorizedFile))
	if err != nil || !bytes.Equal(authorized.Marshal(), clientSigner.PublicKey().Marshal()) {
		t.Fatalf("authorized_keys doesn't hold the client key: %v", err)
	}
	hostSigner, err := ssh.ParsePrivateKey(host)
	if err != nil {
		t.Fatal(err)
	}
	_, hosts, known, _, _, err := ssh.ParseKnownHosts(read(knownHostsFile))
	if err != nil || len(hosts) != 1 || hosts[0] != "*.studio" || !bytes.Equal(known.Marshal(), hostSigner.PublicKey().Marshal()) {
		t.Fatalf("known_hosts = %v %v: %v", hosts, known, err)
	}
	if runtime.GOOS != "windows" {
		for _, name := range []string{hostKeyFile, clientKeyFile, authorizedFile} {
			info, err := os.Stat(filepath.Join(s.Dir, name))
			if err != nil || info.Mode().Perm() != 0o600 {
				t.Fatalf("%s mode = %v: %v", name, info.Mode(), err)
			}
		}
	}

	// A lost authorized_keys comes back for the same key.
	if err := os.Remove(filepath.Join(s.Dir, authorizedFile)); err != nil {
		t.Fatal(err)
	}
	if err := s.Ensure(); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(read(clientKeyFile), client) || len(read(authorizedFile)) == 0 {
		t.Fatal("Ensure did not restore authorized_keys for the same key")
	}
}

func TestRenderNamesTheSandboxes(t *testing.T) {
	s := testService(t)
	got := string(s.Render([]Host{{"api.studio", "a1"}}))
	s.MSBHome = "/tmp/msb"
	if withHome := string(s.Render(nil)); !strings.Contains(withHome, `-data "/home/u/sandbox studio" -msb-home "/tmp/msb" %h`) {
		t.Fatalf("config with MSB_HOME:\n%s", withHome)
	}
	for _, want := range []string{
		"Host api.studio\n    HostName a1.studio\n",
		"Host *.studio\n    User agent\n",
		`ProxyCommand "/opt/sandbox studio/studio" -addr 127.0.0.1:7878 ssh-proxy -data "/home/u/sandbox studio" %h`,
		`IdentityFile "` + filepath.Join(s.Dir, "id_ed25519") + `"`,
		"IdentitiesOnly yes",
		"StrictHostKeyChecking yes",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("config lacks %q:\n%s", want, got)
		}
	}
}

// OpenSSH reads the file as meant: a sandbox's alias gets its ID as the host name, and
// every host the proxy, Studio's key and Studio's known hosts.
func TestOpenSSHReadsTheConfig(t *testing.T) {
	sshBin, err := exec.LookPath("ssh")
	if err != nil {
		t.Skip("no ssh client")
	}
	s := testService(t)
	if err := s.Ensure(); err != nil {
		t.Fatal(err)
	}
	if err := s.Write([]Host{{"api.studio", "a1"}}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Enable(); err != nil {
		t.Fatal(err)
	}
	resolve := func(host string) map[string]string {
		t.Helper()
		out, err := exec.Command(sshBin, "-G", "-F", s.UserConfig, host).Output()
		if err != nil {
			t.Fatalf("ssh -G %s: %v", host, err)
		}
		opts := map[string]string{}
		// ssh on Windows ends its lines with \r\n.
		for _, line := range strings.Split(strings.ReplaceAll(string(out), "\r", ""), "\n") {
			k, v, _ := strings.Cut(line, " ")
			if _, seen := opts[k]; !seen {
				opts[k] = v
			}
		}
		return opts
	}
	opts := resolve("api.studio")
	if opts["hostname"] != "a1.studio" || opts["user"] != "agent" {
		t.Fatalf("api.studio: hostname %q, user %q", opts["hostname"], opts["user"])
	}
	if want := `"/opt/sandbox studio/studio" -addr 127.0.0.1:7878 ssh-proxy -data "/home/u/sandbox studio" %h`; opts["proxycommand"] != want {
		t.Fatalf("proxycommand = %q, want %q", opts["proxycommand"], want)
	}
	// ssh on Windows may print a path with forward slashes or another case.
	has := func(opt, file string) bool {
		return strings.Contains(strings.ToLower(filepath.ToSlash(opts[opt])), strings.ToLower(filepath.ToSlash(filepath.Join(s.Dir, file))))
	}
	if !has("identityfile", "id_ed25519") || !has("userknownhostsfile", "known_hosts") {
		t.Fatalf("identityfile %q, userknownhostsfile %q", opts["identityfile"], opts["userknownhostsfile"])
	}
	// A sandbox the file doesn't list yet is reached by its name or ID all the same.
	if opts := resolve("new.studio"); opts["hostname"] != "new.studio" || opts["proxycommand"] == "" {
		t.Fatalf("new.studio: hostname %q, proxycommand %q", opts["hostname"], opts["proxycommand"])
	}
	// Other hosts are untouched.
	if opts := resolve("example.com"); opts["proxycommand"] != "" && opts["proxycommand"] != "none" {
		t.Fatalf("example.com got proxycommand %q", opts["proxycommand"])
	}
}

func TestEnableAndDisableKeepTheUsersConfig(t *testing.T) {
	s := testService(t)
	st, err := s.Status()
	if err != nil || st.Enabled {
		t.Fatalf("status before = %+v, %v", st, err)
	}

	// Without a config, Enable creates one, private, in a private ~/.ssh.
	if st, err = s.Enable(); err != nil || !st.Enabled {
		t.Fatalf("enable = %+v, %v", st, err)
	}
	if runtime.GOOS != "windows" {
		if info, err := os.Stat(s.UserConfig); err != nil || info.Mode().Perm() != 0o600 {
			t.Fatalf("new config mode = %v, %v", info.Mode(), err)
		}
		if info, err := os.Stat(filepath.Dir(s.UserConfig)); err != nil || info.Mode().Perm() != 0o700 {
			t.Fatalf("new ~/.ssh mode = %v, %v", info.Mode(), err)
		}
	}
	if _, err := s.Disable(); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(s.UserConfig); len(b) != 0 {
		t.Fatalf("config after disable = %q, want empty", b)
	}

	// An existing config gets the Include on top, once, and gets back what it had.
	mine := "Host *\n    ServerAliveInterval 30\n\nHost work\n    HostName work.example.com\n"
	if err := os.WriteFile(s.UserConfig, []byte(mine), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(s.UserConfig, 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Enable(); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Enable(); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(s.UserConfig)
	want := includeComment + "\nInclude \"" + s.Config() + "\"\n\n" + mine
	if string(b) != want {
		t.Fatalf("config after enable =\n%s\nwant\n%s", b, want)
	}
	if runtime.GOOS != "windows" {
		if info, _ := os.Stat(s.UserConfig); info.Mode().Perm() != 0o644 {
			t.Fatalf("enable changed the mode to %v", info.Mode())
		}
	}
	if st, err := s.Disable(); err != nil || st.Enabled {
		t.Fatalf("disable = %+v, %v", st, err)
	}
	if b, _ := os.ReadFile(s.UserConfig); string(b) != mine {
		t.Fatalf("config after disable =\n%s\nwant\n%s", b, mine)
	}
}

// A dotfile manager's symlinked config is written where it points; a read-only one says
// which line to add by hand.
func TestEnableThroughASymlinkAndReadOnly(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlinks and modes")
	}
	s := testService(t)
	target := filepath.Join(t.TempDir(), "dotfiles-ssh-config")
	if err := os.WriteFile(target, []byte("Host x\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(s.UserConfig), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, s.UserConfig); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Enable(); err != nil {
		t.Fatal(err)
	}
	if info, err := os.Lstat(s.UserConfig); err != nil || info.Mode()&os.ModeSymlink == 0 {
		t.Fatal("enable replaced the symlink")
	}
	if b, _ := os.ReadFile(target); !strings.HasPrefix(string(b), includeComment) {
		t.Fatalf("target = %q", b)
	}
	if _, err := s.Disable(); err != nil {
		t.Fatal(err)
	}

	if os.Geteuid() == 0 {
		t.Skip("root writes read-only directories")
	}
	if err := os.Chmod(filepath.Dir(target), 0o500); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(filepath.Dir(target), 0o700) })
	_, err := s.Enable()
	var we *WriteError
	if !errors.As(err, &we) || !strings.Contains(err.Error(), `Include "`+s.Config()+`"`) {
		t.Fatalf("enable on a read-only config = %v, want a WriteError naming the line", err)
	}
}
