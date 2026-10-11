package gitreview

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/binary"
	"errors"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/go-git/go-billy/v5/memfs"
	git "github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/storage/memory"
	"golang.org/x/crypto/ssh"

	"github.com/lukaskoebe/sandbox-studio/internal/integrations"
	"github.com/lukaskoebe/sandbox-studio/internal/store"
)

// --- fake SSH forge -----------------------------------------------------------------------

// fakeSSH is an SSH server that runs the real git upload-pack and receive-pack on bare
// repositories under root, for the keys in authorized. It records what clients ask for.
type fakeSSH struct {
	t          *testing.T
	gitBin     string
	root       string
	hostSigner ssh.Signer
	ln         net.Listener

	mu         sync.Mutex
	authorized []ssh.PublicKey
	users      []string
	commands   []string
	env        []string // name=value of env requests
	refused    []string // other channel and session requests
	noProtocol bool     // refuse GIT_PROTOCOL too
}

func newFakeSSH(t *testing.T) *fakeSSH {
	t.Helper()
	gitBin, err := exec.LookPath("git")
	if err != nil {
		t.Skip("no git binary")
	}
	_, priv, _ := ed25519.GenerateKey(rand.Reader)
	signer, err := ssh.NewSignerFromKey(priv)
	if err != nil {
		t.Fatal(err)
	}
	f := &fakeSSH{t: t, gitBin: gitBin, root: t.TempDir(), hostSigner: signer}
	f.ln, err = net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { f.ln.Close() })
	cfg := &ssh.ServerConfig{
		PublicKeyCallback: func(c ssh.ConnMetadata, k ssh.PublicKey) (*ssh.Permissions, error) {
			f.mu.Lock()
			defer f.mu.Unlock()
			for _, a := range f.authorized {
				if string(a.Marshal()) == string(k.Marshal()) {
					f.users = append(f.users, c.User())
					return nil, nil
				}
			}
			return nil, errors.New("unknown key")
		},
	}
	cfg.AddHostKey(signer)
	go func() {
		for {
			conn, err := f.ln.Accept()
			if err != nil {
				return
			}
			go f.serve(conn, cfg)
		}
	}()
	return f
}

func (f *fakeSSH) hostKey() string {
	return strings.TrimSpace(string(ssh.MarshalAuthorizedKey(f.hostSigner.PublicKey())))
}

func (f *fakeSSH) authorize(pub string) {
	k, _, _, _, err := ssh.ParseAuthorizedKey([]byte(pub))
	if err != nil {
		f.t.Fatal(err)
	}
	f.mu.Lock()
	f.authorized = append(f.authorized, k)
	f.mu.Unlock()
}

func (f *fakeSSH) git(args ...string) string {
	f.t.Helper()
	cmd := exec.Command(f.gitBin, args...)
	cmd.Env = append(os.Environ(), "GIT_CONFIG_NOSYSTEM=1", "HOME="+f.root, "LC_ALL=C")
	out, err := cmd.CombinedOutput()
	if err != nil {
		f.t.Fatalf("git %v: %v\n%s", args, err, out)
	}
	return strings.TrimSpace(string(out))
}

// repo makes a bare repository at path (relative to root, .git added) with one commit on
// main.
func (f *fakeSSH) repo(path string) string {
	f.t.Helper()
	dir := filepath.Join(f.root, path+".git")
	f.git("init", "-q", "--bare", "-b", "main", dir)
	work := f.t.TempDir()
	f.git("clone", "-q", dir, work)
	os.WriteFile(filepath.Join(work, "README.md"), []byte("hello\n"), 0o644)
	f.git("-C", work, "add", ".")
	f.git("-C", work, "-c", "user.name=Up", "-c", "user.email=up@example.com", "commit", "-q", "-m", "Initial commit")
	f.git("-C", work, "push", "-q", "origin", "HEAD:main")
	return dir
}

