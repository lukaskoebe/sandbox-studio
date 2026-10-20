package gitreview

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"runtime"
	"time"

	"golang.org/x/crypto/ssh"
	"golang.org/x/crypto/ssh/agent"

	"github.com/lukaskoebe/sandbox-studio/internal/store"
)

// The host's SSH agent signs for ssh forges with auth agent. Only Studio's own SSH client
// uses it, for fetches and approved pushes; it is never forwarded into a sandbox, which
// still goes through push review. Studio reaches it through SSH_AUTH_SOCK, or on Windows
// the OpenSSH agent's named pipe.

// maxAgentKeys bounds the agent keys Studio offers a host, in the agent's order. OpenSSH
// servers drop the connection after six attempts by default (MaxAuthTries).
const maxAgentKeys = 6

// DefaultSlowSign is how long an agent signature may take before Studio assumes the agent
// asked the user to confirm it: a hardware key touch or a password manager prompt.
const DefaultSlowSign = 2 * time.Second

// ErrNoAgent is the host's SSH agent being unreachable.
var ErrNoAgent = errors.New("SSH agent not available")

// DialAgent connects to the host's SSH agent.
func DialAgent(ctx context.Context) (net.Conn, error) {
	addr, err := agentAddress()
	if err != nil {
		return nil, fmt.Errorf("%w: %s", ErrNoAgent, err)
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	c, err := dialAgentAddress(ctx, addr)
	if err != nil {
		return nil, fmt.Errorf("%w: could not connect to %s: %s", ErrNoAgent, addr, err)
	}
	return c, nil
}

// AgentFix says how to make the SSH agent reachable for Studio on this OS.
func AgentFix() string {
	switch runtime.GOOS {
	case "windows":
		return "Start the OpenSSH Authentication Agent service (or your password manager's SSH agent) and add your key with ssh-add."
	case "darwin":
		return "Studio reads SSH_AUTH_SOCK when it starts: start it from a shell where `ssh-add -l` lists your key. " +
			"For autostart with another agent than macOS's own, run `launchctl setenv SSH_AUTH_SOCK <socket>` and restart Studio."
	}
	return "Studio reads SSH_AUTH_SOCK when it starts: start it from a shell where `ssh-add -l` lists your key. " +
		"For autostart, set SSH_AUTH_SOCK in ~/.config/environment.d/ or run `systemctl --user import-environment SSH_AUTH_SOCK`, then restart Studio."
}

// AgentKey is a key the SSH agent holds.
type AgentKey struct {
	Type        string `json:"type"`
	Fingerprint string `json:"fingerprint" doc:"SHA256 fingerprint"`
	Comment     string `json:"comment,omitempty"`
}

// AgentStatus is whether Studio reaches the host's SSH agent.
type AgentStatus struct {
	Available bool       `json:"available"`
	Keys      []AgentKey `json:"keys" doc:"The keys the agent holds; Studio offers hosts the first six"`
	Message   string     `json:"message,omitempty" doc:"Why the agent can't be used"`
	Fix       string     `json:"fix,omitempty" doc:"How to make it available"`
}

func (s *Service) log() *slog.Logger {
	if s.Log == nil {
		return slog.New(slog.DiscardHandler)
	}
	return s.Log
}

func (s *Service) dialAgent(ctx context.Context) (net.Conn, error) {
	if s.Agent != nil {
		return s.Agent(ctx)
	}
	return DialAgent(ctx)
}

// AgentStatus checks the SSH agent Studio signs with and lists its keys.
func (s *Service) AgentStatus(ctx context.Context) AgentStatus {
	return agentStatus(ctx, s.dialAgent)
}

// CheckAgent checks the host's SSH agent and lists its keys.
func CheckAgent(ctx context.Context) AgentStatus {
	return agentStatus(ctx, DialAgent)
}

func agentStatus(ctx context.Context, dial func(context.Context) (net.Conn, error)) AgentStatus {
	st := AgentStatus{Keys: []AgentKey{}}
	conn, err := dial(ctx)
	if err != nil {
		st.Message, st.Fix = err.Error(), AgentFix()
		return st
	}
	defer conn.Close()
	conn.SetDeadline(time.Now().Add(5 * time.Second))
	keys, err := agent.NewClient(conn).List()
	if err != nil {
		st.Message, st.Fix = fmt.Sprintf("%s: the agent did not list its keys: %s", ErrNoAgent, err), AgentFix()
		return st
	}
	for _, k := range keys {
		st.Keys = append(st.Keys, AgentKey{Type: k.Type(), Fingerprint: ssh.FingerprintSHA256(k), Comment: k.Comment})
	}
	if len(keys) == 0 {
		st.Message, st.Fix = "The SSH agent holds no keys.", "Add your key with ssh-add."
		return st
	}
	st.Available = true
	return st
}

// agentAuth authenticates with the keys of the host's SSH agent. A signature that takes
// longer than the slow-sign threshold is recorded on f, so Settings → Git forges can suggest a
// Studio key instead. done closes the agent connection once the handshake is over.
func (s *Service) agentAuth(ctx context.Context, f store.Forge) (auth ssh.AuthMethod, done func(), err error) {
	conn, err := s.dialAgent(ctx)
	if err != nil {
		return nil, nil, err
	}
	// A prompt nobody answers must not hold the handshake: the agent gets as long as the
	// connection to the host.
	conn.SetDeadline(time.Now().Add(sshTimeout))
	stop := context.AfterFunc(ctx, func() { conn.Close() })
	done = func() { stop(); conn.Close() }
	signers, err := agent.NewClient(conn).Signers()
	if err != nil {
		done()
		return nil, nil, fmt.Errorf("%w: the agent did not list its keys: %s", ErrNoAgent, err)
	}
	if len(signers) == 0 {
		done()
		return nil, nil, fmt.Errorf("%w: it holds no keys; add yours with ssh-add", ErrNoAgent)
	}
	if len(signers) > maxAgentKeys {
		signers = signers[:maxAgentKeys]
	}
	slow := s.slowSign
	if slow == 0 {
		slow = DefaultSlowSign
	}
	onSlow := func(took time.Duration) {
		s.log().Info("git remote: the SSH agent was slow to sign; it may have asked for confirmation", "forge", f.Name, "took", took.Round(time.Millisecond))
		rctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
		defer cancel()
		if err := s.Store.SetForgeAgentSlow(rctx, f.EnvironmentID, f.ID); err != nil {
			s.log().Error("git remote: record slow agent", "forge", f.Name, "err", err)
		}
	}
	for i, sg := range signers {
		if as, ok := sg.(ssh.AlgorithmSigner); ok {
			signers[i] = &timedSigner{AlgorithmSigner: as, slow: slow, onSlow: onSlow}
		}
	}
	return ssh.PublicKeys(signers...), done, nil
}

// timedSigner measures how long the agent takes to sign.
type timedSigner struct {
	ssh.AlgorithmSigner
	slow   time.Duration
	onSlow func(time.Duration)
}

func (t *timedSigner) Sign(rand io.Reader, data []byte) (*ssh.Signature, error) {
	return t.SignWithAlgorithm(rand, data, "")
}

func (t *timedSigner) SignWithAlgorithm(rand io.Reader, data []byte, algorithm string) (*ssh.Signature, error) {
	start := time.Now()
	sig, err := t.AlgorithmSigner.SignWithAlgorithm(rand, data, algorithm)
	if took := time.Since(start); took >= t.slow {
		t.onSlow(took)
	}
	return sig, err
}
