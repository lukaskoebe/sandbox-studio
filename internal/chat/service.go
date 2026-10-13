// Package chat runs chats (docs/design/chat-mode.md): the owner teammate's harness driven
// over ACP in its sandbox, with its updates folded into items that the UI renders. The
// agent process starts on a chat's first prompt and stays until the chat is handed to
// the terminal, deleted, or the sandbox stops; the chat outlives it in the store and
// resumes the agent's session on the next prompt.
package chat

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/lukaskoebe/sandbox-studio/internal/acp"
	"github.com/lukaskoebe/sandbox-studio/internal/agentchan"
	"github.com/lukaskoebe/sandbox-studio/internal/agentproto"
	"github.com/lukaskoebe/sandbox-studio/internal/events"
	"github.com/lukaskoebe/sandbox-studio/internal/harness"
	"github.com/lukaskoebe/sandbox-studio/internal/store"
	"github.com/lukaskoebe/sandbox-studio/internal/version"
)

// Workdir is where chat sessions run.
const Workdir = "/workspace"

// StopInterrupted is the stop reason of a turn the agent never finished: it exited, the
// sandbox stopped, or Studio restarted.
const StopInterrupted = "interrupted"

// Timings; tests shorten them.
var (
	flushEvery   = 250 * time.Millisecond // how often a streaming item is written
	cancelGrace  = 10 * time.Second       // how long a cancelled turn may take to end
	startTimeout = 90 * time.Second       // starting the agent and its session
)

// Conflict is a request the chat's state refuses; the API answers 409 with its text.
type Conflict string

func (c Conflict) Error() string      { return string(c) }
func (Conflict) Is(target error) bool { return target == store.ErrConflict }

// ErrBusy refuses a prompt while the agent works on the last one.
var ErrBusy = Conflict("the agent is still working on the last request; stop it first")

// ErrInvalid marks a prompt the agent can't take.
var ErrInvalid = errors.New("invalid prompt")

// ErrInvalidAnswer marks a question response that does not match its form.
var ErrInvalidAnswer = errors.New("invalid chat answer")

// ErrInvalidDecision marks a permission decision that cannot be mapped to the agent's options.
var ErrInvalidDecision = errors.New("invalid chat permission decision")

// Guest is the part of the guest channel chats use: the sandbox manager's Hub.
type Guest interface {
	OpenACP(ctx context.Context, id string, req agentproto.StartACP) (io.ReadWriteCloser, error)
	WriteHomeFiles(ctx context.Context, id string, files []agentproto.HomeFile) error
	Sessions(ctx context.Context, id string) ([]agentproto.Session, error)
	StartSession(ctx context.Context, id string, req agentproto.StartSession) error
	KillSession(ctx context.Context, id, name string) error
	Snapshot(ctx context.Context, id string, req agentproto.SnapshotRequest) (agentproto.SnapshotReply, error)
	Files(ctx context.Context, id string, req agentproto.FilesRequest) (agentproto.FilesReply, error)
	Upload(ctx context.Context, id string, req agentproto.UploadRequest, body io.Reader) (agentproto.UploadReply, error)
}

// Launch is how to start a chat's agent, planned like an agent session: the harness,
// its config files, its ACP command and environment and, for the terminal, the TUI
// session's environment (placeholders only).
type Launch struct {
	Harness harness.Harness
	Files   []agentproto.HomeFile
	Command []string
	Env     map[string]string
	TUIEnv  map[string]string
}

// Service runs the chats of every environment.
type Service struct {
	Store *store.Store
	Guest Guest
	Bus   *events.Bus
	Log   *slog.Logger
	// PromptNote, when set, returns what a sandbox's next prompt starts with besides the undo
	// note: what an integration must tell the agent, such as a rejected push. sent is called
	// once the prompt went out, so a note that didn't go out is given again.
	PromptNote func(ctx context.Context, envID, sandboxID string) (text string, sent func(), err error)
	// OnIdle, when set, is told that a chat became idle again: a turn ended, or the chat came
	// back from the terminal. Memory extracts the new turns once the chat stays idle.
	OnIdle func(envID, chatID string)

	mu    sync.Mutex
	live  map[string]*live
	watch map[string]map[chan struct{}]struct{}
}