func (f *fakeSSH) serve(nc net.Conn, cfg *ssh.ServerConfig) {
	conn, chans, reqs, err := ssh.NewServerConn(nc, cfg)
	if err != nil {
		return
	}
	defer conn.Close()
	go func() {
		for r := range reqs {
			f.refuse(r.Type)
			r.Reply(false, nil)
		}
	}()
	for nch := range chans {
		if nch.ChannelType() != "session" {
			f.refuse("channel " + nch.ChannelType())
			nch.Reject(ssh.UnknownChannelType, "no")
			continue
		}
		ch, creqs, err := nch.Accept()
		if err != nil {
			continue
		}
		go f.session(ch, creqs)
	}
}

func (f *fakeSSH) refuse(what string) {
	f.mu.Lock()
	f.refused = append(f.refused, what)
	f.mu.Unlock()
}

func (f *fakeSSH) session(ch ssh.Channel, reqs <-chan *ssh.Request) {
	defer ch.Close()
	var env []string
	for r := range reqs {
		switch r.Type {
		case "env":
			var kv struct{ Name, Value string }
			ssh.Unmarshal(r.Payload, &kv)
			f.mu.Lock()
			f.env = append(f.env, kv.Name+"="+kv.Value)
			f.mu.Unlock()
			// Like a stock sshd, accept GIT_PROTOCOL only.
			ok := kv.Name == "GIT_PROTOCOL" && !f.noProtocol
			if ok {
				env = append(env, kv.Name+"="+kv.Value)
			}
			r.Reply(ok, nil)
		case "exec":
			var cmd struct{ Command string }
			ssh.Unmarshal(r.Payload, &cmd)
			f.mu.Lock()
			f.commands = append(f.commands, cmd.Command)
			f.mu.Unlock()
			r.Reply(true, nil)
			f.exec(ch, cmd.Command, env)
			return
		default:
			f.refuse(r.Type)
			r.Reply(false, nil)
		}
	}
}

func (f *fakeSSH) exec(ch ssh.Channel, command string, env []string) {
	status := uint32(1)
	defer func() {
		ch.SendRequest("exit-status", false, binary.BigEndian.AppendUint32(nil, status))
	}()
	service, arg, ok := strings.Cut(command, " ")
	if !ok || (service != "git-upload-pack" && service != "git-receive-pack") || len(arg) < 2 || arg[0] != '\'' || arg[len(arg)-1] != '\'' {
		fmt.Fprintf(ch.Stderr(), "fatal: unexpected command %q\n", command)
		return
	}
	dir := filepath.Join(f.root, strings.TrimSuffix(arg[1:len(arg)-1], ".git")+".git")
	if _, err := os.Stat(dir); err != nil {
		fmt.Fprintf(ch.Stderr(), "ERROR: Repository not found.\n")
		return
	}
	cmd := exec.Command(f.gitBin, strings.TrimPrefix(service, "git-"), dir)
	cmd.Env = append(append(os.Environ(), "GIT_CONFIG_NOSYSTEM=1", "HOME="+f.root), env...)
	cmd.Stdin, cmd.Stdout, cmd.Stderr = ch, ch, ch.Stderr()
	if err := cmd.Run(); err == nil {
		status = 0
	}
}

// dialer connects every address to the fake server, as if DNS pointed there.
func (f *fakeSSH) dialer(ctx context.Context, network, _ string) (net.Conn, error) {
	var d net.Dialer
	return d.DialContext(ctx, network, f.ln.Addr().String())
}

// --- harness ------------------------------------------------------------------------------

type sshHarness struct {
	*harness
	ssh   *fakeSSH
	forge store.Forge
	key   string
}

