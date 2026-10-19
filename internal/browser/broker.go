// Package browser is the browser broker (docs/browser.md). Each teammate has one browser VM, a
// sandbox of kind browser running Chromium under agent-browser. Agents drive it with the
// browser tools of `studio-agent mcp`; the calls arrive on their own sandbox's channel, so
// the broker knows the teammate from the channel and never from what the call says. Every
// action goes through the policy before it runs, snapshots come back with sensitive values
// blanked, credentials are filled by Studio itself after the user approves, and the user
// can watch and take over through the live view.
package browser

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"strings"
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
	ErrNoTeammate = errors.New("this sandbox has no teammate, so it has no browser")
	ErrNoCalls    = errors.New("browser calls come from agent sandboxes only")
	ErrPaused     = errors.New("the user took over the browser and still has control of it, so your browser calls are paused. " +
		"Tell the user what you want to do in the browser and that they still have control; they hand it back with Hand back on the Browser page. " +
		"browser_request_control asks them with a notification. Call again once they have handed it back")
	ErrNotLive    = errors.New("the browser is not running")
	ErrNoTakeover = errors.New("take over the browser before navigating")
	ErrBadURL     = errors.New("url must be an absolute http or https URL with a host, no userinfo, and at most 4000 characters")
	ErrNoSession  = errors.New("there is no such browser session")
	ErrHidden     = errors.New("the page shows a credential, so its picture is hidden until it changes")
	ErrBadMove    = errors.New("to must be back, forward or reload")
)

// Runner reaches a browser VM's guest agent (agentchan.Hub).
type Runner interface {
	Browser(ctx context.Context, sandboxID string, cmd agentproto.BrowserCommand) (agentproto.BrowserResult, error)
	DialTCP(ctx context.Context, sandboxID string, port int) (net.Conn, error)
	BrowserDownloads(ctx context.Context, sandboxID string, req agentproto.BrowserDownloadsRequest) (agentproto.BrowserDownloadList, error)
	BrowserDownload(ctx context.Context, sandboxID string, req agentproto.BrowserDownloadsRequest,
		fn func(d agentproto.BrowserDownload, body io.Reader) error) error
	Upload(ctx context.Context, sandboxID string, req agentproto.UploadRequest, body io.Reader) (agentproto.UploadReply, error)
	// Ports lists an agent sandbox's listening ports, and Forwards has a browser VM listen
	// on those (forward.go).
	Ports(ctx context.Context, sandboxID string) ([]agentproto.Port, error)
	Forwards(ctx context.Context, sandboxID string, ports []int) error
}

// VMs creates, starts and stops browser VMs (sandboxes.Manager).
type VMs interface {
	EnsureBrowser(ctx context.Context, envID, teammateID, image string) (store.Sandbox, error)
	StopBrowser(ctx context.Context, envID, teammateID string) error
	RemoveBrowser(ctx context.Context, envID, teammateID string) error
	BrowserStatus(ctx context.Context, envID, teammateID string) (store.Sandbox, runtime.Status, error)
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
	defaultEphemeral   = 10 * time.Minute // an unused ephemeral session ends after this
	defaultKeep        = 500              // actions kept per teammate
	defaultShotBudget  = 200 << 20        // bytes of screenshots kept in all
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
	// Logged, when set, is told that a teammate's action log has a new entry.
	Logged func(envID, teammateID string)
	Now    func() time.Time

	// Bounds; zero means the default.
	Hold          time.Duration
	PauseWait     time.Duration
	IdleStop      time.Duration
	EphemeralIdle time.Duration
	Keep          int
	ShotBudget    int64

	mu       sync.Mutex
	states   map[string]*state // by teammate ID
	outcomes map[string]*credOutcome
}

// state is what the broker keeps about one teammate's browser. Fields below run are
// guarded by Service.mu. The embedded page is the persistent profile.
type state struct {
	env, teammate string
	run           chan struct{} // one command at a time, in any of the VM's sessions

	vm       string // the browser sandbox's ID, once known
	session  string // groups the action log of one run of the VM
	driver   store.Sandbox
	fwdFrom  string // the agent sandbox whose ports the VM forwards to, and those ports
	fwdPorts []int
	lastUsed time.Time
	grants   []grant
	sites    []string // registrable domains the user approved for this browser session
	hosts    []string // host patterns the user allowed for this browser session
	page
	eph map[string]*page // ephemeral sessions by ID
}