func (s *Service) log() *slog.Logger {
	if s.Log == nil {
		return slog.New(slog.DiscardHandler)
	}
	return s.Log
}

// changed wakes the chat's streams and, for changes the chat list shows, tells other
// tabs.
func (s *Service) changed(envID, chatID string, list bool) {
	s.mu.Lock()
	for ch := range s.watch[chatID] {
		select {
		case ch <- struct{}{}:
		default:
		}
	}
	s.mu.Unlock()
	if list && s.Bus != nil {
		s.Bus.Publish(events.Event{Topic: events.TopicChats, EnvironmentID: envID, ID: chatID})
	}
}

// Watch returns a channel that receives after each change of the chat, and a function
// that stops it. Receivers read the changes from the store.
func (s *Service) Watch(chatID string) (<-chan struct{}, func()) {
	ch := make(chan struct{}, 1)
	s.mu.Lock()
	if s.watch == nil {
		s.watch = map[string]map[chan struct{}]struct{}{}
	}
	if s.watch[chatID] == nil {
		s.watch[chatID] = map[chan struct{}]struct{}{}
	}
	s.watch[chatID][ch] = struct{}{}
	s.mu.Unlock()
	return ch, func() {
		s.mu.Lock()
		delete(s.watch[chatID], ch)
		if len(s.watch[chatID]) == 0 {
			delete(s.watch, chatID)
		}
		s.mu.Unlock()
	}
}

// Create adds an idle chat with the sandbox's owner teammate.
func (s *Service) Create(ctx context.Context, sb store.Sandbox, harnessName string) (store.Chat, error) {
	return s.create(ctx, sb, harnessName, false)
}

// CreateHeartbeat adds an idle chat that a heartbeat opens, which the chat list marks as one.
func (s *Service) CreateHeartbeat(ctx context.Context, sb store.Sandbox, harnessName string) (store.Chat, error) {
	return s.create(ctx, sb, harnessName, true)
}

func (s *Service) create(ctx context.Context, sb store.Sandbox, harnessName string, heartbeat bool) (store.Chat, error) {
	c, err := s.Store.CreateChat(ctx, store.Chat{EnvironmentID: sb.EnvironmentID, SandboxID: sb.ID, TeammateID: sb.TeammateID, Harness: harnessName, Heartbeat: heartbeat})
	if err == nil {
		s.changed(c.EnvironmentID, c.ID, true)
	}
	return c, err
}

// Log is a chat with all its turns and items.
type Log struct {
	Chat  store.Chat `json:"chat"`
	Turns []Turn     `json:"turns"`
	Items []Item     `json:"items"`
}

// Turn is a chat turn with its usage and changed files decoded.
type Turn struct {
	store.ChatTurn
	Usage    *TurnUsage   `json:"usage,omitempty"`
	Changes  *TurnChanges `json:"changes,omitempty" doc:"The files the turn changed, once it ended"`
	Undoable bool         `json:"undoable" doc:"The workspace can be put back as it was before this turn"`
}

// TurnView decodes a stored turn.
func TurnView(t store.ChatTurn) Turn {
	v := Turn{ChatTurn: t, Undoable: t.Snapshot != "" && t.UndoneAt == nil}
	if len(t.Usage) > 0 {
		var u TurnUsage
		if json.Unmarshal(t.Usage, &u) == nil {
			v.Usage = &u
		}
	}
	if len(t.Changes) > 0 {
		var c TurnChanges
		if json.Unmarshal(t.Changes, &c) == nil {
			c.Files = orEmpty(c.Files)
			v.Changes = &c
		}
	}
	return v
}

// Get returns a chat with its turns and items.
func (s *Service) Get(ctx context.Context, envID, chatID string) (Log, error) {
	c, turns, items, err := s.Store.ChatChanges(ctx, envID, chatID, 0)
	if err != nil {
		return Log{}, err
	}
	out := Log{Chat: c, Turns: make([]Turn, 0, len(turns)), Items: make([]Item, 0, len(items))}
	for _, t := range turns {
		out.Turns = append(out.Turns, TurnView(t))
	}
	for _, it := range items {
		out.Items = append(out.Items, ItemView(it))
	}
	slices.SortFunc(out.Turns, func(a, b Turn) int { return a.Seq - b.Seq })
	slices.SortFunc(out.Items, func(a, b Item) int { return a.Seq - b.Seq })
	return out, nil
}

