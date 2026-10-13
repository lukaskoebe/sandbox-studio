package login

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/lukaskoebe/sandbox-studio/internal/agentchan"
	"github.com/lukaskoebe/sandbox-studio/internal/agentproto"
	"github.com/lukaskoebe/sandbox-studio/internal/store"
	"github.com/lukaskoebe/sandbox-studio/internal/teammates"
)

// Errors the API answers for.
var (
	ErrNotSubscription = errors.New("the provider is not a subscription")
	ErrInvalidToken    = errors.New("that is not a Claude Code token: it starts with sk-ant-oat")
	ErrNoLogin         = errors.New("no sign-in is running for this provider")
)

// The CLI's tmux session in a login VM, and the terminal size it runs in.
const (
	sessionName = "login"
	termCols    = 160
	termRows    = 48
)

// The states of a sign-in as the dialog shows them.
const (
	StateIdle     = "idle"     // no sign-in is running
	StateStarting = "starting" // the login VM is booting
	StateWaiting  = "waiting"  // the CLI waits for the user to finish in the browser
	StateSaving   = "saving"   // the login was captured and is being stored
	StateDone     = "done"     // the last sign-in stored its login
	StateFailed   = "failed"   // the sign-in ended without a login; Message says why
)

// View is a provider's sign-in as the dialog shows it.
type View struct {
	State   string `json:"state" enum:"idle,starting,waiting,saving,done,failed"`
	URL     string `json:"url,omitempty" doc:"The page the user opens to finish the sign-in"`
	Code    string `json:"code,omitempty" doc:"The one-time code of ChatGPT's device sign-in"`
	Message string `json:"message,omitempty"`
	// Paste is set when the user can paste a Claude Code token instead, because Studio couldn't
	// read the token from the terminal.
	Paste bool `json:"paste"`
}

// Machines runs the system VMs of logins and utility calls (sandboxes.Manager).
type Machines interface {
	EnsureSystem(ctx context.Context, envID, kind, name string) (store.Sandbox, error)
	RemoveSystem(ctx context.Context, envID, kind, name string) error
	StopSystem(ctx context.Context, envID, kind, name string) error
}

// Guest is the part of the agent channel the logins use (agentchan.Hub).
type Guest interface {
	Run(ctx context.Context, id string, req agentproto.RunRequest) (agentproto.RunResult, error)
	OpenPTY(ctx context.Context, id, name string, cols, rows uint16) (*agentchan.PTY, error)
	WriteHomeFiles(ctx context.Context, id string, files []agentproto.HomeFile) error
	Connected(id string) (agentproto.Hello, bool)
}

// Vault is the part of the secrets vault the logins use (secrets.Vault).
type Vault interface {
	Prepare(envID, name, value string, hosts []string, note string) (store.Secret, error)
	Create(ctx context.Context, sec store.Secret, value string) (store.Secret, error)
	Update(ctx context.Context, envID, id string, value *string, hosts []string, note string) (store.Secret, error)
	Value(ctx context.Context, envID, id string) (string, error)
	Changed(envID string)
}

// Service runs the sign-ins and keeps the logins fresh.
type Service struct {
	Store *store.Store
	Vault Vault
	VMs   Machines
	Guest Guest
	// Notify is told that an environment's providers or secrets changed, so open tabs reload.
	Notify func(envID string)
	Log    *slog.Logger
	// Poll is how often a running sign-in is checked and Timeout ends one that doesn't finish;
	// zero means the defaults. Now is the clock; nil uses time.Now.
	Poll    time.Duration
	Timeout time.Duration
	Now     func() time.Time
	// HTTP is the client of token refreshes; nil dials public addresses only (gateway.DialPublic).
	HTTP *http.Client
	// TokenURL is where refreshes go; tests replace it.
	TokenURL string

	mu       sync.Mutex
	bg       context.Context // lives as long as Run; sign-ins outlive their requests
	attempts map[string]*attempt
	idle     map[string]utilityUse // utility VMs by provider ID, for the idle stop
}

// New returns a service with the default clock and polling.
func New(st *store.Store, vault Vault, vms Machines, guest Guest, log *slog.Logger) *Service {
	if log == nil {
		log = slog.New(slog.DiscardHandler)
	}
	return &Service{Store: st, Vault: vault, VMs: vms, Guest: guest, Log: log, attempts: map[string]*attempt{}, idle: map[string]utilityUse{}}
}

const (
	defaultPoll    = 2 * time.Second
	defaultTimeout = 15 * time.Minute
)

