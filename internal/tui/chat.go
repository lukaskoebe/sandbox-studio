package tui

import (
	"context"
	"fmt"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"time"

	"charm.land/bubbles/v2/key"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/lukaskoebe/sandbox-studio/internal/chat"
	"github.com/lukaskoebe/sandbox-studio/internal/sandboxes"
	"github.com/lukaskoebe/sandbox-studio/internal/store"
)

// Retries of the chat screens: a lost stream reopens after chatRetryDelay from the last rev, as
// the web does; a chat that could not be read is read again after chatReloadDelay.
const (
	chatRetryDelay  = 2 * time.Second
	chatReloadDelay = 5 * time.Second
)

// chatPageTurns is how many turns a page of a chat's log brings, as on the web: a long chat
// opens with its last turns, and the earlier ones load as the transcript reaches its top.
const chatPageTurns = 40

// chatState is what the chat screen shows: a chat of a sandbox with its transcript, kept
// current from the chat's stream.
type chatState struct {
	sandbox sandboxes.View // the sandbox whose chat this is
	back    screen         // where esc returns to
	picking bool           // c waits for the sandbox's chats, to open the newest
	inPane  bool           // the chat shows in Home's pane, not on its own screen

	id     string // the chat on show; "" for a new chat, which the first prompt starts
	gen    int    // counts the chats opened, so the messages of a stream left behind are dropped
	cancel context.CancelFunc
	stream <-chan ChatEvent
	loaded bool // the chat's log arrived
	live   bool // the stream is connected
	gone   bool // the chat was deleted
	chat   store.Chat
	turns  map[string]chat.Turn
	items  map[string]chat.Item
	rev    int64 // the last rev seen, where the stream resumes
	from   int   // the first turn, by seq, whose items are loaded; 0 for every item
	early  bool  // the turns before from are on their way

	expanded map[string]bool         // the folded items the user opened, by item ID
	rendered map[string]renderedText // the agent's messages drawn as markdown, by item ID
	laid     map[string]laidItem     // the transcript's items laid out, by item ID
	sorted   []chat.Item             // the items in order, until one changes; nil to sort again
	fold     int                     // the folded item under the cursor, among the foldable ones; -1 for none
	scroll   int                     // rows the transcript is scrolled up from its end

	composing bool  // the composer takes the keys
	draft     input // the prompt being written
	sending   bool  // a prompt is on its way
}

// The messages of the chat screens. gen ties a chat's messages to the chat they were for.
type (
	chatsListed struct {
		sandbox string
		list    []store.Chat
		err     error
	}
	chatLoaded struct {
		gen int
		log chat.Log
		err error
	}
	chatStreamed struct {
		gen int
		ch  <-chan ChatEvent
		err error
	}
	chatEventMsg struct {
		gen int
		ev  ChatEvent
	}
	chatEarlier struct {
		gen int
		log chat.Log
		err error
	}
	chatStreamEnded struct{ gen int }
	chatRetry       struct{ gen int }
	chatCreated     struct {
		chat store.Chat
		err  error
	}
	promptSent struct {
		gen int
		err error
	}
)

// The keys of the chat screens.
var (
	keyPrompt  = key.NewBinding(key.WithKeys("p"), key.WithHelp("p", "prompt"))
	keyExpand  = key.NewBinding(key.WithKeys("enter", "space"), key.WithHelp("enter", "expand"))
	keyStop    = key.NewBinding(key.WithKeys("x"), key.WithHelp("x", "stop"))
	keyNewChat = key.NewBinding(key.WithKeys("n"), key.WithHelp("n", "new chat"))
	keySend    = key.NewBinding(key.WithKeys("enter"), key.WithHelp("enter", "send"))
	keyNewline = key.NewBinding(key.WithKeys("shift+enter", "ctrl+j"), key.WithHelp("ctrl+j", "new line"))
	keyLeave   = key.NewBinding(key.WithKeys("esc"), key.WithHelp("esc", "close"))
	keyTools   = key.NewBinding(key.WithKeys("up", "k", "down", "j"), key.WithHelp("↑/↓", "tools"))
	keyPageUp  = key.NewBinding(key.WithKeys("pgup", "ctrl+b", "b"), key.WithHelp("pgup", "scroll"))
	keyPageDn  = key.NewBinding(key.WithKeys("pgdown", "ctrl+f", "space"))
	keyTop     = key.NewBinding(key.WithKeys("home", "g"))
	keyBottom  = key.NewBinding(key.WithKeys("end", "G"))
)

// openChats opens the newest chat of a sandbox, or a new one when it has none. Only a sandbox
// with a teammate has chats: the chat is with its teammate.
func (m *app) openChats(sb *sandboxes.View) tea.Cmd {
	if !m.chatsIn(sb) {
		return nil
	}
	m.stopChat()
	m.chats = chatState{sandbox: *sb, back: m.screen, fold: -1, picking: true, gen: m.chats.gen}
	return m.loadChats()
}

// newChat opens the composer of a new chat in a sandbox; the first prompt starts it.
func (m *app) newChat(sb *sandboxes.View) tea.Cmd {
	if !m.chatsIn(sb) {
		return nil
	}
	m.stopChat()
	m.chats = chatState{sandbox: *sb, back: m.screen, fold: -1, gen: m.chats.gen}
	return m.openChat("")
}

// chatsIn reports whether a sandbox has chats, saying why not when it hasn't.
func (m *app) chatsIn(sb *sandboxes.View) bool {
	if sb == nil {
		return false
	}
	if sb.TeammateID == "" {
		m.setNotice(sb.Name+" has no teammate to chat with. Give it one in the browser.", true)
		return false
	}
	return true
}

// loadChats reads the chats of the chat screen's sandbox, for c to open the newest.
func (m *app) loadChats() tea.Cmd {
	ctx, client, env, sandbox := m.ctx, m.client, m.env.ID, m.chats.sandbox.ID
	return func() tea.Msg {
		rctx, cancel := requestContext(ctx)
		defer cancel()
		list, err := client.SandboxChats(rctx, env, sandbox)
		return chatsListed{sandbox: sandbox, list: list, err: err}
	}
}