// Delete ends the chat's agent, if it runs, and removes the chat.
func (s *Service) Delete(ctx context.Context, envID, chatID string) error {
	c, err := s.Store.Chat(ctx, envID, chatID)
	if err != nil {
		return err
	}
	if l := s.get(chatID); l != nil {
		l.shutdown(ctx)
	}
	if c.State == store.ChatTerminal {
		if err := s.killTUI(ctx, c); err != nil {
			s.log().Debug("chat: end the terminal of a deleted chat", "chat", chatID, "err", err)
		}
	}
	if err := s.Store.DeleteChat(ctx, envID, chatID); err != nil {
		return err
	}
	s.changed(envID, chatID, true)
	if err := s.collect(ctx, envID, c.SandboxID); err != nil {
		s.log().Debug("chat: collect the snapshots of a deleted chat", "chat", chatID, "err", err)
	}
	return nil
}

// Close ends every chat's agent, when Studio stops. Running turns end interrupted.
func (s *Service) Close() {
	s.mu.Lock()
	all := make([]*live, 0, len(s.live))
	for _, l := range s.live {
		all = append(all, l)
	}
	s.mu.Unlock()
	for _, l := range all {
		l.conn.Close()
	}
}

// Recover runs at startup, before any chat starts: turns that were running when Studio
// stopped end interrupted, and their chats go back to idle.
func (s *Service) Recover(ctx context.Context) error {
	turns, err := s.Store.OpenChatTurns(ctx)
	if err != nil {
		return err
	}
	for _, t := range turns {
		if err := s.settleOpenItems(ctx, t.ChatID, t.ID); err != nil {
			return err
		}
		if _, err := s.Store.EndChatTurn(ctx, t.ChatID, t.ID, StopInterrupted, nil); err != nil && !errors.Is(err, store.ErrNotFound) {
			return err
		}
		if _, err := s.Store.AddChatItem(ctx, markerItem(t, MarkerInterrupted, "Studio restarted while the agent was working on this.")); err != nil {
			return err
		}
	}
	running, err := s.Store.ChatsInState(ctx, store.ChatRunning)
	if err != nil {
		return err
	}
	for _, c := range running {
		if _, err := s.Store.SetChatState(ctx, c.EnvironmentID, c.ID, store.ChatIdle, store.ChatRunning); err != nil && !errors.Is(err, store.ErrConflict) {
			return err
		}
		s.changed(c.EnvironmentID, c.ID, true)
	}
	return s.recoverApprovals(ctx)
}

func markerItem(t store.ChatTurn, kind, text string) store.ChatItem {
	return store.ChatItem{ChatID: t.ChatID, TurnID: t.ID, Kind: KindMarker, Body: encodeBody(&MarkerBody{Kind: kind, Text: text})}
}

// --- prompts --------------------------------------------------------------------------

// File is an attachment of a prompt.
type File struct {
	Name     string
	MimeType string
	Data     []byte
}

// Prompt is what the user sends.
type Prompt struct {
	Text        string
	Attachments []File
}

// Send starts a turn with p, starting the chat's agent first if it isn't running. The
// turn runs on; its items arrive through Watch.
func (s *Service) Send(ctx context.Context, envID, chatID string, launch Launch, p Prompt) (store.ChatTurn, error) {
	c, err := s.Store.Chat(ctx, envID, chatID)
	if err != nil {
		return store.ChatTurn{}, err
	}
	if strings.TrimSpace(p.Text) == "" && len(p.Attachments) == 0 {
		return store.ChatTurn{}, fmt.Errorf("%w: the prompt is empty", ErrInvalid)
	}
	if c, err = s.lock(ctx, c, store.ChatRunning); err != nil {
		return store.ChatTurn{}, err
	}
	l, err := s.ensure(ctx, c, launch)
	if err == nil {
		var blocks []acp.ContentBlock
		var atts []Attachment
		if blocks, atts, err = promptBlocks(l.caps, p); err == nil {
			var t store.ChatTurn
			if t, err = l.start(ctx, c, p.Text, atts, blocks); err == nil {
				return t, nil
			}
		}
	}
	s.unlock(c, store.ChatRunning)
	return store.ChatTurn{}, err
}