// page is one agent-browser session in the browser VM: the persistent profile, or an
// ephemeral session with a throwaway profile on the VM's tmpfs. Its fields are guarded
// by Service.mu.
type page struct {
	id       string        // "" for the profile, else the ephemeral session's ID
	slot     int           // the guest's ephemeral slot, 0 for the profile
	owner    store.Sandbox // the agent sandbox that opened an ephemeral session
	chat     string        // the chat whose turn opened it, if it could be told; see chatOf
	opened   time.Time
	used     time.Time
	takeover bool
	resumed  chan struct{} // closed when the user hands back
	turn     *userTurn     // what the user did while taking over
	// handback tells the next agent call what the user's turn changed; see userTurn.note.
	// handbackPages is the same with the pages the browser was on, for a teammate whose
	// user shares them.
	handback, handbackPages string
	control                 string // the agent's pending request for the session back, if any
	refs                    map[string]refInfo
	url                     string
	loading                 string          // the site an allowed navigation is loading, until url is known
	secrets                 []string        // credential values filled in this run, for Scrub
	credRefs                map[string]bool // refs Studio filled with a credential
	// visible is set when a credential went into a field that shows it: agent screenshots,
	// history screenshots and frames stop until the page changes.
	visible bool
	// frame is the latest screencast frame relayed, as the guest sent it, and when.
	frame   []byte
	frameAt time.Time
}

type refInfo struct {
	Role, Name string
	Sensitive  bool
}