// newSSHHarness adds forge "box" at ssh://git@git.example.com:2222 with a generated key the
// fake server accepts and its host key pinned, and a repository owner/repo.
func newSSHHarness(t *testing.T) *sshHarness {
	t.Helper()
	// Keep the key-leak scan within this test, including any temporary files
	// opened by the SSH transport. Shared temp directories can contain FIFOs.
	tmp := t.TempDir()
	for _, name := range []string{"TMPDIR", "TMP", "TEMP"} {
		t.Setenv(name, tmp)
	}
	fs := newFakeSSH(t)
	h := newHarness(t)
	h.svc.Dial = fs.dialer
	key, pub, err := GenerateKey("sandbox-studio/test/box")
	if err != nil {
		t.Fatal(err)
	}
	fs.authorize(pub)
	f, err := h.st.CreateForge(h.ctx, store.Forge{ID: store.NewID(), EnvironmentID: h.env.ID, Name: "box", Kind: KindSSH,
		BaseURL: "ssh://git@git.example.com:2222", Username: "git", PublicKey: pub, HostKey: fs.hostKey()},
		store.Secret{ID: store.NewID(), Name: "FORGE_BOX_SSH_KEY", Sealed: []byte("sealed"), Placeholder: "box-placeholder"})
	if err != nil {
		t.Fatal(err)
	}
	h.svc.Secrets.(fakeSecrets)[h.env.ID+"/"+f.SecretID] = key
	fs.repo("owner/repo")
	return &sshHarness{harness: h, ssh: fs, forge: f, key: key}
}

func (sh *sshHarness) assertKeyHidden(t *testing.T, g *guest) {
	t.Helper()
	body := strings.Split(sh.key, "\n")[1]
	if s := g.seen(); strings.Contains(s, body) || strings.Contains(s, "PRIVATE KEY") {
		t.Fatal("the key reached the sandbox")
	}
	// Nothing outside the vault holds the key: not the staging repositories, not the
	// fake forge's files (which stand in for anything Studio might write).
	for _, dir := range []string{sh.svc.Dir, os.TempDir()} {
		filepath.WalkDir(dir, func(path string, d os.DirEntry, err error) error {
			if err != nil || d.IsDir() {
				return nil
			}
			if info, _ := d.Info(); info == nil || !info.Mode().IsRegular() || info.Size() > 1<<20 {
				return nil
			}
			if data, _ := os.ReadFile(path); strings.Contains(string(data), body) {
				t.Errorf("the key is in %s", path)
			}
			return nil
		})
	}
}

// --- tests --------------------------------------------------------------------------------

func TestGenerateKey(t *testing.T) {
	key, pub, err := GenerateKey("sandbox-studio/env/box")
	if err != nil {
		t.Fatal(err)
	}
	signer, err := ssh.ParsePrivateKey([]byte(key))
	if err != nil || !strings.HasPrefix(key, "-----BEGIN OPENSSH PRIVATE KEY-----") {
		t.Fatalf("private key: %v", err)
	}
	k, comment, _, _, err := ssh.ParseAuthorizedKey([]byte(pub))
	if err != nil || comment != "sandbox-studio/env/box" || k.Type() != ssh.KeyAlgoED25519 || string(k.Marshal()) != string(signer.PublicKey().Marshal()) {
		t.Fatalf("public key %q: %v", pub, err)
	}
}

func TestParseSSHURL(t *testing.T) {
	for in, want := range map[string]string{
		"git@github.com":                 "ssh://git@github.com",
		"git@github.com:":                "ssh://git@github.com",
		"github.com":                     "ssh://git@github.com",
		"ssh://gitolite@Git.Example.org": "ssh://gitolite@git.example.org",
		"ssh://git@host.example:2222/":   "ssh://git@host.example:2222",
		"ssh://git@host.example:22":      "ssh://git@host.example",
		"ssh://git@[2606:4700::1]:2222":  "ssh://git@[2606:4700::1]:2222",
	} {
		u, err := ParseSSHURL(in)
		if err != nil || u.Base != want {
			t.Errorf("%q = %q, %v; want %q", in, u.Base, err, want)
		}
	}
	for _, in := range []string{
		"", "git@github.com:owner/repo.git", "ssh://git@github.com/owner/repo", "https://github.com",
		"git@localhost", "git@10.0.0.1", "ssh://git@127.0.0.1:22", "ssh://git@[::1]", "a b@host", "git@host.example:99999",
		"git@git.studio.internal", "ssh://u:p@host.example",
	} {
		if u, err := ParseSSHURL(in); !errors.Is(err, ErrInvalid) {
			t.Errorf("%q accepted as %q", in, u.Base)
		}
	}
}