// lock moves an idle chat to state, or explains why it can't.
func (s *Service) lock(ctx context.Context, c store.Chat, state string) (store.Chat, error) {
	next, err := s.Store.SetChatState(ctx, c.EnvironmentID, c.ID, state, store.ChatIdle)
	if errors.Is(err, store.ErrConflict) {
		if cur, err := s.Store.Chat(ctx, c.EnvironmentID, c.ID); err == nil {
			return c, stateConflict(cur.State)
		}
	}
	if err == nil {
		s.changed(c.EnvironmentID, c.ID, true)
	}
	return next, err
}

// unlock puts a chat locked into state back to idle, unless something else moved it.
func (s *Service) unlock(c store.Chat, state string) {
	if _, err := s.Store.SetChatState(context.Background(), c.EnvironmentID, c.ID, store.ChatIdle, state); err == nil {
		s.changed(c.EnvironmentID, c.ID, true)
		if s.OnIdle != nil {
			s.OnIdle(c.EnvironmentID, c.ID)
		}
	}
}

func stateConflict(state string) error {
	switch state {
	case store.ChatRunning:
		return ErrBusy
	case store.ChatTerminal:
		return Conflict("the chat is open in the terminal; continue in chat first")
	case store.ChatOrphaned:
		return Conflict("the agent's session of this chat is gone; start a new chat")
	}
	return Conflict("the chat is " + state)
}

// --- the agent ------------------------------------------------------------------------

// live is a chat's running agent.
type live struct {
	s       *Service
	envID   string
	chatID  string
	conn    *acp.Conn
	caps    acp.AgentCapabilities
	session string

	mu     sync.Mutex
	turn   *run    // the running turn
	replay *folder // collects session/load's replay
	asks   map[string]*ask
}

// run is a running turn.
type run struct {
	turn       store.ChatTurn
	f          *folder
	cancelled  bool
	ended      bool
	promptSent chan struct{} // closed after session/prompt is written
	done       chan struct{}
}

func (s *Service) get(chatID string) *live {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.live[chatID]
}

// ensure returns the chat's running agent, starting it and its session if needed.
func (s *Service) ensure(ctx context.Context, c store.Chat, launch Launch) (*live, error) {
	if l := s.get(c.ID); l != nil {
		select {
		case <-l.conn.Done():
		default:
			return l, nil
		}
	}
	l, _, err := s.startAgent(ctx, c, launch, false)
	return l, err
}

// startAgent starts the chat's agent and creates or reattaches its session. With reload,
// an existing session is reattached with session/load where the agent can, and the
// replay comes back folded (nil otherwise).
func (s *Service) startAgent(ctx context.Context, c store.Chat, launch Launch, reload bool) (*live, *folder, error) {
	if launch.Harness == nil || launch.Harness.Name() != c.Harness {
		return nil, nil, Conflict(fmt.Sprintf("the teammate no longer runs %s; start a new chat", c.Harness))
	}
	ctx, cancel := context.WithTimeout(ctx, startTimeout)
	defer cancel()
	h := launch.Harness
	if err := s.Guest.WriteHomeFiles(ctx, c.SandboxID, launch.Files); err != nil {
		return nil, nil, fmt.Errorf("writing the harness config: %w", err)
	}
	rwc, err := s.Guest.OpenACP(ctx, c.SandboxID, agentproto.StartACP{Chat: c.ID, Harness: h.Name(), Command: launch.Command, Env: launch.Env})
	var he agentchan.HarnessError
	if errors.As(err, &he) {
		return nil, nil, Conflict(he.Error())
	}
	if err != nil {
		return nil, nil, fmt.Errorf("starting %s's chat agent: %w", h.Name(), err)
	}
	l := &live{s: s, envID: c.EnvironmentID, chatID: c.ID, asks: map[string]*ask{}}
	l.conn = acp.NewConn(rwc, l)
	replay, err := l.open(ctx, c, h.ACPMCPServers(), reload)
	if err != nil {
		l.conn.Close()
		if errors.Is(err, acp.ErrAuthRequired) {
			return nil, nil, Conflict(fmt.Sprintf("%s asks for a login, which chats don't do: check the teammate's provider", h.Name()))
		}
		return nil, nil, err
	}
	s.mu.Lock()
	if s.live == nil {
		s.live = map[string]*live{}
	}
	s.live[c.ID] = l
	s.mu.Unlock()
	go func() {
		<-l.conn.Done()
		s.forgetLive(l)
	}()
	return l, replay, nil
}