// openChat shows a chat: its log, then its stream from the log's rev. An empty id opens a new
// chat, whose composer starts it.
func (m *app) openChat(id string) tea.Cmd {
	m.stopChat()
	c := &m.chats
	c.gen++
	c.id, c.loaded, c.live, c.gone = id, false, false, false
	c.chat, c.rev = store.Chat{}, 0
	c.turns, c.items, c.expanded = map[string]chat.Turn{}, map[string]chat.Item{}, map[string]bool{}
	c.laid, c.sorted = map[string]laidItem{}, nil
	c.from, c.early = 0, false
	c.fold, c.scroll = -1, 0
	if !c.inPane {
		m.screen = screenChat
	}
	if id == "" {
		c.composing = true
		return nil
	}
	return m.loadChat()
}

// stopChat ends the stream of the chat on show.
func (m *app) stopChat() {
	if m.chats.cancel != nil {
		m.chats.cancel()
		m.chats.cancel = nil
	}
	m.chats.stream, m.chats.live = nil, false
}

func (m *app) loadChat() tea.Cmd {
	ctx, client, env, id, gen := m.ctx, m.client, m.env.ID, m.chats.id, m.chats.gen
	return func() tea.Msg {
		rctx, cancel := requestContext(ctx)
		defer cancel()
		log, err := client.Chat(rctx, env, id, chatPageTurns, 0)
		return chatLoaded{gen: gen, log: log, err: err}
	}
}

// earlierTurns loads the page of turns before the first one loaded, once the transcript shows
// its top. The view keeps its place: it is held in rows from the end.
func (m *app) earlierTurns() tea.Cmd {
	c := &m.chats
	if !c.loaded || c.earlier() == 0 || c.early || c.id == "" || !m.chatShows() || !m.transcriptAtTop() {
		return nil
	}
	c.early = true
	ctx, client, env, id, gen, before := m.ctx, m.client, m.env.ID, c.id, c.gen, c.from
	return func() tea.Msg {
		rctx, cancel := requestContext(ctx)
		defer cancel()
		log, err := client.Chat(rctx, env, id, chatPageTurns, before)
		return chatEarlier{gen: gen, log: log, err: err}
	}
}

// earlier is how many turns before the loaded ones have their items still to load.
func (c *chatState) earlier() int {
	n := 0
	for _, t := range c.turns {
		if t.Seq < c.from {
			n++
		}
	}
	return n
}

// transcriptAtTop reports whether the transcript's first row is on show.
func (m *app) transcriptAtTop() bool {
	c := &m.chats
	scroll := c.scroll
	if c.fold >= 0 {
		scroll = m.foldScroll(c.fold)
	}
	return len(m.transcript(m.chatWidth()).lines)-scroll <= m.transcriptRows()
}

// followChat opens the chat's stream from the last rev seen.
func (m *app) followChat() tea.Cmd {
	ctx, cancel := context.WithCancel(m.ctx)
	m.chats.cancel = cancel
	client, env, id, gen, rev := m.client, m.env.ID, m.chats.id, m.chats.gen, m.chats.rev
	return func() tea.Msg {
		ch, err := client.ChatStream(ctx, env, id, rev)
		return chatStreamed{gen: gen, ch: ch, err: err}
	}
}

// listenChat waits for the next change on a chat's stream.
func listenChat(gen int, ch <-chan ChatEvent) tea.Cmd {
	return func() tea.Msg {
		ev, ok := <-ch
		if !ok {
			return chatStreamEnded{gen: gen}
		}
		return chatEventMsg{gen: gen, ev: ev}
	}
}

func retryChat(gen int, after time.Duration) tea.Cmd {
	return tea.Tick(after, func(time.Time) tea.Msg { return chatRetry{gen: gen} })
}

// chatUpdate applies the messages of the chat screens. ok is false for other messages.
func (m *app) chatUpdate(msg tea.Msg) (cmd tea.Cmd, ok bool) {
	c := &m.chats
	current := func(gen int) bool {
		return gen == c.gen && m.chatShows() && c.id != ""
	}
	switch msg := msg.(type) {
	case chatsListed:
		if msg.sandbox != c.sandbox.ID || !c.picking || m.screen == screenChat {
			return nil, true
		}
		c.picking = false
		if msg.err != nil {
			m.fail(msg.err)
			return nil, true
		}
		if len(msg.list) == 0 {
			return m.openChat(""), true
		}
		return m.openChat(byActivity(msg.list)[0].ID), true
	case chatLoaded:
		if !current(msg.gen) {
			return nil, true
		}
		if msg.err != nil {
			if apiStatus(msg.err) == http.StatusNotFound {
				c.gone = true
				return nil, true
			}
			m.fail(msg.err)
			return retryChat(c.gen, chatReloadDelay), true
		}
		if !c.loaded {
			c.loaded = true
			c.chat, c.rev = msg.log.Chat, msg.log.Chat.Rev
			for _, t := range msg.log.Turns {
				c.turns[t.ID] = t
			}
			for _, it := range msg.log.Items {
				c.items[it.ID] = it
			}
			c.from, c.sorted = msg.log.From, nil
		}
		return tea.Batch(m.followChat(), m.earlierTurns()), true
	case chatEarlier:
		if !current(msg.gen) {
			return nil, true
		}
		c.early = false
		if msg.err != nil {
			m.setNotice("Could not load the earlier turns: "+msg.err.Error(), true)
			return nil, true
		}
		// The cursor stays on its folded item, which the turns above it move down the list.
		cursor := ""
		if t := m.transcript(m.chatWidth()); c.fold >= 0 && c.fold < len(t.folds) {
			cursor = t.folds[c.fold].id
		}
		for _, t := range msg.log.Turns {
			if old, ok := c.turns[t.ID]; !ok || old.Rev <= t.Rev {
				c.turns[t.ID] = t
			}
		}
		for _, it := range msg.log.Items {
			if old, ok := c.items[it.ID]; !ok || old.Rev <= it.Rev {
				c.items[it.ID] = it
			}
		}
		c.from, c.sorted = msg.log.From, nil
		if cursor != "" {
			for i, f := range m.transcript(m.chatWidth()).folds {
				if f.id == cursor {
					c.fold = i
				}
			}
		}
		return m.earlierTurns(), true
	case chatStreamed:
		if !current(msg.gen) {
			return nil, true
		}
		if msg.err != nil {
			if apiStatus(msg.err) == http.StatusNotFound {
				c.gone = true
				return nil, true
			}
			return retryChat(c.gen, chatRetryDelay), true
		}
		c.stream, c.live = msg.ch, true
		return listenChat(c.gen, msg.ch), true
	case chatEventMsg:
		if !current(msg.gen) {
			return nil, true
		}
		c.apply(msg.ev)
		return listenChat(c.gen, c.stream), true
	case chatStreamEnded:
		if !current(msg.gen) {
			return nil, true
		}
		c.stream, c.live = nil, false
		return retryChat(c.gen, chatRetryDelay), true
	case chatRetry:
		if !current(msg.gen) || c.gone {
			return nil, true
		}
		if !c.loaded {
			return m.loadChat(), true
		}
		return m.followChat(), true
	case chatCreated:
		c.sending = false
		if msg.err != nil {
			c.composing = true
			m.setNotice("Could not start the chat: "+msg.err.Error(), true)
			return nil, true
		}
		c.draft.set("")
		m.setNotice("", false)
		if !m.chatShows() || c.id != "" {
			return nil, true
		}
		return m.openChat(msg.chat.ID), true
	case promptSent:
		if msg.gen != c.gen {
			return nil, true
		}
		c.sending = false
		if msg.err != nil {
			c.composing = true
			m.setNotice("Could not send: "+msg.err.Error(), true)
			return nil, true
		}
		c.draft.set("")
		c.fold, c.scroll = -1, 0
	default:
		return nil, false
	}
	return nil, true
}

