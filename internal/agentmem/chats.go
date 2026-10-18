package agentmem

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/lukaskoebe/sandbox-studio/internal/agenthook"
	"github.com/lukaskoebe/sandbox-studio/internal/chat"
	"github.com/lukaskoebe/sandbox-studio/internal/memory"
	"github.com/lukaskoebe/sandbox-studio/internal/store"
)

// Chats are extracted by Studio itself: their agents run over ACP, which never ends a session
// the way a terminal does, and Studio has the whole conversation already. A chat's new turns
// are read once it has stayed idle for ChatIdle; a cursor on the chat (memory_seq) marks what
// was read. Hooks from a chat's session still pack context and recall, but don't extract.
//
// The cursor moves once the turns are read, so what wasn't is read later: a sweep every
// chatSweep catches up on the chats whose time came while Studio was stopped or busy. A failed
// call is tried again after chatRetry, then four times as long, and given up after
// maxChatAttempts; a run over the daily budget waits for the next day, and one the host has no
// memory for, for the next sweep.
const (
	// ChatIdle is how long a chat stays idle before its new turns are extracted.
	ChatIdle = 3 * time.Minute
	// minChatDelta is the least conversation worth an extraction call; less waits for more,
	// or chatShortWait before the sweep looks again.
	minChatDelta  = 200
	chatShortWait = 6 * time.Hour
	// chatLookback bounds the chats the sweep reads: older ones are left as they are.
	chatLookback = 7 * 24 * time.Hour
	// chatSweep is how often the sweep runs, and chatSweepBatch the most chats it reads at once.
	chatSweep       = 15 * time.Minute
	chatSweepBatch  = 4
	chatRetry       = 15 * time.Minute
	maxChatAttempts = 3
	// maxChatItems is the most items one extraction loads: the newest, enough for maxTranscript.
	maxChatItems = 500
	maxToolLine  = 200
)

// ChatIdled schedules the extraction of a chat's new turns once it stays idle for ChatIdle;
// it is chat.Service.OnIdle. Each call moves the time out again.
func (s *Service) ChatIdled(envID, chatID string) {
	s.chatMu.Lock()
	defer s.chatMu.Unlock()
	if t, ok := s.chatTimers[chatID]; ok {
		t.Reset(s.chatIdle())
		return
	}
	s.chatTimers[chatID] = time.AfterFunc(s.chatIdle(), func() {
		s.chatMu.Lock()
		delete(s.chatTimers, chatID)
		s.chatMu.Unlock()
		select {
		case s.chatsDue <- chatDue{env: envID, chat: chatID}:
		default: // the sweep finds it
		}
	})
}

func (s *Service) chatIdle() time.Duration {
	if s.ChatIdle > 0 {
		return s.ChatIdle
	}
	return ChatIdle
}

type chatDue struct{ env, chat string }

// sweepChats lists the chats to catch up on, the oldest first: idle for ChatIdle with turns after
// their cursor, changed within chatLookback and not put off.
func (s *Service) sweepChats(ctx context.Context) []chatDue {
	now := s.now()
	list, err := s.Store.ChatsToRemember(ctx, now.Add(-chatLookback), now.Add(-s.chatIdle()), now, chatSweepBatch)
	if err != nil {
		s.Log.Warn("memory: listing chats to extract failed", "err", err)
		return nil
	}
	out := make([]chatDue, 0, len(list))
	for _, c := range list {
		out = append(out, chatDue{env: c.EnvironmentID, chat: c.ID})
	}
	return out
}

// extractChat extracts the chat's turns after its cursor, if it is still idle.
func (s *Service) extractChat(ctx context.Context, d chatDue) error {
	c, err := s.Store.Chat(ctx, d.env, d.chat)
	if errors.Is(err, store.ErrNotFound) {
		return nil
	}
	if err != nil {
		return err
	}
	if c.State != store.ChatIdle || c.Heartbeat || c.TaskID != "" || c.TeammateID == "" {
		return nil // a running chat is scheduled again when its turn ends
	}
	from, err := s.Store.ChatMemorySeq(ctx, c.ID)
	if err != nil {
		return err
	}
	items, err := s.Store.ChatItemsAfter(ctx, c.ID, from, maxChatItems)
	if err != nil || len(items) == 0 {
		return err
	}
	transcript := chatTranscript(items)
	last := items[len(items)-1].Seq
	if len(strings.TrimSpace(transcript)) < minChatDelta {
		if !hasUser(items) {
			return s.Store.SetChatMemorySeq(ctx, c.ID, last) // nothing the user said: nothing to wait for
		}
		return s.Store.PostponeChatMemory(ctx, c.ID, s.now().Add(chatShortWait))
	}
	cl, err := s.chatCaller(ctx, c)
	if errors.Is(err, ErrNoTeammate) || errors.Is(err, store.ErrNotFound) {
		return s.Store.SetChatMemorySeq(ctx, c.ID, last)
	}
	if err == nil {
		var se session
		if se, err = s.session(ctx, cl, chatSessionRef(c), ""); err == nil {
			err = s.extract(ctx, extractJob{c: cl, se: se, ev: agenthook.Event{Event: eventChatIdle, SessionID: se.ref, Transcript: transcript}})
		}
	}
	return s.settle(ctx, c.ID, last, err)
}