func (s *Service) poll() time.Duration {
	if s.Poll > 0 {
		return s.Poll
	}
	return defaultPoll
}

func (s *Service) timeout() time.Duration {
	if s.Timeout > 0 {
		return s.Timeout
	}
	return defaultTimeout
}

func (s *Service) now() time.Time {
	if s.Now != nil {
		return s.Now()
	}
	return time.Now()
}

func (s *Service) notify(envID string) {
	if s.Notify != nil {
		s.Notify(envID)
	}
}

// background is the context sign-ins run in: Run's, or a background one before Run.
func (s *Service) background() context.Context {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.bg != nil {
		return s.bg
	}
	return context.Background()
}

// attempt is one sign-in of a provider. Its fields change while it runs, under mu.
type attempt struct {
	provider store.Provider
	cancel   context.CancelFunc
	done     chan struct{}

	mu        sync.Mutex
	state     string
	sandboxID string
	url, code string
	message   string
	paste     bool
}

func (a *attempt) view() View {
	a.mu.Lock()
	defer a.mu.Unlock()
	return View{State: a.state, URL: a.url, Code: a.code, Message: a.message, Paste: a.paste}
}

func (a *attempt) running() bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.state == StateStarting || a.state == StateWaiting || a.state == StateSaving
}

func (a *attempt) set(fn func(*attempt)) {
	a.mu.Lock()
	defer a.mu.Unlock()
	fn(a)
}

// loginVM is the name of a provider's login VM, and loginKind its sandbox kind.
func loginVM(p store.Provider) string { return "login-" + p.ID }

func loginKind(p store.Provider) string {
	if p.Kind == teammates.KindChatGPTSubscription {
		return store.SandboxKindCodexLogin
	}
	return store.SandboxKindClaudeLogin
}

// cliCommand is the command the sign-in runs in the login VM. verify (S8): the subcommands and
// flags of the pinned CLIs; the device flow prints a page and a code the user enters.
func cliCommand(p store.Provider) string {
	if p.Kind == teammates.KindChatGPTSubscription {
		return "codex login --device-auth"
	}
	return "claude setup-token"
}

// subscription returns the provider of envID with the ID, which must be a subscription.
func (s *Service) subscription(ctx context.Context, envID, id string) (store.Provider, error) {
	p, err := s.Store.Provider(ctx, envID, id)
	if err != nil {
		return store.Provider{}, err
	}
	if k, _ := teammates.LookupKind(p.Kind); !k.Login {
		return store.Provider{}, ErrNotSubscription
	}
	return p, nil
}

// Start begins the sign-in of a subscription provider and returns at once: the login VM boots,
// the vendor's CLI runs in it, and the sign-in completes in the background. A sign-in that is
// running already is returned as it is.
func (s *Service) Start(ctx context.Context, envID, providerID string) (View, error) {
	p, err := s.subscription(ctx, envID, providerID)
	if err != nil {
		return View{}, err
	}
	// background takes the lock itself, so it is read before the lock is held here.
	bg := s.background()
	s.mu.Lock()
	if a := s.attempts[providerID]; a != nil && a.running() {
		s.mu.Unlock()
		return a.view(), nil
	}
	runCtx, cancel := context.WithTimeout(bg, s.timeout())
	a := &attempt{provider: p, state: StateStarting, cancel: cancel, done: make(chan struct{})}
	s.attempts[providerID] = a
	s.mu.Unlock()
	go s.run(runCtx, a)
	return a.view(), nil
}

// Status returns the provider's sign-in as the dialog shows it: idle when none is running.
func (s *Service) Status(providerID string) View {
	s.mu.Lock()
	a := s.attempts[providerID]
	s.mu.Unlock()
	if a == nil {
		return View{State: StateIdle}
	}
	return a.view()
}

// Cancel ends a running sign-in and removes its login VM. Without one it does nothing.
func (s *Service) Cancel(ctx context.Context, envID, providerID string) error {
	s.mu.Lock()
	a := s.attempts[providerID]
	s.mu.Unlock()
	if a == nil || !a.running() {
		return nil
	}
	a.cancel()
	select {
	case <-a.done:
	case <-ctx.Done():
		return ctx.Err()
	}
	return nil
}

// Terminal returns a terminal attached to the CLI of a running sign-in, in its login VM.
func (s *Service) Terminal(ctx context.Context, envID, providerID string, cols, rows uint16) (*agentchan.PTY, error) {
	s.mu.Lock()
	a := s.attempts[providerID]
	s.mu.Unlock()
	var id string
	if a != nil {
		a.mu.Lock()
		if a.state == StateWaiting {
			id = a.sandboxID
		}
		a.mu.Unlock()
	}
	if id == "" {
		return nil, ErrNoLogin
	}
	return s.Guest.OpenPTY(ctx, id, sessionName, cols, rows)
}