// apply takes one change of the stream: rows only move forward in rev, so an older version of
// a turn or item never replaces a newer one.
func (c *chatState) apply(ev ChatEvent) {
	switch {
	case ev.Chat != nil:
		c.chat = *ev.Chat
	case ev.Turn != nil:
		if old, ok := c.turns[ev.Turn.ID]; !ok || old.Rev <= ev.Turn.Rev {
			c.turns[ev.Turn.ID] = *ev.Turn
			c.sorted = nil // a turn places its items before or after from
		}
	case ev.Item != nil:
		if old, ok := c.items[ev.Item.ID]; !ok || old.Rev <= ev.Item.Rev {
			c.items[ev.Item.ID] = *ev.Item
			c.sorted = nil
		}
	}
	c.rev = max(c.rev, ev.Rev)
}

// sortedItems is the transcript in order. It is sorted again only once an item changes, as the
// transcript is laid out for every frame; callers don't change it. The items the stream brings
// for a turn before the loaded ones wait for their page, so they don't show out of their place.
func (c *chatState) sortedItems() []chat.Item {
	if c.sorted != nil {
		return c.sorted
	}
	out := make([]chat.Item, 0, len(c.items))
	for _, it := range c.items {
		if t, ok := c.turns[it.TurnID]; ok && c.from > 0 && t.Seq < c.from {
			continue
		}
		out = append(out, it)
	}
	slices.SortFunc(out, func(a, b chat.Item) int { return a.Seq - b.Seq })
	c.sorted = out
	return out
}

// pendingAsk is the first permission request or question the agent waits on, which the number
// keys answer.
func (c *chatState) pendingAsk() (chat.Item, bool) {
	for _, it := range c.sortedItems() {
		if (it.Kind == chat.KindPermission || it.Kind == chat.KindQuestion) && it.Status == chat.StatusPending {
			return it, true
		}
	}
	return chat.Item{}, false
}

// running reports whether the chat on show has a turn underway.
func (c *chatState) running() bool {
	return c.chat.State == store.ChatRunning
}

// chatKey answers the transcript: the cursor moves between the folded items, and the keys write,
// stop and start chats.
func (m *app) chatKey(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	c := &m.chats
	if c.composing {
		return m.composeKey(msg)
	}
	if it, ok := c.pendingAsk(); ok {
		if k, handled := m.buttonKey(msg, askOwner(it), askCaps(it), 0); handled {
			if k == "" {
				return m, nil
			}
			msg = pressOf(k) // the focused choice's key
		}
	}
	if cmd, ok := m.askKey(msg); ok {
		return m, cmd
	}
	folds := len(m.transcript(m.chatWidth()).folds)
	switch {
	case key.Matches(msg, keyBack):
		m.stopChat()
		m.screen = c.back
	case key.Matches(msg, keyUp):
		switch {
		case folds == 0:
		case c.fold < 0:
			c.fold = m.lastFoldInView()
		default:
			c.fold = max(c.fold-1, 0)
		}
		c.scroll = m.foldScroll(c.fold)
	case key.Matches(msg, keyDown):
		if c.fold >= 0 {
			c.fold++
			if c.fold >= folds {
				c.fold, c.scroll = -1, 0 // past the last one: back to following the end
			} else {
				c.scroll = m.foldScroll(c.fold)
			}
		}
	case key.Matches(msg, keyExpand) && c.fold >= 0 && folds > 0:
		c.fold = min(c.fold, folds-1)
		id := m.transcript(m.chatWidth()).folds[c.fold].id
		c.expanded[id] = !c.expanded[id]
		c.scroll = m.foldScroll(c.fold)
	case key.Matches(msg, keyPrompt), key.Matches(msg, keyOpen):
		return m, m.startComposing()
	case key.Matches(msg, keyPageUp):
		m.scrollTranscript(-1)
	case key.Matches(msg, keyPageDn):
		m.scrollTranscript(1)
	case key.Matches(msg, keyTop):
		c.fold, c.scroll = -1, len(m.transcript(m.chatWidth()).lines)
	case key.Matches(msg, keyBottom):
		c.fold, c.scroll = -1, 0
	case key.Matches(msg, keyStop):
		return m, m.stopTurn()
	case key.Matches(msg, keyNewChat):
		return m, m.openChat("")
	case key.Matches(msg, keyApprovals):
		m.openApprovals()
	case key.Matches(msg, keyAnswerIt) && m.screen == screenChat:
		if card, ok := m.sandboxCard(c.sandbox.ID); ok {
			id := card.first().ID
			m.openApprovalsOn(id, &resumeState{approval: id, back: screenChat})
		}
	case key.Matches(msg, keyHelp):
		m.openHelp()
	}
	return m, m.earlierTurns()
}