func TestScanHostKeyAndPrivateAddresses(t *testing.T) {
	fs := newFakeSSH(t)
	svc := &Service{Dial: fs.dialer}
	u, hk, err := svc.ScanHostKey(context.Background(), "git@git.example.com:")
	if err != nil || hk.Key != fs.hostKey() || hk.Fingerprint != ssh.FingerprintSHA256(fs.hostSigner.PublicKey()) || hk.Algorithm != ssh.KeyAlgoED25519 || u.Base != "ssh://git@git.example.com" {
		t.Fatalf("scan = %+v %+v, %v", u, hk, err)
	}
	if got, err := CheckHostKey(hk.Key + " comment"); err != nil || got != hk {
		t.Fatalf("check host key = %+v, %v", got, err)
	}
	if _, err := CheckHostKey("ssh-dss AAAA"); !errors.Is(err, ErrInvalid) {
		t.Fatalf("bad host key: %v", err)
	}
	// Without a test dialer, Studio connects to public addresses only, so a name that
	// resolves to a private address is refused as well as a literal one.
	public := &Service{}
	if _, _, err := public.ScanHostKey(context.Background(), "git@127.0.0.1"); !errors.Is(err, ErrInvalid) {
		t.Fatalf("scan of a loopback address: %v", err)
	}
	if _, err := public.dial(context.Background(), fs.ln.Addr().String()); err == nil {
		t.Fatal("dialed a loopback address")
	}
}

func TestSSHForgeFetchPushAndApprove(t *testing.T) {
	sh := newSSHHarness(t)
	g := sh.guest(t, sh.env.ID, "one")
	repo, err := git.Clone(memory.NewStorage(), memfs.New(), &git.CloneOptions{URL: g.srv.URL + "/box/owner/repo.git"})
	if err != nil {
		t.Fatalf("clone: %v", err)
	}
	commitFile(t, repo, "ssh.txt", "over ssh\n", "Over SSH")
	if err := push(repo, "refs/heads/main:refs/heads/feature", false); err != nil {
		t.Fatalf("push: %v", err)
	}
	upstream := filepath.Join(sh.ssh.root, "owner/repo.git")
	if out := sh.ssh.git("--git-dir", upstream, "branch", "--list", "feature"); out != "" {
		t.Fatal("the push reached the forge before approval")
	}
	pending := sh.approvals(t, KindPush, store.StatusPending)
	if len(pending) != 1 || !strings.Contains(string(pending[0].Payload), "over SSH to ssh://git@git.example.com:2222/owner/repo, host key SHA256:") {
		t.Fatalf("pending = %+v", pending)
	}
	sh.decide(t, pending[0], integrations.Allow, "")
	if out := sh.ssh.git("--git-dir", upstream, "log", "-1", "--format=%s", "feature"); out != "Over SSH" {
		t.Fatalf("upstream feature = %q", out)
	}
	if prs := sh.approvals(t, KindPR, store.StatusPending); len(prs) != 0 {
		t.Fatal("an ssh forge got a PR proposal")
	}

	sh.ssh.mu.Lock()
	for _, c := range sh.ssh.commands {
		if c != "git-upload-pack 'owner/repo'" && c != "git-receive-pack 'owner/repo'" {
			t.Errorf("command %q", c)
		}
	}
	for _, e := range sh.ssh.env {
		if !strings.HasPrefix(e, "GIT_PROTOCOL=") {
			t.Errorf("env %q", e)
		}
	}
	for _, u := range sh.ssh.users {
		if u != "git" {
			t.Errorf("user %q", u)
		}
	}
	if len(sh.ssh.refused) != 0 { // no agent forwarding, pty, shell or port forwarding
		t.Errorf("refused requests: %v", sh.ssh.refused)
	}
	sh.ssh.mu.Unlock()

	c, err := sh.svc.TestForge(sh.ctx, sh.forge, "owner/repo")
	if err != nil || c.Branches != 2 {
		t.Fatalf("test = %+v, %v", c, err)
	}
	if _, err := sh.svc.TestForge(sh.ctx, sh.forge, "owner/missing"); err == nil || !strings.Contains(err.Error(), "no repository owner/missing") {
		t.Fatalf("test of a missing repository: %v", err)
	}
	sh.assertKeyHidden(t, g)
}

