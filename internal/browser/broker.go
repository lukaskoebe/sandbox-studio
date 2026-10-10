// Package browser is the browser broker (PLAN §6.8). Each persona has one browser VM, a
// sandbox of kind browser running Chromium under agent-browser. Agents drive it with the
// browser tools of `studio-agent mcp`; the calls arrive on their own sandbox's channel, so
// the broker knows the persona from the channel and never from what the call says. Every
// action goes through the policy before it runs, snapshots come back with sensitive values
// blanked, credentials are filled by Studio itself after the user approves, and the user
// can watch and take over through the live view.
package browser

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"sync"
	"time"

	"github.com/lukaskoebe/sandbox-studio/internal/agentproto"
	"github.com/lukaskoebe/sandbox-studio/internal/runtime"
	"github.com/lukaskoebe/sandbox-studio/internal/store"
)

// Approval kinds the broker raises.
const (
	KindAction     = "browser.action"
	KindCredential = "browser.credential"
)

// Errors an agent may see.
var (
	ErrNoPersona = errors.New("this sandbox has no persona, so it has no browser")
	ErrNoCalls   = errors.New("browser calls come from agent sandboxes only")
	ErrPaused    = errors.New("the user is driving the browser, so agent browser calls are paused; try again once the user hands it back")
	ErrNotLive   = errors.New("the browser is not running")
)

// Runner reaches a browser VM's guest agent (agentchan.Hub).
type Runner interface {
	Browser(ctx context.Context, sandboxID string, cmd agentproto.BrowserCommand) (agentproto.BrowserResult, error)
	DialTCP(ctx context.Context, sandboxID string, port int) (net.Conn, error)
}

// VMs creates, starts and stops browser VMs (sandboxes.Manager).
type VMs interface {
	EnsureBrowser(ctx context.Context, envID, personaID, image string) (store.Sandbox, error)
	StopBrowser(ctx context.Context, envID, personaID string) error
	RemoveBrowser(ctx context.Context, envID, personaID string) error
	BrowserStatus(ctx context.Context, envID, personaID string) (store.Sandbox, runtime.Status, error)
}

// Keys unseals a vault secret (secrets.Vault).
type Keys interface {
	Value(ctx context.Context, envID, secretID string) (string, error)
}

// Defaults of the Service's bounds.
const (
	defaultHold        = 30 * time.Second // an approval is awaited within the call this long
	defaultPauseWait   = 20 * time.Second // an agent call waits this long for the user to hand back
	defaultIdleStop    = 10 * time.Minute
	defaultKeep        = 500       // actions kept per persona
	defaultShotBudget  = 200 << 20 // bytes of screenshots kept in all
	commandTimeout     = 25 * time.Second
	grantTTL           = 5 * time.Minute
	maxProbes          = 50
	maxAgentText       = 200 << 10
	approvalPollPeriod = 200 * time.Millisecond
)

// Service is the browser broker.
type Service struct {
	Store  *store.Store
	VMs    VMs
	Runner Runner
	Keys   Keys
	Image  string // the browser image
	Dir    string // where action screenshots are kept
	Log    *slog.Logger
	// Notify, when set, is told that an environment has a new approval.
	Notify func(envID string)
	Now    func() time.Time

	// Bounds; zero means the default.
	Hold       time.Duration
	PauseWait  time.Duration
	IdleStop   time.Duration
	Keep       int
	ShotBudget int64

	mu       sync.Mutex
	states   map[string]*state // by persona ID
	outcomes map[string]*credOutcome
}

// state is what the broker keeps about one persona's browser. Fields below run are
// guarded by Service.mu.
type state struct {
	env, persona string
	run          chan struct{} // one command at a time

	vm       string // the browser sandbox's ID, once known
	session  string // groups the action log of one run of the VM
	driver   store.Sandbox
	takeover bool
	resumed  chan struct{} // closed when the user hands back
	lastUsed time.Time
	refs     map[string]refInfo
	url      string
	secrets  []string        // credential values filled in this run, for Scrub
	credRefs map[string]bool // refs Studio filled with a credential
	// visible is set when a credential went into a field that shows it: agent screenshots
	// and history screenshots stop until the page changes.
	visible bool
	grants  []grant
}

type refInfo struct {
	Role, Name string
	Sensitive  bool
}

// grant is a one-time approval of an action, used by a retry after the call that asked
// returned before the user answered.
type grant struct {
	req   Request
	until time.Time
}

func (s *Service) now() time.Time {
	if s.Now != nil {
		return s.Now()
	}
	return time.Now()
}