// askKey answers the permission request or question the agent waits on, as the web's cards in
// the chat do: a number picks a choice, i ignores the request or skips the question, and r types
// the answer to a question that asks for text. ok is false when the key is not for it.
func (m *app) askKey(msg tea.KeyPressMsg) (cmd tea.Cmd, ok bool) {
	c := &m.chats
	it, pending := c.pendingAsk()
	if !pending {
		return nil, false
	}
	n := digit(msg)
	if it.Kind == chat.KindPermission && it.Permission != nil && it.ApprovalID != "" {
		title := firstNonEmpty(oneLine(it.Permission.ToolCall.Title), "the tool call")
		picks := permissionPicks(it.Permission.Options)
		switch {
		case n > 0 && n <= len(picks):
			p := picks[n-1]
			d := Decision{Action: p.action, Scope: "environment", Remember: p.remember}
			return m.decideApproval(it.ApprovalID, d, "Chose “"+oneLine(p.label)+"” for", title), true
		case key.Matches(msg, keyIgnore):
			return m.decideApproval(it.ApprovalID, Decision{Action: "dismiss", Scope: "environment"}, "Ignored", "the permission request"), true
		}
		return nil, false
	}
	if it.Kind != chat.KindQuestion || it.Question == nil {
		return nil, false
	}
	f, answerable := questionForm(it.Question.Schema)
	switch {
	case key.Matches(msg, keySkip):
		return m.answerQuestion(c.id, it.ID, Answer{Action: "decline"}, "Skipped the question."), true
	case !answerable:
		if n > 0 || key.Matches(msg, keyReply) {
			m.setNotice("open in browser: "+m.browserURL("/e/"+m.env.ID+"/chats/"+c.id), false)
			return nil, true
		}
	case n > 0 && n <= len(f.picks):
		p := f.picks[n-1]
		return m.answerQuestion(c.id, it.ID, Answer{Action: "accept", Content: answerContent(f, p.value)}, "Answered “"+p.label+"”."), true
	case key.Matches(msg, keyReply) && (f.kind == "text" || f.kind == "number"):
		m.askAnswer(it.ID, f)
		return nil, true
	}
	return nil, false
}

// askAnswer types the answer to a question that asks for text or a number on the notice line.
func (m *app) askAnswer(item string, f questionField) {
	chatID := m.chats.id
	m.ask(&linePrompt{
		label: "Answer: ",
		enter: keyAnswer,
		submit: func(text string) (string, tea.Cmd) {
			text = strings.TrimSpace(text)
			var value any = text
			if f.kind == "number" {
				n, err := strconv.ParseFloat(text, 64)
				if err != nil {
					return "a number", nil
				}
				value = n
			}
			return "", m.answerQuestion(chatID, item, Answer{Action: "accept", Content: answerContent(f, value)}, "Answered.")
		},
	})
}

// composeKey types the prompt: enter sends it, ctrl+j starts a new line, esc puts it aside.
func (m *app) composeKey(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	c := &m.chats
	switch {
	case key.Matches(msg, keyNewline):
		c.draft.paste("\n")
	case key.Matches(msg, keySend):
		return m, m.send()
	case key.Matches(msg, keyEsc):
		c.composing = false
		if c.id == "" { // a new chat that never started
			m.screen = c.back
		}
	default:
		c.draft.key(msg)
	}
	return m, nil
}

// startComposing opens the composer, unless the chat can't take a prompt here.
func (m *app) startComposing() tea.Cmd {
	c := &m.chats
	switch {
	case c.gone:
		m.setNotice("This chat was deleted. n starts a new one.", true)
	case c.chat.State == store.ChatTerminal:
		m.setNotice("This chat continues in the sandbox's terminal. Bring it back to chat in the browser.", false)
	case c.chat.State == store.ChatOrphaned:
		m.setNotice("This chat ended: its sandbox was reset or removed. n starts a new one.", false)
	default:
		c.composing = true
	}
	return nil
}

// send sends the draft: the first prompt starts a new chat.
func (m *app) send() tea.Cmd {
	c := &m.chats
	text := strings.TrimSpace(c.draft.value())
	if text == "" || c.sending {
		return nil
	}
	c.composing, c.sending = false, true
	ctx, client, env, sandbox, id, gen := m.ctx, m.client, m.env.ID, c.sandbox.ID, c.id, c.gen
	if id == "" {
		m.setNotice("Starting the chat…", false)
		return func() tea.Msg {
			created, err := client.CreateChat(ctx, env, sandbox, text)
			return chatCreated{chat: created, err: err}
		}
	}
	m.setNotice("", false)
	return func() tea.Msg {
		_, err := client.SendPrompt(ctx, env, id, text)
		return promptSent{gen: gen, err: err}
	}
}

// stopTurn cancels the running turn of the chat on show.
func (m *app) stopTurn() tea.Cmd {
	c := &m.chats
	if c.id == "" || !c.running() {
		m.setNotice("No turn is running.", false)
		return nil
	}
	m.setNotice("Stopping the turn…", false)
	ctx, client, env, id := m.ctx, m.client, m.env.ID, c.id
	return func() tea.Msg {
		rctx, cancel := requestContext(ctx)
		defer cancel()
		if err := client.CancelChat(rctx, env, id); err != nil {
			return actionDone{err: fmt.Errorf("stop the turn: %w", err)}
		}
		return actionDone{done: "Stopped the turn."}
	}
}

// scrollTranscript moves the transcript a page up (-1) or down (1). The fold cursor lets go, so
// the page stays where it was put.
func (m *app) scrollTranscript(dir int) {
	c := &m.chats
	rows := m.transcriptRows()
	total := len(m.transcript(m.chatWidth()).lines)
	if c.fold >= 0 {
		c.scroll = m.foldScroll(c.fold)
		c.fold = -1
	}
	c.scroll = min(max(c.scroll-dir*max(rows-1, 1), 0), max(total-rows, 0))
}

