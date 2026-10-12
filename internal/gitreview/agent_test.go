package gitreview

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"errors"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/go-git/go-billy/v5/memfs"
	git "github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/storage/memory"
	"golang.org/x/crypto/ssh"
	"golang.org/x/crypto/ssh/agent"

	"github.com/lukaskoebe/sandbox-studio/internal/integrations"
	"github.com/lukaskoebe/sandbox-studio/internal/store"
)

// serveAgent serves a on a temporary unix socket and points SSH_AUTH_SOCK at it, as a
// user's ssh-agent would be.
func serveAgent(t *testing.T, a agent.Agent) {
	t.Helper()
	// Unix socket paths are short (104 bytes on macOS); the test's TMPDIR may be long.
	dir, err := os.MkdirTemp("", "sa")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	sock := filepath.Join(dir, "agent.sock")
	ln, err := net.Listen("unix", sock)
	if err != nil {
		t.Skipf("no unix sockets: %v", err)
	}
	t.Cleanup(func() { ln.Close() })
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			go func() {
				defer c.Close()
				agent.ServeAgent(a, c)
			}()
		}
	}()
	t.Setenv("SSH_AUTH_SOCK", sock)
}

// addAgentKey adds a new ed25519 key to the keyring and returns its public key.
func addAgentKey(t *testing.T, a agent.Agent, comment string) string {
	t.Helper()
	_, priv, _ := ed25519.GenerateKey(rand.Reader)
	if err := a.Add(agent.AddedKey{PrivateKey: priv, Comment: comment}); err != nil {
		t.Fatal(err)
	}
	pub, _ := ssh.NewPublicKey(priv.Public())
	return strings.TrimSpace(string(ssh.MarshalAuthorizedKey(pub)))
}

// slowAgent waits before every signature, like an agent waiting for a touch.
type slowAgent struct {
	agent.ExtendedAgent
	delay time.Duration
	signs atomic.Int32
}

func (s *slowAgent) Sign(key ssh.PublicKey, data []byte) (*ssh.Signature, error) {
	s.signs.Add(1)
	time.Sleep(s.delay)
	return s.ExtendedAgent.Sign(key, data)
}

func (s *slowAgent) SignWithFlags(key ssh.PublicKey, data []byte, flags agent.SignatureFlags) (*ssh.Signature, error) {
	s.signs.Add(1)
	time.Sleep(s.delay)
	return s.ExtendedAgent.SignWithFlags(key, data, flags)
}

// newAgentHarness is newSSHHarness with forge "box" switched to the SSH agent: the
// keyring holds several keys and the fake server accepts only the last one.
func newAgentHarness(t *testing.T, a agent.Agent) *sshHarness {
	t.Helper()
	serveAgent(t, a)
	addAgentKey(t, a, "laptop")
	addAgentKey(t, a, "work")
	pub := addAgentKey(t, a, "forge")
	sh := newSSHHarness(t)
	sh.ssh.mu.Lock()
	sh.ssh.authorized = nil // not Studio's generated key
	sh.ssh.mu.Unlock()
	sh.ssh.authorize(pub)
	if err := sh.st.SetForgeAgent(sh.ctx, sh.env.ID, sh.forge.ID); err != nil {
		t.Fatal(err)
	}
	f, err := sh.st.Forge(sh.ctx, sh.env.ID, sh.forge.ID)
	if err != nil || f.Auth != store.ForgeAuthAgent || f.SecretID != "" {
		t.Fatalf("agent forge = %+v, %v", f, err)
	}
	sh.forge = f
	return sh
}

func TestSSHAgentForgeFetchPushAndApprove(t *testing.T) {
	keyring := agent.NewKeyring()
	sh := newAgentHarness(t, keyring)
	g := sh.guest(t, sh.env.ID, "one")
	repo, err := git.Clone(memory.NewStorage(), memfs.New(), &git.CloneOptions{URL: g.srv.URL + "/box/owner/repo.git"})
	if err != nil {
		t.Fatalf("clone: %v", err)
	}
	commitFile(t, repo, "agent.txt", "signed by the agent\n", "Over the agent")
	if err := push(repo, "refs/heads/main:refs/heads/feature", false); err != nil {
		t.Fatalf("push: %v", err)
	}
	pending := sh.approvals(t, KindPush, store.StatusPending)
	if len(pending) != 1 {
		t.Fatalf("pending = %+v", pending)
	}
	sh.decide(t, pending[0], integrations.Allow, "")
	upstream := filepath.Join(sh.ssh.root, "owner/repo.git")
	if out := sh.ssh.git("--git-dir", upstream, "log", "-1", "--format=%s", "feature"); out != "Over the agent" {
		t.Fatalf("upstream feature = %q", out)
	}
	sh.ssh.mu.Lock()
	refused := sh.ssh.refused
	sh.ssh.mu.Unlock()
	if len(refused) != 0 { // in particular no auth-agent forwarding request
		t.Fatalf("refused requests: %v", refused)
	}
	// A fast agent leaves no hint.
	if f, _ := sh.st.Forge(sh.ctx, sh.env.ID, sh.forge.ID); f.AgentSlowAt != nil {
		t.Fatalf("slow sign recorded for a fast agent: %v", f.AgentSlowAt)
	}
}