func (s *Service) log() *slog.Logger {
	if s.Log != nil {
		return s.Log
	}
	return slog.Default()
}

func or[T comparable](v, def T) T {
	var zero T
	if v == zero {
		return def
	}
	return v
}

func (s *Service) state(envID, personaID string) *state {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.stateLocked(envID, personaID)
}

func (s *Service) stateLocked(envID, personaID string) *state {
	if s.states == nil {
		s.states = map[string]*state{}
	}
	st := s.states[personaID]
	if st == nil || st.env != envID {
		st = &state{env: envID, persona: personaID, run: make(chan struct{}, 1), lastUsed: s.now()}
		s.states[personaID] = st
	}
	return st
}

// Driver is gateway.Gateway.Driver: whose rules decide the browser VM's connections.
// While the user drives, or before any agent did, the browser VM's own row decides, which
// means the persona's rules; otherwise the agent sandbox that drove it last.
func (s *Service) Driver(ctx context.Context, sb store.Sandbox) store.Sandbox {
	if sb.Kind != store.SandboxKindBrowser {
		return sb
	}
	s.mu.Lock()
	st := s.states[sb.PersonaID]
	var driver store.Sandbox
	if st != nil && !st.takeover {
		driver = st.driver
	}
	s.mu.Unlock()
	if driver.ID == "" {
		return sb
	}
	// The driver may have been deleted since.
	cur, err := s.Store.LookupSandbox(ctx, driver.ID)
	if err != nil || cur.EnvironmentID != sb.EnvironmentID || cur.PersonaID != sb.PersonaID {
		return sb
	}
	return cur
}

// acquire waits for the persona's browser to be free and not driven by the user.
func (s *Service) acquire(ctx context.Context, st *state) (func(), error) {
	deadline := time.NewTimer(or(s.PauseWait, defaultPauseWait))
	defer deadline.Stop()
	for {
		s.mu.Lock()
		paused, resumed := st.takeover, st.resumed
		s.mu.Unlock()
		if paused {
			select {
			case <-resumed:
				continue
			case <-deadline.C:
				return nil, ErrPaused
			case <-ctx.Done():
				return nil, ctx.Err()
			}
		}
		select {
		case st.run <- struct{}{}:
		case <-ctx.Done():
			return nil, ctx.Err()
		}
		s.mu.Lock()
		paused = st.takeover
		s.mu.Unlock()
		if !paused {
			return func() { <-st.run }, nil
		}
		<-st.run
	}
}

// envelope is agent-browser's --json response.
type envelope struct {
	Success bool            `json:"success"`
	Data    json.RawMessage `json:"data"`
	Error   string          `json:"error"`
}

// batchItem is one entry of `batch --json`'s response. Its command field echoes the
// arguments, credentials included, so it is never decoded.
type batchItem struct {
	Success bool            `json:"success"`
	Result  json.RawMessage `json:"result"`
	Error   string          `json:"error"`
}

// exec runs one agent-browser command in the persona's browser VM. Errors are scrubbed.
func (s *Service) exec(ctx context.Context, st *state, cmd agentproto.BrowserCommand) (json.RawMessage, []byte, error) {
	s.mu.Lock()
	vm, secrets := st.vm, st.secrets
	s.mu.Unlock()
	if vm == "" {
		return nil, nil, ErrNotLive
	}
	if cmd.TimeoutMS == 0 {
		cmd.TimeoutMS = int(commandTimeout / time.Millisecond)
	}
	res, err := s.Runner.Browser(ctx, vm, cmd)
	if err != nil {
		return nil, nil, errors.New(Scrub(err.Error(), secrets))
	}
	if len(cmd.Args) > 0 && cmd.Args[0] == "batch" {
		return res.Output, res.Screenshot, nil
	}
	var env envelope
	if err := json.Unmarshal(res.Output, &env); err != nil {
		return nil, nil, errors.New("the browser's reply was not understood")
	}
	if !env.Success {
		msg := env.Error
		if msg == "" {
			msg = "the browser command failed"
		}
		return nil, res.Screenshot, errors.New(Scrub(msg, secrets))
	}
	return env.Data, res.Screenshot, nil
}

// batch runs commands in one agent-browser call and returns their results in order.
func (s *Service) batch(ctx context.Context, st *state, cmds [][]string) ([]batchItem, error) {
	in, err := json.Marshal(cmds)
	if err != nil {
		return nil, err
	}
	out, _, err := s.exec(ctx, st, agentproto.BrowserCommand{Args: []string{"batch"}, Stdin: string(in)})
	if err != nil {
		return nil, err
	}
	var items []batchItem
	if err := json.Unmarshal(out, &items); err != nil || len(items) != len(cmds) {
		return nil, errors.New("the browser's batch reply was not understood")
	}
	return items, nil
}

