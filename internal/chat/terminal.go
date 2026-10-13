package chat

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strings"

	"github.com/lukaskoebe/sandbox-studio/internal/acp"
	"github.com/lukaskoebe/sandbox-studio/internal/agentproto"
	"github.com/lukaskoebe/sandbox-studio/internal/harness"
	"github.com/lukaskoebe/sandbox-studio/internal/store"
)

// A chat opened in the terminal (docs/design/chat-mode.md section 4): the agent's session
// is handed to its TUI in a tmux session tagged with the chat, and handed back with
// session/load, whose replay brings the TUI's turns into the chat.

// TerminalSession is the name of the tmux session a chat opens in the terminal.
func TerminalSession(chatID string) string { return "chat-" + chatID }

// sessionID is what Studio passes to a TUI's resume flag: the harnesses' IDs are like
// ses_…, UUIDs or thread IDs.
var sessionID = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._:-]{0,127}$`)

// OpenInTerminal hands an idle chat's session to its harness's TUI and returns the tmux
// session's name. The chat is read-only in state terminal until ReturnToChat. Opening a
// chat that is already in the terminal starts its TUI again if it has exited.
func (s *Service) OpenInTerminal(ctx context.Context, envID, chatID string, launch Launch) (string, error) {
	c, err := s.Store.Chat(ctx, envID, chatID)
	if err != nil {
		return "", err
	}
	if c.ACPSessionID == "" {
		return "", Conflict("the chat has no agent session yet; send a message first")
	}
	if !sessionID.MatchString(c.ACPSessionID) {
		return "", Conflict("the agent's session ID can't be passed to its terminal")
	}
	if launch.Harness == nil || launch.Harness.Name() != c.Harness {
		return "", Conflict(fmt.Sprintf("the teammate no longer runs %s; start a new chat", c.Harness))
	}
	name := TerminalSession(c.ID)
	if c.State == store.ChatTerminal {
		running, err := s.Guest.Sessions(ctx, c.SandboxID)
		if err != nil {
			return "", err
		}
		for _, x := range running {
			if x.Name == name {
				return name, nil
			}
		}
		return name, s.startTUI(ctx, c, launch)
	}
	if c, err = s.lock(ctx, c, store.ChatTerminal); err != nil {
		return "", err
	}
	if l := s.get(c.ID); l != nil {
		l.shutdown(ctx)
	}
	if err = s.killTUI(ctx, c); err == nil {
		err = s.startTUI(ctx, c, launch)
	}
	if err != nil {
		s.unlock(c, store.ChatTerminal)
		return "", err
	}
	return name, nil
}

// startTUI writes the harness config and starts the TUI resuming the chat's session.
func (s *Service) startTUI(ctx context.Context, c store.Chat, launch Launch) error {
	if err := s.Guest.WriteHomeFiles(ctx, c.SandboxID, launch.Files); err != nil {
		return fmt.Errorf("writing the harness config: %w", err)
	}
	h := launch.Harness
	return s.Guest.StartSession(ctx, c.SandboxID, agentproto.StartSession{
		Name:    TerminalSession(c.ID),
		Harness: h.Name(),
		Command: h.TUICommand(harness.SessionOpts{Workdir: Workdir, Resume: c.ACPSessionID}),
		Env:     launch.TUIEnv,
		Chat:    c.ID,
	})
}

// killTUI ends the chat's tmux session if it runs.
func (s *Service) killTUI(ctx context.Context, c store.Chat) error {
	running, err := s.Guest.Sessions(ctx, c.SandboxID)
	if err != nil {
		return err
	}
	for _, x := range running {
		if x.Name == TerminalSession(c.ID) || x.Chat == c.ID {
			if err := s.Guest.KillSession(ctx, c.SandboxID, x.Name); err != nil {
				return err
			}
		}
	}
	return nil
}

// ReturnToChat ends the chat's TUI, reattaches the agent with session/load and adds the
// turns made in the terminal. A chat that isn't in the terminal is left as it is. If
// anything fails the chat stays in the terminal, so the user can try again.
func (s *Service) ReturnToChat(ctx context.Context, envID, chatID string, launch Launch) error {
	c, err := s.Store.Chat(ctx, envID, chatID)
	if err != nil {
		return err
	}
	if c.State != store.ChatTerminal {
		return nil
	}
	// running locks out prompts and a second return while the replay is reconciled.
	c, err = s.Store.SetChatState(ctx, envID, chatID, store.ChatRunning, store.ChatTerminal)
	if errors.Is(err, store.ErrConflict) {
		return nil
	}
	if err != nil {
		return err
	}
	s.changed(envID, chatID, true)
	back := func() {
		if _, err := s.Store.SetChatState(context.Background(), envID, chatID, store.ChatTerminal, store.ChatRunning); err == nil {
			s.changed(envID, chatID, true)
		}
	}
	if err := s.killTUI(ctx, c); err != nil {
		back()
		return err
	}
	if l := s.get(c.ID); l != nil {
		l.shutdown(ctx)
	}
	l, replay, err := s.startAgent(ctx, c, launch, true)
	if err != nil {
		back()
		return err
	}
	if err := s.reconcile(ctx, c, l, replay); err != nil {
		s.log().Warn("chat: reconcile the terminal's turns", "chat", c.ID, "err", err)
	}
	s.unlock(c, store.ChatRunning)
	return nil
}

// replayTurn is one prompt of a replay and what followed it.
type replayTurn struct {
	user    string
	entries []*entry
}

// splitReplay cuts a replay's entries into turns at each user message. Entries before
// the first one belong to the first turn.
func splitReplay(f *folder) []replayTurn {
	var out []replayTurn
	for _, e := range f.entries {
		if b, ok := e.body.(*UserBody); ok && e.kind == KindUser {
			out = append(out, replayTurn{user: b.Text})
		} else if len(out) == 0 {
			out = append(out, replayTurn{})
		}
		out[len(out)-1].entries = append(out[len(out)-1].entries, e)
	}
	return out
}

// reconcile adds the turns the replay has beyond the chat's own, with origin terminal.
// The replay's turns are matched to the chat's in order by their prompts; the chat's
// items of matched turns stay as they are, since they also hold what the replay lacks
// (permissions, questions, markers). From the first replay turn that doesn't match, every
// replay turn is new. If some of the chat's turns don't match, the TUI changed the
// history (/clear, /compact, a rewind) and undo is no longer offered for them. An agent
// without session/load can't replay: the chat gets a marker instead.
func (s *Service) reconcile(ctx context.Context, c store.Chat, l *live, replay *folder) error {
	if replay == nil {
		return s.terminalTurn(ctx, c, nil, "Continued in the terminal. This agent can't show those messages here.")
	}
	stored, err := s.Store.ChatTurns(ctx, c.ID)
	if err != nil {
		return err
	}
	items, err := s.Store.ChatItems(ctx, c.ID, "")
	if err != nil {
		return err
	}
	prompts := map[string]string{}
	for _, it := range items {
		if it.Kind != KindUser {
			continue
		}
		if _, ok := prompts[it.TurnID]; !ok {
			if v := ItemView(it); v.User != nil {
				prompts[it.TurnID] = v.User.Text
			}
		}
	}
	turns := splitReplay(replay)
	matched := 0
	for matched < len(stored) && matched < len(turns) && samePrompt(prompts[stored[matched].ID], turns[matched].user) {
		matched++
	}
	if matched < len(stored) {
		for _, t := range stored {
			if t.Snapshot != "" {
				if _, err := s.Store.SetChatTurnSnapshot(ctx, c.ID, t.ID, ""); err != nil {
					return err
				}
			}
		}
	}
	for i, rt := range turns[matched:] {
		marker := ""
		if i == 0 {
			marker = "Continued in the terminal"
		}
		if err := s.terminalTurn(ctx, c, rt.entries, marker); err != nil {
			return err
		}
	}
	s.retitle(c, replay.title)
	s.changed(c.EnvironmentID, c.ID, false)
	return nil
}

// terminalTurn stores a turn made in the TUI: a marker, if any, and the replay's entries.
func (s *Service) terminalTurn(ctx context.Context, c store.Chat, entries []*entry, marker string) error {
	t, err := s.Store.StartChatTurn(ctx, c.ID, store.OriginTerminal, "")
	if err != nil {
		return err
	}
	if marker != "" {
		if _, err := s.Store.AddChatItem(ctx, markerItem(t, MarkerTerminal, marker)); err != nil {
			return err
		}
	}
	for _, e := range entries {
		if err := s.save(t, e); err != nil {
			return err
		}
	}
	_, err = s.Store.EndChatTurn(ctx, c.ID, t.ID, acp.StopEndTurn, nil)
	return err
}

// samePrompt reports whether a replayed prompt is the one Studio sent: the replay may
// carry more (the undo note, attachments as text), so the stored prompt need only be in
// it. Whitespace doesn't count.
func samePrompt(stored, replayed string) bool {
	norm := func(s string) string { return strings.Join(strings.Fields(s), " ") }
	return strings.Contains(norm(replayed), norm(stored))
}