// open initializes the agent and creates or reattaches the chat's session: with
// session/resume, or session/load where the agent can't resume or reload asks for the
// replay.
func (l *live) open(ctx context.Context, c store.Chat, servers []acp.MCPServer, reload bool) (*folder, error) {
	init, err := l.conn.Initialize(ctx, acp.Implementation{Name: "sandbox-studio", Version: version.Version})
	if err != nil {
		return nil, fmt.Errorf("initialize: %w", err)
	}
	l.caps = init.AgentCapabilities
	if c.ACPSessionID == "" {
		id, err := l.conn.NewSession(ctx, Workdir, servers)
		if err != nil {
			return nil, fmt.Errorf("new session: %w", err)
		}
		l.session = id
		return nil, l.s.Store.SetChatSession(ctx, c.ID, id)
	}
	l.session = c.ACPSessionID
	var replay *folder
	switch {
	case l.caps.LoadSession && (reload || !l.caps.CanResume()):
		replay, err = l.load(ctx, servers)
	case l.caps.CanResume():
		err = l.conn.ResumeSession(ctx, l.session, Workdir, servers)
	default:
		err = &acp.Error{Message: "the agent can resume no sessions"}
	}
	var ae *acp.Error
	if errors.As(err, &ae) {
		l.s.orphan(c)
		return nil, Conflict("the agent's session of this chat is gone (" + ae.Message + "); start a new chat")
	}
	if !reload {
		replay = nil
	}
	return replay, err
}

// load reattaches with session/load and returns its replay folded into entries.
func (l *live) load(ctx context.Context, servers []acp.MCPServer) (*folder, error) {
	f := newFolder(store.NewID)
	f.users = true
	l.mu.Lock()
	l.replay = f
	l.mu.Unlock()
	err := l.conn.LoadSession(ctx, l.session, Workdir, servers)
	l.mu.Lock()
	l.replay = nil
	l.mu.Unlock()
	f.close()
	return f, err
}

// orphan marks a chat whose session is gone; it stays readable.
func (s *Service) orphan(c store.Chat) {
	if _, err := s.Store.SetChatState(context.Background(), c.EnvironmentID, c.ID, store.ChatOrphaned); err == nil {
		s.changed(c.EnvironmentID, c.ID, true)
	}
}

// shutdown closes the session, where the agent supports it, and stops the agent.
func (l *live) shutdown(ctx context.Context) {
	if l.caps.CanClose() {
		ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
		if err := l.conn.CloseSession(ctx, l.session); err != nil {
			l.s.log().Debug("chat: session/close", "chat", l.chatID, "err", err)
		}
		cancel()
	}
	l.conn.Close()
	<-l.conn.Done()
	l.s.forgetLive(l)
}

// forgetLive drops l from the running agents, unless another took its place.
func (s *Service) forgetLive(l *live) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.live[l.chatID] == l {
		delete(s.live, l.chatID)
	}
}

// SessionUpdate folds the agent's updates into the running turn, or the replay.
func (l *live) SessionUpdate(n acp.SessionNotification) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if n.SessionID != l.session {
		return
	}
	if l.replay != nil {
		l.replay.apply(n.Update)
		return
	}
	if l.turn != nil {
		l.write(l.turn, l.turn.f.apply(n.Update))
	}
}

