package sandboxes

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/lukaskoebe/sandbox-studio/internal/agentproto"
)

// ErrNotAgentTerminal means text was sent to a terminal that runs no agent. Text typed into
// a shell would run as commands, so only agent sessions Studio started take it.
var ErrNotAgentTerminal = errors.New("the terminal runs no agent; send to an agent's terminal or a chat")

// SendToAgent types text into an agent's terminal, the tmux session of a claude, codex or
// opencode that Studio started, and submits it. The text is pasted as one bracketed paste,
// so its lines reach the agent as one message, then Enter submits it.
func (m *Manager) SendToAgent(ctx context.Context, envID, id, terminal, text string) error {
	sessions, err := m.Terminals(ctx, envID, id)
	if err != nil {
		return err
	}
	agent := false
	for _, s := range sessions {
		agent = agent || (s.Name == terminal && s.Harness != "")
	}
	if !agent {
		return ErrNotAgentTerminal
	}
	const buffer = "studio-input"
	// The calls go one by one, so the agent has read the paste before the Enter arrives.
	for _, req := range []agentproto.RunRequest{
		{Args: []string{"tmux", "load-buffer", "-b", buffer, "-"}, Stdin: text},
		{Args: []string{"tmux", "paste-buffer", "-p", "-d", "-b", buffer, "-t", terminal}},
		{Args: []string{"tmux", "send-keys", "-t", terminal, "Enter"}},
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
	return nil
}
