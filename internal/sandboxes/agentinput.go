package sandboxes

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strings"

	"github.com/lukaskoebe/sandbox-studio/internal/agentproto"
)

// ErrNotAgentTerminal means text was sent to a terminal that runs no agent. Text typed into
// a shell would run as commands, so only terminals where an agent runs take it.
var ErrNotAgentTerminal = errors.New("the terminal runs no agent; send to an agent's terminal or a chat")

// ErrAgentLeft means the agent that ran in a terminal by hand was gone, or busy with a
// program of its own, by the time the text reached it; nothing was submitted.
var ErrAgentLeft = errors.New("the agent in that terminal is no longer at its prompt; nothing was sent")

// safeName is a program or session name tmux's command strings can carry as it is.
var safeName = regexp.MustCompile(`^[A-Za-z0-9._+-]{1,64}$`)

// SendToAgent types text into an agent's terminal and submits it: the tmux session of a
// claude, codex or opencode that Studio started, or one where the user started one by hand.
// The text is pasted as one bracketed paste, so its lines reach the agent as one message,
// then Enter submits it. In a terminal of the user's, tmux pastes and presses Enter only
// while the program in the foreground is still the one Studio saw, so text never lands in
// the shell the agent ran from.
func (m *Manager) SendToAgent(ctx context.Context, envID, id, terminal, text string) error {
	sessions, err := m.Terminals(ctx, envID, id)
	if err != nil {
		return err
	}
	var t *Terminal
	for i := range sessions {
		if sessions[i].Name == terminal {
			t = &sessions[i]
		}
	}
	if t == nil || !t.Agent {
		return ErrNotAgentTerminal
	}
	// The guard names the session and the program in tmux's own command strings.
	byHand := t.Harness == ""
	if byHand && (!safeName.MatchString(t.Command) || !safeName.MatchString(terminal)) {
		return ErrNotAgentTerminal
	}
	const buffer = "studio-input"
	paste := []string{"tmux", "paste-buffer", "-p", "-d", "-b", buffer, "-t", terminal}
	enter := []string{"tmux", "send-keys", "-t", terminal, "Enter"}
	if byHand {
		still := "#{==:#{pane_current_command}," + t.Command + "}"
		paste = []string{"tmux", "if-shell", "-F", "-t", terminal, still, strings.Join(paste[1:], " "), "delete-buffer -b " + buffer}
		enter = []string{"tmux", "if-shell", "-F", "-t", terminal, still, strings.Join(enter[1:], " ")}
	}
	// The calls go one by one, so the agent has read the paste before the Enter arrives.
	for _, req := range []agentproto.RunRequest{
		{Args: []string{"tmux", "load-buffer", "-b", buffer, "-"}, Stdin: text},
		{Args: paste},
		{Args: enter},
	} {
		req.TimeoutSec = 10
		res, err := m.Hub.Run(ctx, id, req)
		if err != nil {
			return err
		}
		if res.ExitCode != 0 {
			return fmt.Errorf("%s: %s", strings.Join(req.Args[:2], " "), strings.TrimSpace(res.Stderr))
		}
	}
	if byHand {
		// tmux skipped the paste or the Enter if the agent had left: say so rather than
		// report the feedback sent.
		after, err := m.Terminals(ctx, envID, id)
		if err != nil {
			return err
		}
		for _, s := range after {
			if s.Name == terminal && s.Command == t.Command {
				return nil
			}
		}
		return ErrAgentLeft
	}
	return nil
}