// write stores changed entries: new ones at once, so items keep their order, ended ones
// at once, and others on the next flush. Called with l.mu held.
func (l *live) write(r *run, changes []change) {
	wrote := false
	for _, c := range changes {
		e := c.e
		if e.saved && (!c.ended || !e.dirty) {
			continue
		}
		if err := l.s.save(r.turn, e); err != nil {
			l.s.log().Warn("chat: store item", "chat", l.chatID, "err", err)
			continue
		}
		wrote = true
	}
	if wrote {
		l.s.changed(l.envID, l.chatID, false)
	}
}

// save inserts or updates an entry.
func (s *Service) save(t store.ChatTurn, e *entry) error {
	it := store.ChatItem{ID: e.id, ChatID: t.ChatID, TurnID: t.ID, Kind: e.kind, ToolCallID: e.toolCallID, Status: e.status, ApprovalID: e.approvalID, Body: encodeBody(e.body)}
	var err error
	if e.saved {
		_, err = s.Store.UpdateChatItem(context.Background(), it)
	} else {
		_, err = s.Store.AddChatItem(context.Background(), it)
	}
	if err == nil {
		e.saved, e.dirty = true, false
	}
	return err
}

// flush writes every entry changed since its last write.
func (l *live) flush(r *run) {
	wrote := false
	for _, e := range r.f.entries {
		if e.saved && e.dirty {
			if err := l.s.save(r.turn, e); err == nil {
				wrote = true
			}
		}
	}
	if wrote {
		l.s.changed(l.envID, l.chatID, false)
	}
}

// promptNotes is the text a chat's next prompt starts with: the undo note and the notes of the
// integrations, one per line. sent tells the integrations the prompt went out with theirs.
func (s *Service) promptNotes(ctx context.Context, c store.Chat) (string, func(), error) {
	note, err := s.undoNote(ctx, c)
	if err != nil {
		return "", nil, err
	}
	if s.PromptNote == nil {
		return note, func() {}, nil
	}
	text, sent, err := s.PromptNote(ctx, c.EnvironmentID, c.SandboxID)
	if err != nil {
		return "", nil, err
	}
	if sent == nil {
		sent = func() {}
	}
	switch {
	case note == "":
		note = text
	case text != "":
		note += "\n" + text
	}
	return note, sent, nil
}

// start opens a turn with the user's item and runs the prompt in the background.
func (l *live) start(ctx context.Context, c store.Chat, text string, atts []Attachment, blocks []acp.ContentBlock) (store.ChatTurn, error) {
	note, sent, err := l.s.promptNotes(ctx, c)
	if err != nil {
		return store.ChatTurn{}, err
	}
	if note != "" {
		blocks = append([]acp.ContentBlock{{Type: "text", Text: note}}, blocks...)
	}
	t, err := l.s.Store.StartChatTurn(ctx, c.ID, store.OriginChat, "")
	if err != nil {
		return t, err
	}
	t = l.s.snapshot(ctx, c, t)
	r := &run{turn: t, f: newFolder(store.NewID), promptSent: make(chan struct{}), done: make(chan struct{})}
	l.mu.Lock()
	l.turn = r
	_, ch := r.f.add(KindUser, &UserBody{Text: text, Attachments: atts})
	l.write(r, ch)
	l.mu.Unlock()
	if c.Title == "" {
		l.s.retitle(c, titleOf(text, atts))
	}
	// Publish the turn before writing so early updates are retained. Cancel waits on
	// promptSent, which preserves wire order without holding l.mu during the write.
	prompt, err := l.conn.StartPrompt(context.Background(), l.session, blocks)
	close(r.promptSent)
	if err != nil {
		go l.end(r, acp.PromptResponse{}, err)
	} else {
		sent()
		go l.run(r, prompt)
	}
	return t, nil
}

func (s *Service) retitle(c store.Chat, title string) {
	if title == "" || title == c.Title {
		return
	}
	if err := s.Store.SetChatTitle(context.Background(), c.ID, title); err == nil {
		s.changed(c.EnvironmentID, c.ID, true)
	}
}

// titleOf names a chat after its first prompt.
func titleOf(text string, atts []Attachment) string {
	line, _, _ := strings.Cut(strings.TrimSpace(text), "\n")
	if line == "" && len(atts) > 0 {
		line = atts[0].Name
	}
	if len(line) > 80 {
		line = strings.TrimSpace(clip(line, 77)) + "..."
	}
	return line
}

