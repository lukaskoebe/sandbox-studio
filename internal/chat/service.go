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
	idleTimeout  = 30 * time.Minute       // how long an agent runs on with no turn
	slotRetry    = 250 * time.Millisecond // how often a start looks for a free slot
	slotWait     = 7 * time.Second        // how long a retired agent's slot may take to free
)

// Conflict is a request the chat's state refuses; the API answers 409 with its text.
type Conflict string

func (c Conflict) Error() string      { return string(c) }
func (Conflict) Is(target error) bool { return target == store.ErrConflict }

// ErrBusy refuses a prompt while the agent works on the last one.
var ErrBusy = Conflict("the agent is still working on the last request; stop it first")

// ErrStopped is Send's error when the user stopped the message while its agent started.
var ErrStopped = Conflict("stopped before the agent started on it")

// ErrInvalid marks a prompt the agent can't take.
var ErrInvalid = errors.New("invalid prompt")

// ErrNoImages is ErrInvalid for an image attached for an agent that can't take images, so
// that the user can send the text alone.
var ErrNoImages = fmt.Errorf("%w: this agent can't take images", ErrInvalid)

// ErrModelNoImages is ErrNoImages for an agent that takes images but whose model, its provider
// says, reads text only.
var ErrModelNoImages = fmt.Errorf("%w: its provider says the model reads text only", ErrNoImages)

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
// session's environment (placeholders only). NoImages says that the model can't take
// images, whatever the agent says it takes: an OpenAI-compatible endpoint's model can be text
// only behind an agent that always takes them.
type Launch struct {
	Harness  harness.Harness
	Files    []agentproto.HomeFile
	Command  []string
	Env      map[string]string
	TUIEnv   map[string]string
	NoImages bool
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
	queue map[string][]queuedPrompt // chat ID → prompts that wait for its running turn, see Queue
	// draining holds the chats whose queue sendQueued sends now, and whether it was
	// asked to look again.
	draining map[string]bool
	// stops holds the chats whose message Send starts, and whether the user stopped it
	// before its turn existed.
	stops map[string]bool
}

// queuedPrompt is a prompt that waits for its chat to become idle.
type queuedPrompt struct {
	envID  string
	launch Launch
	p      Prompt
	sent   func()
	view   QueuedPrompt
}

// QueuedPrompt is a prompt that waits for its chat's running turn to end, as the chat shows it.
type QueuedPrompt struct {
	ID          string    `json:"id"`
	Text        string    `json:"text"`
	Attachments []string  `json:"attachments,omitempty" doc:"The names of its files"`
	QueuedAt    time.Time `json:"queuedAt"`
	Error       string    `json:"error,omitempty" doc:"Why it couldn't go when the turn before it ended; it waits until the next message"`
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
	return s.create(ctx, store.Chat{EnvironmentID: sb.EnvironmentID, SandboxID: sb.ID, TeammateID: sb.TeammateID, Harness: harnessName})
}

// CreateHeartbeat adds an idle chat that a heartbeat opens, which the chat list marks as one.
func (s *Service) CreateHeartbeat(ctx context.Context, sb store.Sandbox, harnessName string) (store.Chat, error) {
	return s.create(ctx, store.Chat{EnvironmentID: sb.EnvironmentID, SandboxID: sb.ID, TeammateID: sb.TeammateID, Harness: harnessName, Heartbeat: true})
}

// CreateRun adds an idle chat for a scheduled run of a task, titled title, which the chat
// list marks as scheduled. Its title stays.
func (s *Service) CreateRun(ctx context.Context, sb store.Sandbox, harnessName, taskID, title string) (store.Chat, error) {
	return s.create(ctx, store.Chat{EnvironmentID: sb.EnvironmentID, SandboxID: sb.ID, TeammateID: sb.TeammateID, Harness: harnessName, TaskID: taskID, Title: title})
}

func (s *Service) create(ctx context.Context, c store.Chat) (store.Chat, error) {
	c, err := s.Store.CreateChat(ctx, c)
	if err == nil {
		s.changed(c.EnvironmentID, c.ID, true)
	}
	return c, err
}