// transcriptRows is how many rows the transcript gets: the body less the chat's title line and
// the composer.
func (m *app) transcriptRows() int {
	return max(m.bodyRows()-1-m.composerRows(), 1)
}

// chatShows reports whether the chat is on show: on its screen, or in Home's pane.
func (m *app) chatShows() bool {
	return m.screen == screenChat || m.chats.inPane
}

// chatWidth is the width the chat on show is laid out in: the screen's, or the pane's.
func (m *app) chatWidth() int {
	if m.chats.inPane {
		return m.paneW()
	}
	return m.width
}

// lastFoldInView is the last folded item the transcript shows, or the last one above the view.
func (m *app) lastFoldInView() int {
	t := m.transcript(m.chatWidth())
	end := max(len(t.lines)-m.chats.scroll, 0)
	pick := 0
	for i, f := range t.folds {
		if f.line < end {
			pick = i
		}
	}
	return pick
}

// foldScroll is the scroll that shows the fold at index i: the view moves only as far as it
// needs to, and keeps the item's open body in view when it fits.
func (m *app) foldScroll(i int) int {
	t := m.transcript(m.chatWidth())
	rows := m.transcriptRows()
	if i < 0 || i >= len(t.folds) {
		return m.chats.scroll
	}
	f := t.folds[i]
	total := len(t.lines)
	end := total - m.chats.scroll // the row after the last one on show
	start := end - rows
	switch {
	case f.line < start:
		end = f.line + rows
	case f.end > end:
		end = min(f.end, f.line+rows)
	}
	return min(max(total-end, 0), max(total-rows, 0))
}

// folded is a folded item of the transcript: its item, its first line and the line after it.
type folded struct {
	id        string
	line, end int
}

// transcriptView is the chat as lines of a width, and where its folded items are.
type transcriptView struct {
	lines []string
	folds []folded
}

// transcript lays the chat on show out in w cells: the user's prompts, the agent's answers with
// light styling, and its tool calls and thoughts folded to one line each.
func (m *app) transcript(w int) transcriptView {
	c := &m.chats
	var out transcriptView
	items := c.sortedItems()
	liveID := ""
	if c.running() {
		for _, it := range items {
			if t, ok := c.turns[it.TurnID]; ok && t.EndedAt == nil {
				liveID = it.ID
			}
		}
	}
	ask, _ := c.pendingAsk()
	askID := ask.ID
	if n := c.earlier(); n > 0 {
		more := fmt.Sprintf("↑ %d earlier %s · scroll up to load them", n, plural(n, "turn", "turns"))
		if c.early {
			more = fmt.Sprintf("Loading %d earlier %s…", n, plural(n, "turn", "turns"))
		}
		out.lines = append(out.lines, "  "+styleDim.Render(more), "")
	}
	prevKind := ""
	for _, it := range items {
		gap := prevKind != "" && (it.Kind == chat.KindUser || it.Kind == chat.KindAgent || prevKind == chat.KindUser || prevKind == chat.KindAgent)
		if gap {
			out.lines = append(out.lines, "")
		}
		prevKind = it.Kind
		if it.Kind == chat.KindThought || it.Kind == chat.KindTool {
			out.folds = append(out.folds, folded{id: it.ID, line: len(out.lines)})
		}
		out.lines = append(out.lines, m.itemLines(it, it.ID == liveID, it.ID == askID, w)...)
		if it.Kind == chat.KindThought || it.Kind == chat.KindTool {
			out.folds[len(out.folds)-1].end = len(out.lines)
		}
	}
	if c.running() && liveID == "" {
		starting := styleDim.Render(m.teammateName() + " is starting…")
		if !m.look.plain {
			starting = m.pal.running.Render(m.spinner()) + " " + starting
		}
		out.lines = append(out.lines, "", starting)
	}
	// The cursor's folded item is marked on its first line.
	if c.fold >= 0 && c.fold < len(out.folds) {
		f := out.folds[c.fold]
		out.lines[f.line] = styleAccent.Render("›") + strings.TrimPrefix(out.lines[f.line], " ")
	}
	return out
}

// laidItem is an item of the transcript laid out, with what it was laid out from.
type laidItem struct {
	key   laidKey
	lines []string
}

// laidKey is what an item's lines are drawn from, besides the item: the palette follows the look.
type laidKey struct {
	rev  int64
	w    int
	look look
	open bool
	name string
}

// itemLines is an item of the transcript as lines of w cells. A long chat would otherwise be
// laid out again for every frame and key, so an item's lines are kept until it or its drawing
// changes. The live item, a tool call at work and the ask waiting on the user are drawn
// fresh: their spinners turn and their buttons follow the pointer.
func (m *app) itemLines(it chat.Item, live, ask bool, w int) []string {
	c := &m.chats
	fresh := live || ask || it.Status == chat.StatusPending || (it.Tool != nil && toolStatus(it.Tool.Status) == "working")
	key := laidKey{rev: it.Rev, w: w, look: m.look, open: c.expanded[it.ID], name: m.teammateName()}
	if !fresh {
		if l, ok := c.laid[it.ID]; ok && l.key == key {
			return l.lines
		}
	}
	lines := m.layItem(it, live, ask, w)
	if !fresh {
		if c.laid == nil {
			c.laid = map[string]laidItem{}
		}
		c.laid[it.ID] = laidItem{key: key, lines: lines}
	}
	return lines
}

