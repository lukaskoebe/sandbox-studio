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

// chatState is what the chat screens show: a sandbox's chats, and the chat on show with its
// transcript, kept current from the chat's stream.
type chatState struct {
	sandbox sandboxes.View // the sandbox whose chats these are
	back    screen         // where esc returns to from the list
	list    []store.Chat
	listed  bool // the list arrived
	cursor  int  // the selected chat of the list

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

	expanded map[string]bool // the folded items the user opened, by item ID
	fold     int             // the folded item under the cursor, among the foldable ones; -1 for none
	scroll   int             // rows the transcript is scrolled up from its end

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

// openChats shows the chats of a sandbox. Only a sandbox with a teammate has chats: the chat is
// with its teammate.
func (m *app) openChats(sb *sandboxes.View) tea.Cmd {
	if sb == nil {
		return nil
	}
	if sb.TeammateID == "" {
		m.setNotice(sb.Name+" has no teammate to chat with. Give it one in the browser.", true)
		return nil
	}
	m.stopChat()
	m.chats = chatState{sandbox: *sb, back: m.screen, fold: -1}
	m.screen = screenChats
	return m.loadChats()
}

// loadChats reads the chats of the sandbox on the chat screens.
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
	c.fold, c.scroll = -1, 0
	m.screen = screenChat
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
		log, err := client.Chat(rctx, env, id)
		return chatLoaded{gen: gen, log: log, err: err}
	}
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
		return gen == c.gen && m.screen == screenChat && c.id != ""
	}
	switch msg := msg.(type) {
	case chatsListed:
		if msg.sandbox != c.sandbox.ID {
			return nil, true
		}
		if msg.err != nil {
			m.fail(msg.err)
			return nil, true
		}
		prev := ""
		if c.cursor < len(c.list) {
			prev = c.list[c.cursor].ID
		}
		c.list, c.listed = msg.list, true
		c.cursor = min(c.cursor, max(len(c.list)-1, 0))
		for i, x := range c.list {
			if x.ID == prev {
				c.cursor = i
			}
		}
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
		}
		return m.followChat(), true
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
		if m.screen != screenChat || c.id != "" {
			return m.loadChats(), true
		}
		return tea.Batch(m.openChat(msg.chat.ID), m.loadChats()), true
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
		}
	case ev.Item != nil:
		if old, ok := c.items[ev.Item.ID]; !ok || old.Rev <= ev.Item.Rev {
			c.items[ev.Item.ID] = *ev.Item
		}
	}
	c.rev = max(c.rev, ev.Rev)
}