// Log is a chat with all its turns and items.
type Log struct {
	Chat   store.Chat     `json:"chat"`
	Turns  []Turn         `json:"turns"`
	Items  []Item         `json:"items"`
	Queued []QueuedPrompt `json:"queued" doc:"Prompts that go once the running turn ends, in order"`
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
	out := Log{Chat: c, Turns: make([]Turn, 0, len(turns)), Items: make([]Item, 0, len(items)), Queued: s.Queued(chatID)}
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
	if c.State == store.ChatTerminal {
		if err := s.killTUI(ctx, c); err != nil {
			s.log().Debug("chat: end the terminal of a deleted chat", "chat", chatID, "err", err)
		}
	}
	if err := s.Store.DeleteChat(ctx, envID, chatID); err != nil {
		return err
	}
	// After the chat is gone, so an agent still starting finds it gone (startAgent) or is here.
	if l := s.get(chatID); l != nil {
		l.shutdown(ctx)
	}
	s.mu.Lock()
	delete(s.queue, chatID)
	s.mu.Unlock()
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
	s.mu.Lock()
	if s.stops == nil {
		s.stops = map[string]bool{}
	}
	s.stops[c.ID] = false
	s.mu.Unlock()
	l, err := s.ensure(ctx, c, launch)
	if err == nil && s.stopped(c.ID) {
		err = ErrStopped
	}
	if err == nil {
		var blocks []acp.ContentBlock
		var atts []Attachment
		if blocks, atts, err = l.prompt(p); err == nil {
			var t store.ChatTurn
			if t, err = l.start(ctx, c, p.Text, atts, blocks); err == nil {
				return t, nil
			}
		}
	}
	s.started(c.ID)
	s.unlock(c, store.ChatRunning)
	return store.ChatTurn{}, err
}

// Queue sends p like Send, or, while a turn runs, once the chat is idle again; queued says
// it waits. sent is called once p went out. The queue lives in memory: a prompt still waiting
// when Studio stops is not sent.
func (s *Service) Queue(ctx context.Context, envID, chatID string, launch Launch, p Prompt, sent func()) (queued bool, err error) {
	_, queued, err = s.Enqueue(ctx, envID, chatID, launch, p, sent)
	return queued, err
}

// Enqueue is Queue that also returns the waiting prompt as the chat shows it, when it waits.
func (s *Service) Enqueue(ctx context.Context, envID, chatID string, launch Launch, p Prompt, sent func()) (QueuedPrompt, bool, error) {
	s.retryQueue(envID, chatID)
	if _, err := s.Send(ctx, envID, chatID, launch, p); !errors.Is(err, ErrBusy) {
		if err == nil && sent != nil {
			sent()
		}
		return QueuedPrompt{}, false, err
	}
	// A prompt the running agent can't take is refused now, not once it is its turn.
	if l := s.get(chatID); l != nil {
		if _, _, err := l.prompt(p); err != nil {
			return QueuedPrompt{}, false, err
		}
	}
	view := QueuedPrompt{ID: store.NewID(), Text: p.Text, QueuedAt: time.Now().UTC()}
	for _, f := range p.Attachments {
		view.Attachments = append(view.Attachments, f.Name)
	}
	s.mu.Lock()
	if s.queue == nil {
		s.queue = map[string][]queuedPrompt{}
	}
	s.queue[chatID] = append(s.queue[chatID], queuedPrompt{envID: envID, launch: launch, p: p, sent: sent, view: view})
	s.mu.Unlock()
	s.changed(envID, chatID, false)
	// The turn may have ended since Send refused; then nothing else sends it.
	if c, err := s.Store.Chat(ctx, envID, chatID); err == nil && c.State == store.ChatIdle {
		go s.sendQueued(chatID)
	}
	return view, true, nil
}

// retryQueue clears the reason a waiting prompt couldn't go, so the queue goes on once the
// chat is idle: the user sends another message.
func (s *Service) retryQueue(envID, chatID string) {
	s.mu.Lock()
	list := slices.Clone(s.queue[chatID])
	failed := len(list) > 0 && list[0].view.Error != ""
	if failed {
		list[0].view.Error = ""
		s.queue[chatID] = list
	}
	s.mu.Unlock()
	if failed {
		s.changed(envID, chatID, false)
	}
}