// layItem lays an item of the transcript out in w cells.
func (m *app) layItem(it chat.Item, live, ask bool, w int) []string {
	c := &m.chats
	var lines []string
	switch it.Kind {
	case chat.KindUser:
		if it.User == nil {
			return nil
		}
		text := it.User.Text
		for _, a := range it.User.Attachments {
			text += "\n[" + a.Name + "]"
		}
		for i, line := range wrapText(text, w-2) {
			mark := "  "
			switch {
			case !m.look.plain:
				mark = m.pal.hue(areaChat).Render("┃") + " " // the prompt's rule, as the web marks a turn
			case i == 0:
				mark = styleAccent.Render("›") + " "
			}
			lines = append(lines, mark+styleBold.Render(line))
		}
	case chat.KindAgent:
		if it.Text == nil {
			return nil
		}
		lines = append(lines, c.markdown(it, w, !m.look.plain)...)
		if it.Text.Truncated {
			lines = append(lines, styleDim.Render("The rest of this message was too long to keep."))
		}
	case chat.KindThought, chat.KindTool:
		lines = m.foldLines(it, live, w)
	case chat.KindPlan:
		if m.look.plain {
			lines = append(lines, planLines(it, w)...)
		} else {
			lines = append(lines, m.checklist(it, w)...)
		}
	case chat.KindPermission:
		lines = append(lines, permissionLine(it, w))
		switch {
		case ask && m.buttonAt(askOwner(it)) >= 0:
			row, _ := m.buttonsLine(askCaps(it), m.buttonAt(askOwner(it)))
			lines = append(lines, "    "+row)
		case ask && it.Permission != nil:
			lines = append(lines, "    "+picksText(permissionPicks(it.Permission.Options))+styleDim.Render("  i ignore"))
		}
	case chat.KindQuestion:
		lines = append(lines, m.questionLines(it, ask, w)...)
	case chat.KindMarker:
		lines = append(lines, markerLine(it, w))
	}
	return lines
}

// teammateName is the name of the teammate of the chat on show.
func (m *app) teammateName() string {
	if name := m.teammates[m.chats.sandbox.TeammateID]; name != "" {
		return name
	}
	return "The teammate"
}

// toolLabels name what each kind of tool call does, as the web does.
var toolLabels = map[string]string{
	"read": "Read", "edit": "Edit", "delete": "Delete", "move": "Move", "search": "Search",
	"execute": "Run a command", "think": "Think", "fetch": "Fetch", "switch_mode": "Switch mode",
}

// toolGlyphs mark each kind of tool call in the visual look.
var toolGlyphs = map[string]string{
	"read": "≡", "edit": "✎", "delete": "×", "move": "→", "search": "⌕",
	"execute": "▶", "think": "…", "fetch": "↯", "switch_mode": "⇄",
}

// toolDot is a tool call's state as a status dot: a spinner while it works.
func (m *app) toolDot(status string) string {
	switch status {
	case "working":
		return m.pal.running.Render(m.spinner())
	case "done":
		return m.pal.dot("running")
	case "failed":
		return m.pal.dot("failed")
	case "stopped":
		return m.pal.dot("stopped")
	}
	return m.pal.dot("waiting")
}

// toolStatus is a tool call's status as a word.
func toolStatus(status string) string {
	switch status {
	case "in_progress":
		return "working"
	case "completed":
		return "done"
	case "failed":
		return "failed"
	case "stopped":
		return "stopped"
	}
	return "waiting"
}

// maxToolLines bounds what one part of an open tool call shows.
const maxToolLines = 40

// foldLines is a tool call or a thought: one line, and its body under it once expanded.
func (m *app) foldLines(it chat.Item, live bool, w int) []string {
	open := m.chats.expanded[it.ID]
	mark := "▸"
	if open {
		mark = "▾"
	}
	if it.Kind == chat.KindThought {
		if it.Text == nil {
			return []string{"  " + styleDim.Render(mark+" Thought it through")}
		}
		title := "Thought it through"
		if live {
			title = "Thinking…"
		}
		lines := []string{"  " + styleDim.Render(mark+" "+title)}
		if live && !m.look.plain {
			lines[0] = "  " + styleDim.Render(mark+" ") + m.pal.running.Render(m.spinner()) + " " + styleDim.Render(title)
		}
		if open {
			for _, line := range wrapText(it.Text.Text, w-4) {
				lines = append(lines, "    "+styleDim.Render(line))
			}
		}
		return lines
	}
	tool := it.Tool
	if tool == nil {
		return []string{"  " + styleDim.Render(mark+" Tool")}
	}
	title := oneLine(tool.Title)
	if title == "" {
		title = firstNonEmpty(toolLabels[tool.Kind], "Tool")
	}
	status := toolStatus(tool.Status)
	statusCell := styleDim.Render(status)
	switch {
	case !m.look.plain:
		statusCell = m.toolDot(status) + " " + status
		title = firstNonEmpty(toolGlyphs[tool.Kind], "•") + " " + title
	case status == "failed":
		statusCell = styleFailed.Render(status)
	case status == "working":
		statusCell = styleAccent.Render(status)
	}
	lines := []string{spread("  "+mark+" "+title, statusCell, w)}
	if !open {
		return lines
	}
	body := toolBody(tool)
	if len(body) == 0 {
		body = []string{styleDim.Render("Nothing more to show.")}
	}
	for _, line := range body {
		lines = append(lines, "    "+line)
	}
	return lines
}

// toolBody is what an open tool call shows: the files it touched, its output and its changes.
func toolBody(tool *chat.ToolBody) []string {
	var lines []string
	for _, l := range tool.Locations {
		where := l.Path
		if l.Line != nil {
			where += fmt.Sprintf(":%d", *l.Line)
		}
		lines = append(lines, styleDim.Render(where))
	}
	for _, c := range tool.Content {
		switch c.Type {
		case "diff":
			lines = append(lines, styleBold.Render(c.Path))
			var diff []string
			if c.OldText != nil {
				for _, l := range strings.Split(*c.OldText, "\n") {
					diff = append(diff, styleFailed.Render("- "+l))
				}
			}
			for _, l := range strings.Split(c.NewText, "\n") {
				diff = append(diff, styleAdded.Render("+ "+l))
			}
			lines = append(lines, capLines(diff)...)
		case "terminal":
			lines = append(lines, styleDim.Render("Ran in the agent's own terminal."))
		default:
			if c.Content == nil {
				continue
			}
			switch c.Content.Type {
			case "text":
				lines = append(lines, capLines(strings.Split(strings.TrimRight(c.Content.Text, "\n"), "\n"))...)
			case "image":
				lines = append(lines, styleDim.Render("[an image]"))
			default:
				lines = append(lines, styleDim.Render("["+firstNonEmpty(c.Content.Name, c.Content.URI, c.Content.Type)+"]"))
			}
		}
	}
	if tool.Truncated {
		lines = append(lines, styleDim.Render("The rest was too long to keep."))
	}
	return lines
}