func TestSSHHostKeyMismatch(t *testing.T) {
	sh := newSSHHarness(t)
	_, other, _ := ed25519.GenerateKey(rand.Reader)
	otherSigner, _ := ssh.NewSignerFromKey(other)
	pinned := strings.TrimSpace(string(ssh.MarshalAuthorizedKey(otherSigner.PublicKey())))
	if err := sh.st.PinForgeHostKey(sh.ctx, sh.env.ID, sh.forge.ID, pinned); err != nil {
		t.Fatal(err)
	}
	g := sh.guest(t, sh.env.ID, "one")
	_, err := git.Clone(memory.NewStorage(), memfs.New(), &git.CloneOptions{URL: g.srv.URL + "/box/owner/repo.git"})
	if err == nil {
		t.Fatal("cloned through a changed host key")
	}
	if !strings.Contains(g.seen(), "host key of git.example.com changed") {
		t.Fatalf("the sandbox was not told: %s", g.seen())
	}
	sh.ssh.mu.Lock()
	n := len(sh.ssh.commands)
	sh.ssh.mu.Unlock()
	if n != 0 {
		t.Fatal("a command ran on a host with the wrong key")
	}
	f, _ := sh.st.Forge(sh.ctx, sh.env.ID, sh.forge.ID)
	if f.HostKeyMismatch != ssh.FingerprintSHA256(sh.ssh.hostSigner.PublicKey()) {
		t.Fatalf("offered fingerprint = %q", f.HostKeyMismatch)
	}
	_, err = sh.svc.TestForge(sh.ctx, f, "owner/repo")
	var changed *HostKeyChangedError
	if !errors.As(err, &changed) || changed.Pinned != ssh.FingerprintSHA256(otherSigner.PublicKey()) {
		t.Fatalf("test: %v", err)
	}
	// Re-pinning the offered key works again.
	if err := sh.st.PinForgeHostKey(sh.ctx, sh.env.ID, f.ID, sh.ssh.hostKey()); err != nil {
		t.Fatal(err)
	}
	f, _ = sh.st.Forge(sh.ctx, sh.env.ID, f.ID)
	if _, err := sh.svc.TestForge(sh.ctx, f, "owner/repo"); err != nil || f.HostKeyMismatch != "" {
		t.Fatalf("after re-pin: %v", err)
	}
}

func TestSSHUnknownKeyIsRefused(t *testing.T) {
	sh := newSSHHarness(t)
	key, _, _ := GenerateKey("other")
	sh.svc.Secrets.(fakeSecrets)[sh.env.ID+"/"+sh.forge.SecretID] = key
	_, err := sh.svc.TestForge(sh.ctx, sh.forge, "owner/repo")
	if err == nil || !strings.Contains(err.Error(), "refused Studio's key") {
		t.Fatalf("test with an unknown key: %v", err)
	}
}