// PasteToken stores a Claude Code token the user copied from the terminal, and ends a sign-in
// that is running for the provider.
func (s *Service) PasteToken(ctx context.Context, envID, providerID, token string) error {
	p, err := s.subscription(ctx, envID, providerID)
	if err != nil {
		return err
	}
	if p.Kind != teammates.KindClaudeSubscription {
		return ErrNotSubscription
	}
	if !ValidClaudeToken(token) {
		return ErrInvalidToken
	}
	if err := s.Cancel(ctx, envID, providerID); err != nil {
		return err
	}
	if err := s.storeKey(ctx, p, strings.TrimSpace(token), []string{teammates.HostOf(p.Kind)}); err != nil {
		return err
	}
	s.notify(envID)
	return nil
}

// run is one sign-in: it starts the login VM and the CLI in it, then waits for the result. It
// always ends the attempt through finish, which removes the login VM. ctx is the attempt's: Cancel
// and the timeout end it.
func (s *Service) run(ctx context.Context, a *attempt) {
	defer close(a.done)
	defer a.cancel()
	p := a.provider
	kind, name := loginKind(p), loginVM(p)

	// A VM left by an earlier attempt goes first: every sign-in starts from a fresh VM.
	if err := s.VMs.RemoveSystem(ctx, p.EnvironmentID, kind, name); err != nil {
		s.Log.Warn("removing an old login VM", "provider", p.ID, "err", err)
	}
	sb, err := s.VMs.EnsureSystem(ctx, p.EnvironmentID, kind, name)
	if err != nil {
		s.abort(ctx, a, "The login VM didn't start: "+err.Error()+". Try again.")
		return
	}
	a.set(func(a *attempt) { a.sandboxID = sb.ID })
	cmd := []string{"tmux", "new-session", "-d", "-s", sessionName, "-x", fmt.Sprint(termCols), "-y", fmt.Sprint(termRows), cliCommand(p)}
	res, err := s.Guest.Run(ctx, sb.ID, agentproto.RunRequest{Args: cmd, TimeoutSec: 60})
	if err == nil && res.ExitCode == 0 {
		// The pane outlives the CLI, so the output it printed last, the token, stays readable. verify (S8):
		// remain-on-exit as a window option, of the pinned tmux.
		res, err = s.Guest.Run(ctx, sb.ID, agentproto.RunRequest{
			Args: []string{"tmux", "set-option", "-w", "-t", sessionName, "remain-on-exit", "on"}, TimeoutSec: 30,
		})
	}
	if err != nil || res.ExitCode != 0 {
		s.Log.Warn("starting the login", "provider", p.ID, "err", err, "stderr", res.Stderr)
		s.abort(ctx, a, "The login couldn't start in its VM. Try again.")
		return
	}
	a.set(func(a *attempt) { a.state = StateWaiting })
	s.watch(ctx, a, sb.ID)
}

// abort ends an attempt that failed before its CLI ran. A cancelled attempt ends idle.
func (s *Service) abort(ctx context.Context, a *attempt, msg string) {
	if ctx.Err() == context.Canceled {
		s.finish(a, StateIdle, "", false)
		return
	}
	s.finish(a, StateFailed, msg, false)
}

// watch checks the login's terminal and the CLI's files until the login is stored, the CLI ends
// without one, or ctx ends.
func (s *Service) watch(ctx context.Context, a *attempt, sandboxID string) {
	p := a.provider
	t := time.NewTicker(s.poll())
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			s.abort(ctx, a, "The sign-in timed out. Sign in again.")
			return
		case <-t.C:
		}
		if p.Kind == teammates.KindChatGPTSubscription {
			if l, ok := s.readCodexAuth(ctx, sandboxID); ok {
				s.saveCodex(ctx, a, l)
				return
			}
		}
		text, alive := s.capture(ctx, sandboxID)
		url, code := "", ""
		if p.Kind == teammates.KindChatGPTSubscription {
			url, code = DeviceLogin(text)
		} else {
			url = FirstURL(text)
		}
		a.set(func(a *attempt) {
			if url != "" {
				a.url = url
			}
			if code != "" {
				a.code = code
			}
		})
		if p.Kind == teammates.KindClaudeSubscription {
			if tok, ok := ClaudeToken(text); ok {
				s.saveClaude(ctx, a, tok)
				return
			}
		}
		if !alive {
			// The CLI ended without a login Studio could read.
			if p.Kind == teammates.KindChatGPTSubscription {
				s.finish(a, StateFailed, "The ChatGPT sign-in ended without a login. Sign in again.", false)
			} else {
				s.finish(a, StateFailed, "Studio couldn't read the token from the terminal. Paste it instead, or sign in again.", true)
			}
			return
		}
	}
}