func TestSSHAgentSlowSignIsRecorded(t *testing.T) {
	slow := &slowAgent{ExtendedAgent: agent.NewKeyring().(agent.ExtendedAgent), delay: 150 * time.Millisecond}
	sh := newAgentHarness(t, slow)
	sh.svc.slowSign = 100 * time.Millisecond
	c, err := sh.svc.TestForge(sh.ctx, sh.forge, "owner/repo")
	if err != nil || c.Branches != 1 {
		t.Fatalf("test = %+v, %v", c, err)
	}
	// The client asks the server before signing, so only the accepted key signs.
	if n := slow.signs.Load(); n != 1 {
		t.Fatalf("the agent signed %d times", n)
	}
	f, _ := sh.st.Forge(sh.ctx, sh.env.ID, sh.forge.ID)
	if f.AgentSlowAt == nil || time.Since(*f.AgentSlowAt) > time.Minute || f.AgentHintDismissed {
		t.Fatalf("slow sign = %+v", f)
	}
}

func TestSSHAgentRefusedAndMissing(t *testing.T) {
	keyring := agent.NewKeyring()
	sh := newAgentHarness(t, keyring)
	sh.ssh.mu.Lock()
	sh.ssh.authorized = nil
	sh.ssh.mu.Unlock()
	_, err := sh.svc.TestForge(sh.ctx, sh.forge, "owner/repo")
	if err == nil || !strings.Contains(err.Error(), "refused the keys in your SSH agent") {
		t.Fatalf("refused keys: %v", err)
	}

	keyring.RemoveAll()
	_, err = sh.svc.TestForge(sh.ctx, sh.forge, "owner/repo")
	if !errors.Is(err, ErrNoAgent) || !strings.Contains(err.Error(), "no keys") {
		t.Fatalf("empty agent: %v", err)
	}
	if st := sh.svc.AgentStatus(sh.ctx); st.Available || st.Message == "" {
		t.Fatalf("status of an empty agent = %+v", st)
	}

	t.Setenv("SSH_AUTH_SOCK", "")
	os.Unsetenv("SSH_AUTH_SOCK")
	_, err = sh.svc.TestForge(sh.ctx, sh.forge, "owner/repo")
	if !errors.Is(err, ErrNoAgent) || !strings.Contains(err.Error(), "SSH_AUTH_SOCK") {
		t.Fatalf("no agent: %v", err)
	}
	g := sh.guest(t, sh.env.ID, "one")
	if _, err := git.Clone(memory.NewStorage(), memfs.New(), &git.CloneOptions{URL: g.srv.URL + "/box/owner/repo.git"}); err == nil {
		t.Fatal("cloned without an agent")
	}
	if !strings.Contains(g.seen(), "SSH agent not available") {
		t.Fatalf("the sandbox was not told: %s", g.seen())
	}
	st := sh.svc.AgentStatus(sh.ctx)
	if st.Available || !strings.Contains(st.Message, "SSH agent not available") || st.Fix == "" {
		t.Fatalf("status without an agent = %+v", st)
	}
}

func TestAgentStatusAndKeyLimit(t *testing.T) {
	keyring := agent.NewKeyring()
	sh := newAgentHarness(t, keyring)
	var last string
	for range maxAgentKeys {
		last = addAgentKey(t, keyring, "extra")
	}
	st := CheckAgent(context.Background())
	if !st.Available || len(st.Keys) != maxAgentKeys+3 || st.Keys[0].Comment != "laptop" || !strings.HasPrefix(st.Keys[0].Fingerprint, "SHA256:") {
		t.Fatalf("status = %+v", st)
	}
	// Only the first keys are offered, so a host that accepts a later one refuses Studio
	// instead of disconnecting after too many attempts.
	sh.ssh.mu.Lock()
	sh.ssh.authorized, sh.ssh.attempts = nil, 0
	sh.ssh.mu.Unlock()
	sh.ssh.authorize(last)
	if _, err := sh.svc.TestForge(sh.ctx, sh.forge, "owner/repo"); err == nil || !strings.Contains(err.Error(), "refused the keys") {
		t.Fatalf("test with the key past the limit: %v", err)
	}
	sh.ssh.mu.Lock()
	attempts := sh.ssh.attempts
	sh.ssh.mu.Unlock()
	if attempts != maxAgentKeys {
		t.Fatalf("offered %d keys", attempts)
	}
}