// Queued returns the prompts waiting for the chat's running turn, in the order they go.
func (s *Service) Queued(chatID string) []QueuedPrompt {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]QueuedPrompt, 0, len(s.queue[chatID]))
	for _, q := range s.queue[chatID] {
		out = append(out, q.view)
	}
	return out
}

// Unqueue takes a waiting prompt off the chat's queue, so it never goes. A prompt that already
// went, or never waited, is store.ErrNotFound.
func (s *Service) Unqueue(envID, chatID, id string) error {
	s.mu.Lock()
	list := s.queue[chatID]
	i := slices.IndexFunc(list, func(q queuedPrompt) bool { return q.view.ID == id && q.envID == envID })
	if i < 0 {
		s.mu.Unlock()
		return store.ErrNotFound
	}
	s.setQueue(chatID, slices.Delete(slices.Clone(list), i, i+1))
	s.mu.Unlock()
	s.changed(envID, chatID, false)
	return nil
}

// sendQueued sends the chat's waiting prompts, one turn each, while it is idle. Only one
// call sends at a time; one that comes in meanwhile has the sender look again.
func (s *Service) sendQueued(chatID string) {
	s.mu.Lock()
	if s.draining == nil {
		s.draining = map[string]bool{}
	}
	if _, ok := s.draining[chatID]; ok {
		s.draining[chatID] = true
		s.mu.Unlock()
		return
	}
	for {
		s.draining[chatID] = false
		s.mu.Unlock()
		s.sendNext(chatID)
		s.mu.Lock()
		if !s.draining[chatID] {
			delete(s.draining, chatID)
			s.mu.Unlock()
			return
		}
	}
}

// sendNext sends the chat's first waiting prompt. A prompt that can't go stays first with
// the reason, and the queue waits until the user sends or queues another message.
func (s *Service) sendNext(chatID string) {
	s.mu.Lock()
	list := s.queue[chatID]
	if len(list) == 0 || list[0].view.Error != "" {
		s.mu.Unlock()
		return
	}
	q := list[0]
	s.mu.Unlock()
	_, err := s.Send(context.Background(), q.envID, chatID, q.launch, q.p)
	if errors.Is(err, ErrBusy) {
		return // it goes when the turn that runs now ends
	}
	s.mu.Lock()
	list = slices.Clone(s.queue[chatID])
	if i := slices.IndexFunc(list, func(w queuedPrompt) bool { return w.view.ID == q.view.ID }); i >= 0 {
		if err == nil {
			list = slices.Delete(list, i, i+1)
		} else {
			list[i].view.Error = clip(err.Error(), 500)
		}
		s.setQueue(chatID, list)
	}
	s.mu.Unlock()
	if err != nil {
		s.log().Warn("chat: send a queued prompt", "chat", chatID, "err", err)
	} else if q.sent != nil {
		q.sent()
	}
	s.changed(q.envID, chatID, false)
}

// setQueue replaces the chat's queue. s.mu is held.
func (s *Service) setQueue(chatID string, list []queuedPrompt) {
	if len(list) == 0 {
		delete(s.queue, chatID)
	} else {
		s.queue[chatID] = list
	}
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
		go s.sendQueued(c.ID)
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
	s         *Service
	envID     string
	chatID    string
	sandboxID string
	conn      *acp.Conn
	caps      acp.AgentCapabilities
	textOnly  bool // the model can't take images, whatever caps says (Launch.NoImages)
	session   string

	mu     sync.Mutex
	turn   *run    // the running turn
	replay *folder // collects session/load's replay
	asks   map[string]*ask
	used   time.Time   // when the last turn started or ended
	idle   *time.Timer // retires the agent after idleTimeout without a turn
}