// capture returns the login terminal's text, and whether the CLI still runs in it. The pane is
// kept after the CLI exits (remain-on-exit), so the text it printed last is still there; a
// session that is gone has ended.
func (s *Service) capture(ctx context.Context, sandboxID string) (string, bool) {
	text, err := s.Guest.Run(ctx, sandboxID, agentproto.RunRequest{
		Args: []string{"tmux", "capture-pane", "-p", "-J", "-S", "-", "-t", sessionName}, TimeoutSec: 30,
	})
	if err != nil {
		if ctx.Err() == nil {
			s.Log.Warn("reading the login terminal", "err", err)
		}
		return "", true // a failed read is not the CLI ending; try again
	}
	if text.ExitCode != 0 {
		return "", false
	}
	// verify (S8): pane_dead is 1 once the command in the pane has exited.
	dead, err := s.Guest.Run(ctx, sandboxID, agentproto.RunRequest{
		Args: []string{"tmux", "display-message", "-p", "-t", sessionName, "#{pane_dead}"}, TimeoutSec: 30,
	})
	if err != nil || dead.ExitCode != 0 {
		return text.Stdout, true
	}
	return text.Stdout, strings.TrimSpace(dead.Stdout) != "1"
}

// readCodexAuth reads the auth file a finished ChatGPT sign-in wrote in the login VM.
func (s *Service) readCodexAuth(ctx context.Context, sandboxID string) (codexLogin, bool) {
	res, err := s.Guest.Run(ctx, sandboxID, agentproto.RunRequest{Args: []string{"cat", ".codex/auth.json"}, TimeoutSec: 30})
	if err != nil || res.ExitCode != 0 || res.Stdout == "" {
		return codexLogin{}, false
	}
	l, err := parseCodexAuth([]byte(res.Stdout))
	return l, err == nil
}

// saveClaude stores a captured Claude Code token.
func (s *Service) saveClaude(ctx context.Context, a *attempt, token string) {
	a.set(func(a *attempt) { a.state = StateSaving })
	p := a.provider
	if err := s.storeKey(ctx, p, token, []string{teammates.HostOf(p.Kind)}); err != nil {
		s.finish(a, StateFailed, "The token was read but couldn't be stored: "+err.Error(), true)
		return
	}
	s.finish(a, StateDone, "", false)
}

// saveCodex stores a finished ChatGPT sign-in.
func (s *Service) saveCodex(ctx context.Context, a *attempt, l codexLogin) {
	a.set(func(a *attempt) { a.state = StateSaving })
	if err := s.storeCodex(ctx, a.provider, l); err != nil {
		s.finish(a, StateFailed, "The sign-in was read but couldn't be stored: "+err.Error(), false)
		return
	}
	s.finish(a, StateDone, "", false)
}

// finish ends an attempt: the login VM is removed, the state is set and open tabs are told. A
// VM that can't be removed is left to the next sign-in, which removes it first.
func (s *Service) finish(a *attempt, state, msg string, paste bool) {
	p := a.provider
	ctx := s.background()
	if err := s.VMs.RemoveSystem(ctx, p.EnvironmentID, loginKind(p), loginVM(p)); err != nil {
		s.Log.Warn("removing the login VM", "provider", p.ID, "err", err)
	}
	a.set(func(a *attempt) {
		a.state, a.message, a.paste, a.sandboxID = state, msg, paste, ""
		a.url, a.code = "", ""
	})
	s.notify(p.EnvironmentID)
}

// storeKey stores value as the provider's key: the vault secret the guest's placeholder
// stands for, bound to hosts. The first key is attached to the provider; later ones replace it.
func (s *Service) storeKey(ctx context.Context, p store.Provider, value string, hosts []string) error {
	if p.SecretID != "" {
		sec, err := s.Store.SecretByID(ctx, p.EnvironmentID, p.SecretID)
		if err != nil {
			return err
		}
		if _, err := s.Vault.Update(ctx, p.EnvironmentID, p.SecretID, &value, sec.Hosts, sec.Note); err != nil {
			return err
		}
		s.Vault.Changed(p.EnvironmentID)
		return nil
	}
	sec, err := s.Vault.Prepare(p.EnvironmentID, teammates.SecretName(p.Name, p.ID), value, hosts, "Login of provider "+p.Name)
	if err != nil {
		return err
	}
	if _, err := s.Store.AttachProviderSecret(ctx, p.EnvironmentID, p.ID, sec); err != nil {
		return err
	}
	s.Vault.Changed(p.EnvironmentID)
	return nil
}