// boot makes sure the persona's browser VM is running and its state knows it.
func (s *Service) boot(ctx context.Context, st *state) error {
	vm, err := s.VMs.EnsureBrowser(ctx, st.env, st.persona, s.Image)
	if err != nil {
		return fmt.Errorf("starting the browser: %w", err)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	st.vm = vm.ID
	st.lastUsed = s.now()
	if st.session == "" {
		st.session = store.NewID()
	}
	return nil
}

// reset forgets a browser run: the page, its refs and the credentials filled into it.
func (st *state) reset() {
	st.session, st.url, st.refs, st.credRefs, st.secrets, st.visible, st.grants = "", "", nil, nil, nil, false, nil
}

// navigated forgets what belonged to the previous page.
func (st *state) navigated(url string) {
	st.url, st.refs, st.credRefs, st.visible = url, nil, nil, false
}

// takeGrant consumes a one-time grant that covers r.
func (s *Service) takeGrant(st *state, r Request) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := s.now()
	for i, g := range st.grants {
		if now.After(g.until) {
			continue
		}
		if g.req.Action == r.Action && g.req.Origin == r.Origin && g.req.Role == r.Role && g.req.Label == r.Label {
			st.grants = append(st.grants[:i], st.grants[i+1:]...)
			return true
		}
	}
	return false
}

func (s *Service) addGrant(envID, personaID string, r Request) {
	s.mu.Lock()
	defer s.mu.Unlock()
	st := s.stateLocked(envID, personaID)
	now := s.now()
	live := st.grants[:0]
	for _, g := range st.grants {
		if now.Before(g.until) {
			live = append(live, g)
		}
	}
	st.grants = append(live, grant{req: r, until: now.Add(grantTTL)})
}

// awaitApproval polls an approval until it is decided or hold passes; it returns its
// status then.
func (s *Service) awaitApproval(ctx context.Context, envID, id string) (string, error) {
	hold := time.NewTimer(or(s.Hold, defaultHold))
	defer hold.Stop()
	tick := time.NewTicker(approvalPollPeriod)
	defer tick.Stop()
	for {
		a, err := s.Store.Approval(ctx, envID, id)
		if err != nil {
			return "", err
		}
		if a.Status != store.StatusPending {
			return a.Status, nil
		}
		select {
		case <-tick.C:
		case <-hold.C:
			return store.StatusPending, nil
		case <-ctx.Done():
			return "", ctx.Err()
		}
	}
}

func (s *Service) notify(envID string) {
	if s.Notify != nil {
		s.Notify(envID)
	}
}

// Run stops browser VMs that went idle until ctx ends. It first adopts the browser VMs
// that outlived a previous run of Studio.
func (s *Service) Run(ctx context.Context) {
	if rows, err := s.Store.BrowserSandboxes(ctx); err == nil {
		s.mu.Lock()
		for _, sb := range rows {
			st := s.stateLocked(sb.EnvironmentID, sb.PersonaID)
			st.vm = sb.ID
		}
		s.mu.Unlock()
	}
	s.sweepScreenshots(ctx)
	idle := or(s.IdleStop, defaultIdleStop)
	t := time.NewTicker(min(idle/2, time.Minute))
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			s.stopIdle(ctx, idle)
		}
	}
}

// stopIdle stops the browser VMs not used for idle; never one the user drives.
func (s *Service) stopIdle(ctx context.Context, idle time.Duration) {
	s.mu.Lock()
	var due []*state
	for _, st := range s.states {
		if st.vm != "" && !st.takeover && s.now().Sub(st.lastUsed) > idle {
			due = append(due, st)
		}
	}
	s.mu.Unlock()
	for _, st := range due {
		select {
		case st.run <- struct{}{}:
		default:
			continue // a command is running
		}
		s.mu.Lock()
		still := !st.takeover && s.now().Sub(st.lastUsed) > idle
		s.mu.Unlock()
		if still {
			_, status, err := s.VMs.BrowserStatus(ctx, st.env, st.persona)
			if err == nil && status != runtime.StatusStopped && status != runtime.StatusAbsent {
				if err := s.VMs.StopBrowser(ctx, st.env, st.persona); err != nil {
					s.log().Warn("stopping an idle browser failed", "persona", st.persona, "err", err)
				}
			}
			s.mu.Lock()
			st.reset()
			st.vm = ""
			s.mu.Unlock()
		}
		<-st.run
	}
}