// run is a running turn.
type run struct {
	turn       store.ChatTurn
	f          *folder
	cancelled  bool
	ended      bool
	promptSent chan struct{} // closed after session/prompt is written
	answered   chan struct{} // closed once the prompt's response or error arrived
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
	rwc, err := s.openACP(ctx, c, agentproto.StartACP{Chat: c.ID, Harness: h.Name(), Command: launch.Command, Env: launch.Env})
	var he agentchan.HarnessError
	if errors.As(err, &he) {
		return nil, nil, Conflict(he.Error())
	}
	if err != nil {
		return nil, nil, fmt.Errorf("starting %s's chat agent: %w", h.Name(), err)
	}
	l := &live{s: s, envID: c.EnvironmentID, chatID: c.ID, sandboxID: c.SandboxID, textOnly: launch.NoImages, asks: map[string]*ask{}, used: time.Now()}
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
	// A chat deleted while its agent started doesn't keep it (see Delete).
	if _, err := s.Store.Chat(ctx, c.EnvironmentID, c.ID); errors.Is(err, store.ErrNotFound) {
		l.conn.Close()
		s.forgetLive(l)
		return nil, nil, err
	}
	l.mu.Lock()
	l.rest()
	l.mu.Unlock()
	go func() {
		<-l.conn.Done()
		l.mu.Lock()
		l.idle.Stop()
		l.mu.Unlock()
		s.forgetLive(l)
	}()
	return l, replay, nil
}

// openACP starts the chat's agent in the guest. A sandbox runs agentproto.MaxACPProcesses
// agents at most; when it has no room, the agents of other chats that sat idle longest are
// retired, and their sessions resume with the next message.
func (s *Service) openACP(ctx context.Context, c store.Chat, req agentproto.StartACP) (io.ReadWriteCloser, error) {
	var retired time.Time
	for {
		rwc, err := s.Guest.OpenACP(ctx, c.SandboxID, req)
		if !errors.Is(err, agentchan.ErrACPFull) {
			return rwc, err
		}
		// A retired agent's slot frees once its process exited, a moment after its stream:
		// wait for that rather than retire another.
		if time.Since(retired) > slotWait {
			if !s.makeRoom(c) {
				return nil, Conflict(fmt.Sprintf("this sandbox already runs %d chats that are busy; wait for one to finish or stop it", agentproto.MaxACPProcesses))
			}
			retired = time.Now()
		}
		select {
		case <-ctx.Done():
			return nil, fmt.Errorf("waiting for another chat's agent to stop: %w", ctx.Err())
		case <-time.After(slotRetry):
		}
	}
}

// makeRoom retires the idle agent that was used longest ago among the other chats of c's
// sandbox, and says whether there was one.
func (s *Service) makeRoom(c store.Chat) bool {
	s.mu.Lock()
	var others []*live
	for _, l := range s.live {
		if l.sandboxID == c.SandboxID && l.chatID != c.ID {
			others = append(others, l)
		}
	}
	s.mu.Unlock()
	slices.SortFunc(others, func(a, b *live) int { return a.lastUsed().Compare(b.lastUsed()) })
	for _, l := range others {
		if s.retire(l) {
			return true
		}
	}
	return false
}

// retire stops l if its chat is idle and its agent waits on nothing. The chat is held
// running meanwhile, so no turn starts on the agent going away: a message sent then waits
// in the queue, and goes once the chat is idle again.
func (s *Service) retire(l *live) bool {
	ctx := context.Background()
	if !l.resting() {
		return false
	}
	if _, err := s.Store.SetChatState(ctx, l.envID, l.chatID, store.ChatRunning, store.ChatIdle); err != nil {
		return false
	}
	defer func() {
		if _, err := s.Store.SetChatState(ctx, l.envID, l.chatID, store.ChatIdle, store.ChatRunning); err == nil {
			go s.sendQueued(l.chatID)
		}
	}()
	// A turn starts only with the chat held, so with it held now l stays as it is.
	if !l.resting() {
		return false
	}
	s.log().Debug("chat: retire an idle agent", "chat", l.chatID)
	l.shutdown(ctx)
	return true
}

// resting says whether l runs no turn and waits on no answer.
func (l *live) resting() bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.turn == nil && l.replay == nil && len(l.asks) == 0
}

func (l *live) lastUsed() time.Time {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.used
}

// rest notes that l's turn ended, or it just started, and retires it after idleTimeout
// with no other turn. l.mu is held.
func (l *live) rest() {
	l.used = time.Now()
	if l.idle != nil {
		l.idle.Stop()
	}
	l.idle = time.AfterFunc(idleTimeout, func() { l.s.retire(l) })
}