// sortedItems is the transcript in order.
func (c *chatState) sortedItems() []chat.Item {
	out := make([]chat.Item, 0, len(c.items))
	for _, it := range c.items {
		out = append(out, it)
	}
	slices.SortFunc(out, func(a, b chat.Item) int { return a.Seq - b.Seq })
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

// chatsKey answers the list of a sandbox's chats.
func (m *app) chatsKey(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	c := &m.chats
	switch {
	case key.Matches(msg, keyBack):
		m.screen = c.back
	case key.Matches(msg, keyUp):
		c.cursor = max(c.cursor-1, 0)
	case key.Matches(msg, keyDown):
		c.cursor = min(c.cursor+1, max(len(c.list)-1, 0))
	case key.Matches(msg, keyOpen):
		if c.cursor < len(c.list) {
			return m, m.openChat(c.list[c.cursor].ID)
		}
	case key.Matches(msg, keyNewChat):
		return m, m.openChat("")
	case key.Matches(msg, keyApprovals):
		m.openApprovals()
	case key.Matches(msg, keyHelp):
		m.openHelp()
	}
	return m, nil
}

// chatKey answers the transcript: the cursor moves between the folded items, and the keys write,
// stop and start chats.
func (m *app) chatKey(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	c := &m.chats
	if c.composing {
		return m.composeKey(msg)
	}
	if cmd, ok := m.askKey(msg); ok {
		return m, cmd
	}
	folds := len(m.transcript(m.width).folds)
	switch {
	case key.Matches(msg, keyBack):
		m.stopChat()
		m.screen = screenChats
		return m, m.loadChats()
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
		id := m.transcript(m.width).folds[c.fold].id
		c.expanded[id] = !c.expanded[id]
		c.scroll = m.foldScroll(c.fold)
	case key.Matches(msg, keyPrompt), key.Matches(msg, keyOpen):
		return m, m.startComposing()
	case key.Matches(msg, keyPageUp):
		m.scrollTranscript(-1)
	case key.Matches(msg, keyPageDn):
		m.scrollTranscript(1)
	case key.Matches(msg, keyTop):
		c.fold, c.scroll = -1, len(m.transcript(m.width).lines)
	case key.Matches(msg, keyBottom):
		c.fold, c.scroll = -1, 0
	case key.Matches(msg, keyStop):
		return m, m.stopTurn()
	case key.Matches(msg, keyNewChat):
		return m, m.openChat("")
	case key.Matches(msg, keyApprovals):
		m.openApprovals()
	case key.Matches(msg, keyHelp):
		m.openHelp()
	}
	return m, nil
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
			m.screen = screenChats
			return m, m.loadChats()
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
	total := len(m.transcript(m.width).lines)
	if c.fold >= 0 {
		c.scroll = m.foldScroll(c.fold)
		c.fold = -1
	}
	c.scroll = min(max(c.scroll-dir*max(rows-1, 1), 0), max(total-rows, 0))
}

// transcriptRows is how many rows the transcript gets: the body less the chat's title line.
func (m *app) transcriptRows() int {
	return max(m.height-5-1, 1)
}

// lastFoldInView is the last folded item the transcript shows, or the last one above the view.
func (m *app) lastFoldInView() int {
	t := m.transcript(m.width)
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
	t := m.transcript(m.width)
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
	prevKind := ""
	for _, it := range items {
		gap := prevKind != "" && (it.Kind == chat.KindUser || it.Kind == chat.KindAgent || prevKind == chat.KindUser || prevKind == chat.KindAgent)
		if gap {
			out.lines = append(out.lines, "")
		}
		prevKind = it.Kind
		switch it.Kind {
		case chat.KindUser:
			if it.User == nil {
				continue
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
				out.lines = append(out.lines, mark+styleBold.Render(line))
			}
		case chat.KindAgent:
			if it.Text == nil {
				continue
			}
			out.lines = append(out.lines, renderMarkdown(it.Text.Text, w, !m.look.plain)...)
			if it.Text.Truncated {
				out.lines = append(out.lines, styleDim.Render("The rest of this message was too long to keep."))
			}
		case chat.KindThought, chat.KindTool:
			out.addFold(m.foldLines(it, it.ID == liveID, w))
			out.folds[len(out.folds)-1].id = it.ID
		case chat.KindPlan:
			if m.look.plain {
				out.lines = append(out.lines, planLines(it, w)...)
			} else {
				out.lines = append(out.lines, m.checklist(it, w)...)
			}
		case chat.KindPermission:
			out.lines = append(out.lines, permissionLine(it, w))
			if it.ID == askID && it.Permission != nil {
				out.lines = append(out.lines, "    "+picksText(permissionPicks(it.Permission.Options))+styleDim.Render("  i ignore"))
			}
		case chat.KindQuestion:
			out.lines = append(out.lines, m.questionLines(it, it.ID == askID, w)...)
		case chat.KindMarker:
			out.lines = append(out.lines, markerLine(it, w))
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

// addFold appends a folded item's lines and records where they are.
func (t *transcriptView) addFold(lines []string) {
	t.folds = append(t.folds, folded{line: len(t.lines), end: len(t.lines) + len(lines)})
	t.lines = append(t.lines, lines...)
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

// markdownLines styles an agent's answer lightly, with no markdown renderer: headings are bold
// and code blocks are indented, as they come. The rest is wrapped to w.
func markdownLines(text string, w int) []string {
	return renderMarkdown(text, w, false)
}

// renderMarkdown is markdownLines; ruled gives code blocks the visual look's dim left rule, with
// the block's language as its label.
func renderMarkdown(text string, w int, ruled bool) []string {
	var lines []string
	code := false
	for _, line := range strings.Split(strings.TrimRight(text, "\n"), "\n") {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "```") || strings.HasPrefix(trimmed, "~~~") {
			code = !code
			if lang := strings.TrimSpace(strings.TrimLeft(trimmed, "`~")); code && ruled && lang != "" {
				lines = append(lines, styleDim.Render("  │ "+lang))
			}
			continue
		}
		switch {
		case code && ruled:
			lines = append(lines, styleDim.Render("  │ ")+strings.ReplaceAll(line, "\t", "    "))
		case code:
			lines = append(lines, "    "+strings.ReplaceAll(line, "\t", "    "))
		case isHeading(trimmed):
			for _, l := range wrapText(strings.TrimSpace(strings.TrimLeft(trimmed, "#")), w) {
				lines = append(lines, styleBold.Render(l))
			}
		default:
			lines = append(lines, wrapText(line, w)...)
		}
	}
	return lines
}

// isHeading reports whether a line is a markdown heading: one to six # and a space.
func isHeading(line string) bool {
	n := len(line) - len(strings.TrimLeft(line, "#"))
	return n >= 1 && n <= 6 && strings.HasPrefix(line[n:], " ")
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

// chatsBody is the list of the sandbox's chats, the newest activity first.
func (m *app) chatsBody(w, h int) []string {
	c := &m.chats
	head := styleBold.Render("Chats") + styleDim.Render(" in "+c.sandbox.Name+" with "+m.teammateName())
	switch {
	case !c.listed:
		return []string{head, "", styleDim.Render("Loading…")}
	case len(c.list) == 0:
		return []string{head, "", "No chats yet. Press n to start one with " + m.teammateName() + "."}
	}
	lines := []string{head, ""}
	offset := 0
	if c.cursor >= h-2 {
		offset = c.cursor - (h - 2) + 1
	}
	for i := offset; i < len(c.list); i++ {
		m.hitRow(len(lines), 0, w, rowChat, i)
		x := c.list[i]
		cursor, title := "  ", fit(chatTitle(x), max(w-2-colStatus-1-colUptime-1, 8))
		if i == c.cursor {
			cursor, title = m.cursorMark(true), styleBold.Render(title)
		}
		mark, style := "○", styleDim
		if x.State == store.ChatRunning {
			mark, style = "◐", styleAccent
		}
		state := style.Render(mark) + " " + fit(chatStateWords[x.State], colStatus-2)
		lines = append(lines, cursor+state+" "+title+" "+styleDim.Render(age(m.now.Sub(x.UpdatedAt))))
	}
	return lines
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
	rows := max(h-1, 1)
	switch {
	case c.gone:
		return append(lines, "", "This chat was deleted. Press n to start a new one.")
	case c.id == "":
		return append(lines, "", styleDim.Render("Write what "+m.teammateName()+" should do. Enter sends it and starts the chat."))
	case !c.loaded:
		return append(lines, styleDim.Render("Loading…"))
	}
	t := m.transcript(w)
	if len(t.lines) == 0 {
		return append(lines, "", styleDim.Render("Nothing here yet. Press p and say what "+m.teammateName()+" should do."))
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
	return append(lines, t.lines[start:end]...)
}

// chatBindings are the keys the key bar shows on the chat screens.
func (m *app) chatBindings() []key.Binding {
	c := &m.chats
	if m.screen == screenChats {
		if len(c.list) == 0 {
			return []key.Binding{keyNewChat, keyBack, keyHelp}
		}
		return []key.Binding{keyOpen, keyNewChat, keyUp, keyBack, keyHelp}
	}
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

// askBindings are the keys of the permission request or question the agent waits on.
func askBindings(it chat.Item) []key.Binding {
	if it.Kind == chat.KindPermission && it.Permission != nil {
		return []key.Binding{keyPick(len(permissionPicks(it.Permission.Options))), keyIgnore}
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