// capLines keeps the first maxToolLines lines and says how many more there are.
func capLines(lines []string) []string {
	if len(lines) <= maxToolLines {
		return lines
	}
	more := len(lines) - maxToolLines
	return append(lines[:maxToolLines:maxToolLines], styleDim.Render(fmt.Sprintf("… %d more %s", more, plural(more, "line", "lines"))))
}

// planLines is the agent's plan: its steps with their state.
func planLines(it chat.Item, w int) []string {
	if it.Plan == nil || len(it.Plan.Entries) == 0 {
		return nil
	}
	lines := []string{"  " + styleBold.Render("Plan")}
	for _, e := range it.Plan.Entries {
		mark := styleDim.Render("○")
		switch e.Status {
		case "completed":
			mark = styleAccent.Render("✓")
		case "in_progress":
			mark = styleAccent.Render("◐")
		}
		lines = append(lines, "    "+mark+" "+fit(oneLine(e.Content), w-6))
	}
	return lines
}

// checklist is the agent's plan in the visual look: its steps as boxes that fill in as the turn
// runs, and how many are done.
func (m *app) checklist(it chat.Item, w int) []string {
	if it.Plan == nil || len(it.Plan.Entries) == 0 {
		return nil
	}
	done := 0
	for _, e := range it.Plan.Entries {
		if e.Status == "completed" {
			done++
		}
	}
	lines := []string{"  " + styleBold.Render("Plan") + styleDim.Render(fmt.Sprintf("  %d of %d done", done, len(it.Plan.Entries)))}
	for _, e := range it.Plan.Entries {
		text := fit(oneLine(e.Content), w-6)
		switch e.Status {
		case "completed":
			lines = append(lines, "    "+m.pal.running.Render("☑")+" "+styleDim.Render(text))
		case "in_progress":
			lines = append(lines, "    "+styleAccent.Render("☐")+" "+styleBold.Render(text))
		default:
			lines = append(lines, "    "+styleDim.Render("☐")+" "+text)
		}
	}
	return lines
}

// questionLines are a question of the agent: what it asks, and for the one the keys answer, its
// choices or how to answer it.
func (m *app) questionLines(it chat.Item, keys bool, w int) []string {
	if it.Question == nil {
		return nil
	}
	if it.Status != chat.StatusPending {
		return []string{"  " + styleDim.Render(m.teammateName()+" asked: "+oneLine(it.Question.Message))}
	}
	var lines []string
	for i, l := range wrapText(m.teammateName()+" asks: "+it.Question.Message, w-4) {
		mark := "  "
		if i == 0 {
			mark = styleAccent.Render("?") + " "
		}
		lines = append(lines, "  "+mark+l)
	}
	if !keys {
		return lines
	}
	if at := m.buttonAt(askOwner(it)); at >= 0 {
		row, _ := m.buttonsLine(askCaps(it), at)
		return append(lines, "    "+row)
	}
	f, ok := questionForm(it.Question.Schema)
	switch {
	case !ok:
		return append(lines, "    "+styleDim.Render("A form: answer it in the browser.  i skip"))
	case len(f.picks) > 0:
		return append(lines, "    "+picksText(f.picks)+styleDim.Render("  i skip"))
	}
	return append(lines, "    "+styleBold.Render("r")+" answer"+styleDim.Render("  i skip"))
}

// permissionLine is a tool call the agent asks to run. The number keys under it decide the first
// one waiting, here or on the approvals screen.
func permissionLine(it chat.Item, w int) string {
	title := "a tool"
	if it.Permission != nil && it.Permission.ToolCall.Title != "" {
		title = oneLine(it.Permission.ToolCall.Title)
	}
	if it.Status == chat.StatusPending {
		return "  " + styleAccent.Render("?") + " Asks to run " + title + styleDim.Render(" · waiting for you")
	}
	outcome := "answered"
	if it.Permission != nil && it.Permission.Outcome != "" {
		outcome = it.Permission.Outcome
	}
	return "  " + styleDim.Render("Asked to run "+title+" · "+outcome)
}

// markerWords say why a turn ended, or what happened between turns.
var markerWords = map[string]string{
	chat.MarkerStopped: "Stopped", chat.MarkerInterrupted: "Interrupted", chat.MarkerError: "Something went wrong",
	chat.MarkerLimit: "Hit a limit", chat.MarkerNotice: "Notice", chat.MarkerCompaction: "Context compacted",
	chat.MarkerTerminal: "Continued in the terminal", chat.MarkerUndo: "Changes undone",
}

// markerLine is a line between items, such as why a turn ended.
func markerLine(it chat.Item, w int) string {
	if it.Marker == nil {
		return ""
	}
	text := firstNonEmpty(oneLine(it.Marker.Text), markerWords[it.Marker.Kind], it.Marker.Kind)
	line := "── " + text + " ──"
	if it.Marker.Kind == chat.MarkerError || it.Marker.Kind == chat.MarkerInterrupted {
		return styleFailed.Render(fit(line, w))
	}
	return styleDim.Render(fit(line, w))
}

// renderedText is a message drawn as markdown, for its text at a width and look.
type renderedText struct {
	text  string
	w     int
	ruled bool
	lines []string
}

// markdown draws an agent's message, once for each text, width and look: the transcript is
// laid out for every key and frame, so scrolling would otherwise draw every message again.
func (c *chatState) markdown(it chat.Item, w int, ruled bool) []string {
	if r, ok := c.rendered[it.ID]; ok && r.text == it.Text.Text && r.w == w && r.ruled == ruled {
		return r.lines
	}
	if c.rendered == nil {
		c.rendered = map[string]renderedText{}
	}
	lines := renderMarkdown(it.Text.Text, w, ruled)
	c.rendered[it.ID] = renderedText{text: it.Text.Text, w: w, ruled: ruled, lines: lines}
	return lines
}