// run ends the turn when the prompt response arrives or the agent goes away.
func (l *live) run(r *run, prompt <-chan acp.PromptResult) {
	stop := make(chan struct{})
	go func() {
		tick := time.NewTicker(flushEvery)
		defer tick.Stop()
		for {
			select {
			case <-stop:
				return
			case <-tick.C:
				l.mu.Lock()
				l.flush(r)
				l.mu.Unlock()
			}
		}
	}()
	result := <-prompt
	close(stop)
	l.end(r, result.Response, result.Err)
}

// end closes the turn: the last writes, a marker saying why it stopped, the usage.
func (l *live) end(r *run, resp acp.PromptResponse, err error) {
	ctx := context.Background()
	l.mu.Lock()
	r.ended = true
	l.mu.Unlock()
	l.dismissAsks(r, "the turn ended")
	l.mu.Lock()
	l.write(r, r.f.finish())
	reason := resp.StopReason
	var kind, text string
	switch {
	case err != nil && (errors.Is(err, acp.ErrClosed) || errors.Is(err, io.EOF) || isClosed(l.conn)):
		reason, kind, text = StopInterrupted, MarkerInterrupted, "The agent stopped before it finished."
		if r.cancelled {
			text = "Stopped. The agent didn't stop in time, so Studio ended it."
		}
	case err != nil:
		reason, kind, text = StopInterrupted, MarkerError, "The request failed: "+clip(err.Error(), 2000)
	case reason == acp.StopCancelled:
		kind, text = MarkerStopped, "Stopped."
	case reason == acp.StopMaxTokens:
		kind, text = MarkerLimit, "The agent reached its output limit."
	case reason == acp.StopMaxTurnRequests:
		kind, text = MarkerLimit, "The agent reached its limit of model requests for one message."
	case reason == acp.StopRefusal:
		kind, text = MarkerLimit, "The agent refused to continue."
	}
	if kind != "" {
		e, ch := r.f.add(KindMarker, &MarkerBody{Kind: kind, Text: text})
		e.dirty = true
		l.write(r, ch)
	}
	usage := r.f.usage
	usage.Tokens = resp.Usage
	var raw []byte
	if usage.Tokens != nil || usage.ContextUsed != nil || usage.Cost != nil {
		raw, _ = json.Marshal(usage)
	}
	title := r.f.title
	l.turn = nil
	l.mu.Unlock()

	c, cerr := l.s.Store.Chat(ctx, l.envID, l.chatID)
	if cerr != nil {
		close(r.done)
		return // deleted
	}
	if _, err := l.s.Store.EndChatTurn(ctx, l.chatID, r.turn.ID, reason, raw); err != nil && !errors.Is(err, store.ErrNotFound) {
		l.s.log().Warn("chat: end turn", "chat", l.chatID, "err", err)
	}
	l.s.changes(ctx, c, r.turn)
	l.s.retitle(c, title)
	l.s.unlock(c, store.ChatRunning)
	close(r.done)
}

func isClosed(c *acp.Conn) bool {
	select {
	case <-c.Done():
		return true
	default:
		return false
	}
}

// Cancel stops the chat's running turn: the agent gets session/cancel, open approvals
// and questions are dismissed, and if the turn hasn't ended after cancelGrace the agent
// is stopped and the turn ends interrupted.
func (s *Service) Cancel(ctx context.Context, envID, chatID string) error {
	if _, err := s.Store.Chat(ctx, envID, chatID); err != nil {
		return err
	}
	l := s.get(chatID)
	if l == nil {
		return nil
	}
	l.mu.Lock()
	r := l.turn
	if r != nil && !r.ended {
		r.cancelled = true
	} else {
		r = nil
	}
	l.mu.Unlock()
	if r == nil {
		return nil
	}
	<-r.promptSent
	if err := l.conn.Cancel(l.session); err != nil {
		l.conn.Close()
		return nil
	}
	l.dismissAsks(r, "the user stopped the turn")
	grace := cancelGrace
	go func() {
		select {
		case <-r.done:
		case <-time.After(grace):
			l.conn.Close()
		}
	}()
	return nil
}