// prompt turns p into the agent's content (promptBlocks). A model that reads text only is
// sent no images, whatever its agent takes.
func (l *live) prompt(p Prompt) ([]acp.ContentBlock, []Attachment, error) {
	caps := l.caps
	if l.textOnly {
		caps.PromptCapabilities.Image = false
	}
	blocks, atts, err := promptBlocks(caps, p)
	if errors.Is(err, ErrNoImages) && l.caps.PromptCapabilities.Image {
		err = ErrModelNoImages
	}
	return blocks, atts, err
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
	newSession := func() (*folder, error) {
		id, err := l.conn.NewSession(ctx, Workdir, servers)
		if err != nil {
			return nil, fmt.Errorf("new session: %w", err)
		}
		l.session = id
		return nil, l.s.Store.SetChatSession(ctx, c.ID, id)
	}
	if c.ACPSessionID == "" {
		return newSession()
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
	if errors.As(err, &ae) && !l.s.answered(ctx, c) {
		// The agent never kept a session it didn't answer in, such as one whose first
		// message was stopped: nothing is lost by starting over.
		return newSession()
	}
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

// answered says whether the agent wrote anything in the chat, which its session would hold:
// an answer, or a turn in the terminal. It errs towards yes.
func (s *Service) answered(ctx context.Context, c store.Chat) bool {
	turns, err := s.Store.ChatTurns(ctx, c.ID)
	if err != nil || slices.ContainsFunc(turns, func(t store.ChatTurn) bool { return t.Origin == store.OriginTerminal }) {
		return true
	}
	items, err := s.Store.ChatItems(ctx, c.ID, "")
	if err != nil {
		return true
	}
	return slices.ContainsFunc(items, func(it store.ChatItem) bool {
		return it.Kind == KindAgent || it.Kind == KindThought || it.Kind == KindTool || it.Kind == KindPlan
	})
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
	r := &run{turn: t, f: newFolder(store.NewID), promptSent: make(chan struct{}), answered: make(chan struct{}), done: make(chan struct{})}
	l.mu.Lock()
	l.turn = r
	l.used = time.Now()
	l.idle.Stop()
	_, ch := r.f.add(KindUser, &UserBody{Text: text, Attachments: atts})
	l.write(r, ch)
	l.mu.Unlock()
	if c.Title == "" {
		l.s.retitle(c, titleOf(text, atts))
	}
	// Publish the turn before writing so early updates are retained. Cancel waits on
	// promptSent, which preserves wire order without holding l.mu during the write.
	// A stop that came while the turn started, after Send looked (see Cancel).
	stop := l.s.started(c.ID)
	prompt, err := l.conn.StartPrompt(context.Background(), l.session, blocks)
	close(r.promptSent)
	if stop {
		go func() { _ = l.s.Cancel(context.Background(), c.EnvironmentID, c.ID) }()
	}
	if err != nil {
		go l.end(r, acp.PromptResponse{}, err)
	} else {
		sent()
		go l.run(r, prompt)
	}
	return t, nil
}

// retitle names a chat. A scheduled run's chat keeps the title of its task and time.
func (s *Service) retitle(c store.Chat, title string) {
	if title == "" || title == c.Title || c.TaskID != "" {
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
	close(r.answered)
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
	l.rest()
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

// stopped says whether the user stopped the message Send is starting.
func (s *Service) stopped(chatID string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.stops[chatID]
}

// started ends Send's start of the chat's message, and says whether the user stopped it.
func (s *Service) started(chatID string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	stop := s.stops[chatID]
	delete(s.stops, chatID)
	return stop
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
	// A message whose agent still starts has no turn yet: Send stops it before it goes, or
	// start once its turn exists. The note goes first, so one of the two sees the other.
	s.mu.Lock()
	if _, starting := s.stops[chatID]; starting {
		s.stops[chatID] = true
	}
	s.mu.Unlock()
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
		case <-r.answered: // the turn stopped; what follows, such as the diff, may take long
		case <-time.After(grace):
			l.conn.Close()
		}
	}()
	return nil
}