// settle moves the chat's cursor past last once its turns were read, or puts the chat off by
// what kept them from being read.
func (s *Service) settle(ctx context.Context, chatID string, last int, err error) error {
	if err != nil && errors.Is(ctx.Err(), context.Canceled) {
		return err // Studio stops: the sweep of its next start reads them
	}
	wctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second) // ctx may have timed out
	defer cancel()
	now := s.now()
	switch {
	case err == nil:
		return s.Store.SetChatMemorySeq(wctx, chatID, last)
	case errors.Is(err, errOverBudget):
		y, m, d := now.Date()
		return s.Store.PostponeChatMemory(wctx, chatID, time.Date(y, m, d+1, 0, 0, 0, 0, time.UTC))
	case errors.Is(err, ErrLowMemory):
		s.Log.Info("memory extraction of a chat waits for free memory", "chat", chatID)
		return s.Store.PostponeChatMemory(wctx, chatID, now.Add(chatSweep))
	}
	attempts, perr := s.Store.ChatMemoryFailed(wctx, chatID)
	if perr != nil {
		return errors.Join(err, perr)
	}
	if attempts >= maxChatAttempts {
		if perr := s.Store.SetChatMemorySeq(wctx, chatID, last); perr != nil {
			return errors.Join(err, perr)
		}
		return fmt.Errorf("given up after %d attempts: %w", attempts, err)
	}
	if perr := s.Store.PostponeChatMemory(wctx, chatID, now.Add(chatRetry<<(2*(attempts-1)))); perr != nil {
		return errors.Join(err, perr)
	}
	return fmt.Errorf("attempt %d of %d, tried again later: %w", attempts, maxChatAttempts, err)
}

// eventChatIdle names a chat extraction in the log, where hooks put their event.
const eventChatIdle = "ChatIdle"

// chatCaller is the chat's teammate in its sandbox. The chat's teammate counts, not the
// sandbox's current one: the facts are what that teammate was told.
func (s *Service) chatCaller(ctx context.Context, c store.Chat) (caller, error) {
	sb, err := s.Store.LookupSandbox(ctx, c.SandboxID)
	if err != nil {
		return caller{}, err
	}
	t, err := s.Store.Teammate(ctx, c.EnvironmentID, c.TeammateID)
	if errors.Is(err, store.ErrNotFound) {
		return caller{}, ErrNoTeammate
	}
	if err != nil {
		return caller{}, err
	}
	return caller{env: c.EnvironmentID, sandbox: sb, teammate: t, scope: memory.TeammateScope(t.ID)}, nil
}

// chatSessionRef is the memory session of a chat: its harness session, which its hooks name too.
func chatSessionRef(c store.Chat) string {
	if c.ACPSessionID != "" && len(c.ACPSessionID) <= agenthook.MaxSessionID {
		return c.ACPSessionID
	}
	return "chat-" + c.ID
}

// isChatSession reports whether a hooked session is one of the sandbox's chats.
func (s *Service) isChatSession(ctx context.Context, c caller, ref string) (bool, error) {
	_, err := s.Store.ChatBySession(ctx, c.sandbox.ID, ref)
	if errors.Is(err, store.ErrNotFound) {
		return false, nil
	}
	return err == nil, err
}

// injectedMemory is the context Studio added to a prompt, which is not extracted again.
var injectedMemory = regexp.MustCompile(`(?s)<studio-memory>.*?</studio-memory>`)

// chatTranscript condenses chat items the way the guest condenses a transcript file: "user:"
// and "assistant:" lines, continuation lines indented. Tool calls are kept as one line each,
// their title, so procedures that worked can be found; thoughts and tool output are not.
func chatTranscript(items []store.ChatItem) string {
	var b strings.Builder
	write := func(role, text string) {
		text = strings.TrimSpace(injectedMemory.ReplaceAllString(text, ""))
		if text != "" {
			b.WriteString(role + ": " + strings.ReplaceAll(text, "\n", "\n  ") + "\n")
		}
	}
	for _, raw := range items {
		it := chat.ItemView(raw)
		switch {
		case it.User != nil:
			write("user", it.User.Text)
		case it.Kind == chat.KindAgent && it.Text != nil:
			write("assistant", it.Text.Text)
		case it.Tool != nil && it.Tool.Title != "":
			status := ""
			if it.Tool.Status == "failed" {
				status = " (failed)"
			}
			write("assistant", fmt.Sprintf("[tool] %s%s", truncate(strings.Join(strings.Fields(it.Tool.Title), " "), maxToolLine), status))
		}
	}
	out := b.String()
	if len(out) > maxTranscript {
		out = out[len(out)-maxTranscript:]
		if i := strings.IndexByte(out, '\n'); i >= 0 {
			out = out[i+1:]
		}
	}
	return out
}

// maxTranscript is the most conversation one extraction reads, as for a hook's delta.
const maxTranscript = 64 << 10

func hasUser(items []store.ChatItem) bool {
	for _, it := range items {
		if it.Kind == chat.KindUser {
			return true
		}
	}
	return false
}