// storeCodex stores a ChatGPT login: the access token's signature as the provider's key, and the
// whole login as a studio-only secret, which is what refreshes and new guest files come from.
func (s *Service) storeCodex(ctx context.Context, p store.Provider, l codexLogin) error {
	if err := checkCodexLogin(l); err != nil {
		return err
	}
	sig, err := accessSignature(l)
	if err != nil {
		return err
	}
	if err := s.storeKey(ctx, p, sig, []string{teammates.HostOf(p.Kind)}); err != nil {
		return err
	}
	b, err := json.Marshal(l)
	if err != nil {
		return err
	}
	value := string(b)
	sec, ok, err := s.loginSecret(ctx, p)
	if err != nil {
		return err
	}
	if ok {
		_, err = s.Vault.Update(ctx, p.EnvironmentID, sec.ID, &value, nil, sec.Note)
	} else {
		_, err = s.Vault.Create(ctx, store.Secret{EnvironmentID: p.EnvironmentID, Name: teammates.LoginSecretName(p.Name, p.ID),
			StudioOnly: true, Note: "Login of provider " + p.Name}, value)
	}
	if err != nil {
		return err
	}
	s.Vault.Changed(p.EnvironmentID)
	s.notify(p.EnvironmentID)
	return nil
}

// loginSecret returns the studio-only secret that holds a ChatGPT login.
func (s *Service) loginSecret(ctx context.Context, p store.Provider) (store.Secret, bool, error) {
	list, err := s.Store.Secrets(ctx, p.EnvironmentID)
	if err != nil {
		return store.Secret{}, false, err
	}
	want := teammates.LoginSecretName(p.Name, p.ID)
	for _, sec := range list {
		if sec.StudioOnly && sec.Name == want {
			return sec, true, nil
		}
	}
	return store.Secret{}, false, nil
}

// login reads the ChatGPT login a provider keeps; ok is false without one.
func (s *Service) login(ctx context.Context, p store.Provider) (codexLogin, store.Secret, bool, error) {
	sec, ok, err := s.loginSecret(ctx, p)
	if err != nil || !ok {
		return codexLogin{}, sec, false, err
	}
	raw, err := s.Vault.Value(ctx, p.EnvironmentID, sec.ID)
	if err != nil {
		return codexLogin{}, sec, false, err
	}
	var l codexLogin
	if err := json.Unmarshal([]byte(raw), &l); err != nil {
		return codexLogin{}, sec, false, fmt.Errorf("the stored login: %w", err)
	}
	return l, sec, true, checkCodexLogin(l)
}

// Expired reports whether a provider's login can no longer be used: a ChatGPT login whose access
// token lapsed and could not be refreshed. Claude Code tokens are long-lived and are not checked.
func (s *Service) Expired(ctx context.Context, p store.Provider) (bool, error) {
	if p.Kind != teammates.KindChatGPTSubscription || p.SecretID == "" {
		return false, nil
	}
	l, _, ok, err := s.login(ctx, p)
	if err != nil || !ok {
		return false, err
	}
	exp, err := jwtExpiry(l.AccessToken)
	if err != nil {
		return true, nil
	}
	return !exp.After(s.now()), nil
}

// GuestFiles are the files a session of a subscription provider's teammate needs: a ChatGPT
// login's auth file with placeholders only. A Claude Code session needs none; its token comes in
// the environment as a placeholder.
func (s *Service) GuestFiles(ctx context.Context, p store.Provider) ([]agentproto.HomeFile, error) {
	if p.Kind != teammates.KindChatGPTSubscription {
		return nil, nil
	}
	l, sec, ok, err := s.login(ctx, p)
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, ErrNoLogin
	}
	if p.Placeholder == "" {
		return nil, errors.New("the provider's key has no placeholder")
	}
	data, err := codexGuestAuth(l, p.Placeholder, sec.Placeholder)
	if err != nil {
		return nil, err
	}
	return []agentproto.HomeFile{{Path: ".codex/auth.json", Content: string(data), Mode: 0o600}}, nil
}