// wrapText wraps text to w cells, keeping its line breaks. A line's leading spaces stay on
// the lines it wraps to, so list items keep their indent.
func wrapText(text string, w int) []string {
	w = max(w, 8)
	var out []string
	for _, line := range strings.Split(text, "\n") {
		line = strings.ReplaceAll(line, "\t", "    ")
		rest := strings.TrimLeft(line, " ")
		indent := line[:len(line)-len(rest)]
		if len(indent) > w/2 {
			indent = ""
		}
		// A list item's next lines start under its text.
		hang := ""
		if strings.HasPrefix(rest, "- ") || strings.HasPrefix(rest, "* ") {
			hang = "  "
		}
		first := true
		for _, l := range strings.Split(lipgloss.Wrap(rest, w-len(indent)-len(hang), ""), "\n") {
			if first {
				out, first = append(out, indent+l), false
				continue
			}
			out = append(out, indent+hang+l)
		}
	}
	return out
}

// oneLine joins the lines of s with spaces.
func oneLine(s string) string {
	return strings.Join(strings.Fields(s), " ")
}

// chatStateWords are a chat's states as the web words them.
var chatStateWords = map[string]string{
	store.ChatIdle: "Ready", store.ChatRunning: "Working", store.ChatTerminal: "In the terminal", store.ChatOrphaned: "Ended",
}

// chatTitle is a chat's title, or a stand-in until the agent names it.
func chatTitle(c store.Chat) string {
	return firstNonEmpty(oneLine(c.Title), "New chat")
}

// chatBody is the chat on show: its title and state, and the transcript's rows in view.
func (m *app) chatBody(w, h int) []string {
	c := &m.chats
	title := "New chat with " + m.teammateName()
	state := ""
	if c.id != "" {
		title = chatTitle(c.chat) + styleDim.Render(" · "+c.sandbox.Name)
		switch {
		case c.gone:
			state = styleFailed.Render("deleted")
		case c.running() && !m.look.plain:
			state = m.pal.running.Render(m.spinner()) + " " + chatStateWords[c.chat.State]
		case c.running():
			state = styleAccent.Render("◐ " + chatStateWords[c.chat.State])
		case c.loaded:
			state = styleDim.Render(chatStateWords[c.chat.State])
		}
	}
	lines := []string{spread(styleBold.Render(title), state, w)}
	box := m.composer(w)
	rows := max(h-1-len(box), 1)
	switch {
	case c.gone:
		return append(lines, "", "This chat was deleted. Press n to start a new one.")
	case c.id == "":
		return withComposer(append(lines, "", styleDim.Render("Write what "+m.teammateName()+" should do. Enter sends it and starts the chat.")), box, h)
	case !c.loaded:
		return append(lines, styleDim.Render("Loading…"))
	}
	t := m.transcript(w)
	if len(t.lines) == 0 {
		return withComposer(append(lines, "", styleDim.Render("Nothing here yet. Press p and say what "+m.teammateName()+" should do.")), box, h)
	}
	scroll := c.scroll
	if c.fold >= 0 {
		scroll = m.foldScroll(c.fold)
	}
	end := max(len(t.lines)-scroll, 0)
	start := max(end-rows, 0)
	for i, f := range t.folds {
		if f.line >= start && f.line < end {
			m.hitRow(len(lines)+f.line-start, 0, w, rowFold, i)
		}
	}
	return withComposer(m.withBanner(append(lines, t.lines[start:end]...), w), box, h)
}

// withBanner draws the approval waiting in the chat's sandbox over the transcript, under the
// title, as Home's pane does over a terminal. The chat's own permission requests are in the
// transcript instead.
func (m *app) withBanner(lines []string, w int) []string {
	c, ok := m.sandboxCard(m.chats.sandbox.ID)
	if !ok || w < 24 {
		return lines
	}
	typing := m.screen == screenChat || m.paneFocused()
	b := m.banner(c, m.chats.sandbox.ID, typing, w)
	lines = pad(lines, len(b)+1)
	copy(lines[1:], b)
	return lines
}

// chatBindings are the keys the key bar shows on the chat screen.
func (m *app) chatBindings() []key.Binding {
	c := &m.chats
	if c.composing {
		return []key.Binding{keySend, keyNewline, keyLeave}
	}
	var bindings []key.Binding
	if it, ok := c.pendingAsk(); ok {
		bindings = append(bindings, askBindings(it)...)
	}
	bindings = append(bindings, keyPrompt)
	if c.fold >= 0 {
		bindings = append(bindings, keyExpand)
	}
	bindings = append(bindings, keyTools, keyPageUp)
	if c.running() {
		bindings = append(bindings, keyStop)
	}
	return append(bindings, keyNewChat, keyBack, keyHelp)
}

// askOwner names the choices of a permission request or question for the arrows.
func askOwner(it chat.Item) string {
	return "ask:" + it.ID
}

// askCaps are the choices of the permission request or question the agent waits on, as the
// arrows move over them: its numbered picks, answering and ignoring or skipping it.
func askCaps(it chat.Item) []choiceCap {
	var caps []choiceCap
	numbered := func(picks []pick) {
		for i, p := range picks[:min(len(picks), 9)] {
			caps = append(caps, choiceCap{fmt.Sprint(i + 1), oneLine(p.label)})
		}
	}
	if it.Kind == chat.KindPermission && it.Permission != nil {
		numbered(permissionPicks(it.Permission.Options))
		return append(caps, choiceCap{"i", "Ignore"})
	}
	if it.Question == nil {
		return nil
	}
	f, ok := questionForm(it.Question.Schema)
	switch {
	case !ok:
	case len(f.picks) > 0:
		numbered(f.picks)
	default:
		caps = append(caps, choiceCap{"r", "Answer"})
	}
	return append(caps, choiceCap{"i", "Skip"})
}

// askBindings are the keys of the permission request or question the agent waits on.
func askBindings(it chat.Item) []key.Binding {
	if it.Kind == chat.KindPermission && it.Permission != nil {
		return []key.Binding{keyPick(len(permissionPicks(it.Permission.Options))), keyIgnore, keyLeft}
	}
	if it.Question == nil {
		return nil
	}
	f, ok := questionForm(it.Question.Schema)
	switch {
	case !ok:
		return []key.Binding{keySkip}
	case len(f.picks) > 0:
		return []key.Binding{keyPick(len(f.picks)), keySkip}
	}
	return []key.Binding{keyReply, keySkip}
}