// grant is a one-time approval of an action in one session, used by a retry after the
// call that asked returned before the user answered.
type grant struct {
	session string
	req     Request
	until   time.Time
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

func (s *Service) state(envID, teammateID string) *state {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.stateLocked(envID, teammateID)
}

func (s *Service) stateLocked(envID, teammateID string) *state {
	if s.states == nil {
		s.states = map[string]*state{}
	}
	st := s.states[teammateID]
	if st == nil || st.env != envID {
		st = &state{env: envID, teammate: teammateID, run: make(chan struct{}, 1), lastUsed: s.now()}
		s.states[teammateID] = st
	}
	return st
}

// Driver is gateway.Gateway.Driver: whose rules decide the browser VM's connections.
// While the user drives any of its sessions, or before any agent did, the browser VM's own
// row decides, which means the teammate's rules; otherwise the agent sandbox that drove it
// last.
func (s *Service) Driver(ctx context.Context, sb store.Sandbox) store.Sandbox {
	if sb.Kind != store.SandboxKindBrowser {
		return sb
	}
	s.mu.Lock()
	st := s.states[sb.TeammateID]
	var driver store.Sandbox
	if st != nil && !st.anyTakeover() {
		driver = st.driver
	}
	s.mu.Unlock()
	if driver.ID == "" {
		return sb
	}
	// The driver may have been deleted since.
	cur, err := s.Store.LookupSandbox(ctx, driver.ID)
	if err != nil || cur.EnvironmentID != sb.EnvironmentID || cur.TeammateID != sb.TeammateID {
		return sb
	}
	return cur
}

// pageFor finds a session: "" is the profile, else an ephemeral session or nil.
func (st *state) pageFor(id string) *page {
	if id == "" {
		return &st.page
	}
	return st.eph[id]
}

// anyTakeover reports whether the user drives any of the browser's sessions.
func (st *state) anyTakeover() bool {
	if st.takeover {
		return true
	}
	for _, pg := range st.eph {
		if pg.takeover {
			return true
		}
	}
	return false
}

// acquire waits for the teammate's browser to be free and for pg not to be driven by the
// user. A nil pg is a session that does not exist yet.
func (s *Service) acquire(ctx context.Context, st *state, pg *page) (func(), error) {
	deadline := time.NewTimer(or(s.PauseWait, defaultPauseWait))
	defer deadline.Stop()
	if pg == nil {
		pg = &page{}
	}
	for {
		s.mu.Lock()
		paused, resumed := pg.takeover, pg.resumed
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
		paused = pg.takeover
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

// lostReply is a command whose reply did not reach Studio, for example because the channel to
// the browser VM broke. The command may have run, so the tools check the page before they answer.
type lostReply struct{ error }

// run sends one command to the teammate's browser VM with the teammate's launch options.
// Every command goes through here, so a session's options agree from one command to the
// next and agent-browser restarts its browser only when the user changed them.
func (s *Service) run(ctx context.Context, st *state, vm string, cmd agentproto.BrowserCommand) (agentproto.BrowserResult, error) {
	if tm, err := s.Store.Teammate(ctx, st.env, st.teammate); err == nil {
		cmd.Stealth = tm.BrowserStealth
	}
	return s.Runner.Browser(ctx, vm, cmd)
}

// exec runs one agent-browser command in a session of the teammate's browser VM. Errors
// are scrubbed.
func (s *Service) exec(ctx context.Context, st *state, pg *page, cmd agentproto.BrowserCommand) (json.RawMessage, []byte, error) {
	s.mu.Lock()
	vm, secrets := st.vm, pg.secrets
	s.mu.Unlock()
	if vm == "" {
		return nil, nil, ErrNotLive
	}
	if cmd.TimeoutMS == 0 {
		cmd.TimeoutMS = int(commandTimeout / time.Millisecond)
	}
	cmd.Ephemeral = pg.slot
	res, err := s.run(ctx, st, vm, cmd)
	if err != nil {
		if pg.slot > 0 && strings.Contains(err.Error(), agentproto.ErrBrowserEphemeralGone) {
			// The VM restarted under the session: its profile is gone, and so is the session.
			s.mu.Lock()
			s.dropLocked(st, pg)
			s.mu.Unlock()
			return nil, nil, refuse("ephemeral session %s ended with its browser; open a new one with browser_open", pg.id)
		}
		return nil, nil, lostReply{errors.New(Scrub(err.Error(), secrets))}
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
func (s *Service) batch(ctx context.Context, st *state, pg *page, cmds [][]string) ([]batchItem, error) {
	in, err := json.Marshal(cmds)
	if err != nil {
		return nil, err
	}
	out, _, err := s.exec(ctx, st, pg, agentproto.BrowserCommand{Args: []string{"batch"}, Stdin: string(in)})
	if err != nil {
		return nil, err
	}
	var items []batchItem
	if err := json.Unmarshal(out, &items); err != nil || len(items) != len(cmds) {
		return nil, errors.New("the browser's batch reply was not understood")
	}
	return items, nil
}

// boot makes sure the teammate's browser VM is running and its state knows it.
func (s *Service) boot(ctx context.Context, st *state) error {
	vm, err := s.VMs.EnsureBrowser(ctx, st.env, st.teammate, s.Image)
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

// reset forgets a browser run: the pages, their refs and the credentials filled into them.
// Ephemeral sessions end with the VM run. The caller holds s.mu.
func (st *state) reset() {
	for _, pg := range st.eph {
		if pg.takeover {
			pg.takeover = false
			close(pg.resumed)
		}
	}
	st.session, st.grants, st.eph, st.sites, st.hosts = "", nil, nil, nil, nil
	st.fwdFrom, st.fwdPorts = "", nil
	st.url, st.loading, st.refs, st.credRefs, st.secrets, st.visible = "", "", nil, nil, nil, false
	st.frame, st.frameAt = nil, time.Time{}
}

// navigated forgets what belonged to the previous page.
func (pg *page) navigated(url string) {
	pg.url, pg.loading, pg.refs, pg.credRefs, pg.visible = url, "", nil, nil, false
	pg.turn.saw(url)
}

// A URL observation can be a same-document change with credential fields still visible.
// Invalidate element refs, but keep the screenshot restriction until a confirmed open.
func (pg *page) observedURL(url string) {
	if url != pg.url {
		pg.url, pg.refs, pg.credRefs = url, nil, nil
	}
	pg.loading = ""
	pg.turn.saw(url)
}

// takeGrant consumes a one-time grant that covers r in session.
func (s *Service) takeGrant(st *state, session string, r Request) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := s.now()
	for i, g := range st.grants {
		if now.After(g.until) {
			continue
		}
		if g.session == session && g.req.Action == r.Action && g.req.Origin == r.Origin && g.req.Role == r.Role && g.req.Label == r.Label {
			st.grants = append(st.grants[:i], st.grants[i+1:]...)
			return true
		}
	}
	return false
}

func (s *Service) addGrant(envID, teammateID, session string, r Request) {
	s.mu.Lock()
	defer s.mu.Unlock()
	st := s.stateLocked(envID, teammateID)
	now := s.now()
	live := st.grants[:0]
	for _, g := range st.grants {
		if now.Before(g.until) {
			live = append(live, g)
		}
	}
	st.grants = append(live, grant{session: session, req: r, until: now.Add(grantTTL)})
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
			st := s.stateLocked(sb.EnvironmentID, sb.TeammateID)
			st.vm = sb.ID
		}
		s.mu.Unlock()
	}
	s.sweepScreenshots(ctx)
	idle, ephIdle := or(s.IdleStop, defaultIdleStop), or(s.EphemeralIdle, defaultEphemeral)
	t := time.NewTicker(min(idle/2, ephIdle/2, time.Minute))
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			s.stopIdle(ctx, idle)
			s.endIdleEphemeral(ctx, ephIdle)
		}
	}
}

// stopIdle stops the browser VMs not used for idle; never one the user drives.
func (s *Service) stopIdle(ctx context.Context, idle time.Duration) {
	s.mu.Lock()
	var due []*state
	for _, st := range s.states {
		if st.vm != "" && !st.anyTakeover() && s.now().Sub(st.lastUsed) > idle {
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
		still := !st.anyTakeover() && s.now().Sub(st.lastUsed) > idle
		s.mu.Unlock()
		if still {
			_, status, err := s.VMs.BrowserStatus(ctx, st.env, st.teammate)
			if err == nil && status != runtime.StatusStopped && status != runtime.StatusAbsent {
				if err := s.VMs.StopBrowser(ctx, st.env, st.teammate); err != nil {
					s.log().Warn("stopping an idle browser failed", "teammate", st.teammate, "err", err)
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