// TestSSHRealGit bridges the git binary's fetches to SSH with protocol v2 and v0, including
// v0 negotiation over several rounds, and pushes through the staging repository.
func TestSSHRealGit(t *testing.T) {
	sh := newSSHHarness(t)
	g := sh.guest(t, sh.env.ID, "one")
	home := t.TempDir()
	run := func(dir string, args ...string) string {
		t.Helper()
		cmd := exec.Command(sh.ssh.gitBin, args...)
		cmd.Dir = dir
		cmd.Env = append(os.Environ(), "HOME="+home, "XDG_CONFIG_HOME="+home, "GIT_CONFIG_NOSYSTEM=1",
			"GIT_TERMINAL_PROMPT=0", "GIT_ASKPASS=/bin/false", "LC_ALL=C",
			"GIT_AUTHOR_NAME=Agent", "GIT_AUTHOR_EMAIL=agent@example.com",
			"GIT_COMMITTER_NAME=Agent", "GIT_COMMITTER_EMAIL=agent@example.com")
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
		return strings.TrimSpace(string(out))
	}
	upstream := filepath.Join(sh.ssh.root, "owner/repo.git")
	upWork := t.TempDir()
	sh.ssh.git("clone", "-q", upstream, upWork)
	upCommit := func(msg string) {
		os.WriteFile(filepath.Join(upWork, "up.txt"), []byte(msg), 0o644)
		sh.ssh.git("-C", upWork, "add", ".")
		sh.ssh.git("-C", upWork, "-c", "user.name=Up", "-c", "user.email=up@example.com", "commit", "-q", "-m", msg)
		sh.ssh.git("-C", upWork, "push", "-q", "origin", "HEAD:main")
	}

	for _, version := range []string{"2", "0"} {
		t.Run("protocol v"+version, func(t *testing.T) {
			work := filepath.Join(home, "v"+version)
			run(home, "-c", "protocol.version="+version, "clone", "-q", g.srv.URL+"/box/owner/repo.git", work)
			run(work, "config", "protocol.version", version)
			// Many local commits the forge lacks make the fetch negotiate over several
			// rounds before it finds the common one.
			for i := range 40 {
				os.WriteFile(filepath.Join(work, "local.txt"), []byte(fmt.Sprint(i)), 0o644)
				run(work, "add", ".")
				run(work, "commit", "-q", "-m", fmt.Sprint("local ", i))
			}
			upCommit("upstream for v" + version)
			sh.ssh.mu.Lock()
			before := len(sh.ssh.commands)
			sh.ssh.mu.Unlock()
			run(work, "fetch", "-q", "origin")
			sh.ssh.mu.Lock()
			runs := len(sh.ssh.commands) - before
			sh.ssh.mu.Unlock()
			// The advertisement, at least two negotiation rounds and the pack.
			if runs < 3 {
				t.Fatalf("the fetch ran upload-pack %d times; expected several rounds", runs)
			}
			if got := run(work, "log", "-1", "--format=%s", "origin/main"); got != "upstream for v"+version {
				t.Fatalf("fetched origin/main = %q", got)
			}
			run(work, "push", "-q", "origin", "HEAD:refs/heads/v"+version)
		})
	}
	pending := sh.approvals(t, KindPush, store.StatusPending)
	if len(pending) != 2 {
		t.Fatalf("pending pushes = %d", len(pending))
	}
	for _, a := range pending {
		sh.decide(t, a, integrations.Allow, "")
	}
	for _, v := range []string{"v2", "v0"} {
		if got := sh.ssh.git("--git-dir", upstream, "log", "-1", "--format=%s", v); got != "local 39" {
			t.Fatalf("upstream %s = %q", v, got)
		}
	}
	sh.ssh.mu.Lock()
	sawV2 := false
	for _, e := range sh.ssh.env {
		sawV2 = sawV2 || e == "GIT_PROTOCOL=version=2"
	}
	sh.ssh.mu.Unlock()
	if !sawV2 {
		t.Fatal("protocol v2 was never asked for")
	}
	sh.assertKeyHidden(t, g)
}

// TestSSHForgeWithoutGitProtocol is a server that refuses GIT_PROTOCOL, as a stock sshd
// does: the sandbox's git asks for v2 and gets v0.
func TestSSHForgeWithoutGitProtocol(t *testing.T) {
	sh := newSSHHarness(t)
	sh.ssh.noProtocol = true
	g := sh.guest(t, sh.env.ID, "one")
	work := filepath.Join(t.TempDir(), "w")
	cmd := exec.Command(sh.ssh.gitBin, "-c", "protocol.version=2", "clone", "-q", g.srv.URL+"/box/owner/repo.git", work)
	cmd.Env = append(os.Environ(), "GIT_CONFIG_NOSYSTEM=1", "HOME="+t.TempDir(), "GIT_TERMINAL_PROMPT=0")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("clone: %v\n%s", err, out)
	}
	if !strings.Contains(g.seen(), "# service=git-upload-pack") {
		t.Fatal("expected a v0 advertisement")
	}
}
