package cli

import (
	"flag"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/lukaskoebe/sandbox-studio/internal/chat"
	"github.com/lukaskoebe/sandbox-studio/internal/store"
)

// maxPrompt bounds a prompt read from stdin; the API takes at most 10 MiB with attachments.
const maxPrompt = 1 << 20

type askOpts struct {
	chat  string
	quiet bool
}

func askFlags(fs *flag.FlagSet, c *call) {
	o := &askOpts{}
	c.extra = o
	fs.StringVar(&o.chat, "chat", "", "continue this chat instead of starting a new one")
	fs.BoolVar(&o.quiet, "quiet", false, "print the answer only: no tool calls on stderr")
}

// ask sends a prompt to the sandbox teammate's chat and prints the answer as it streams in. The
// chat is the same as one started in the web UI: it shows up there, with its tool calls and
// approvals. Approvals the turn waits for are named on stderr; answer them with `studio approve`
// or in the browser.
func (c *call) ask(rest []string) error {
	o := c.extra.(*askOpts)
	if len(rest) < 2 {
		return usagef("ask needs a sandbox and a prompt, or - to read the prompt from stdin")
	}
	prompt := strings.Join(rest[1:], " ")
	if prompt == "-" {
		b, err := io.ReadAll(io.LimitReader(c.opts.Stdin, maxPrompt+1))
		if err != nil {
			return err
		}
		if len(b) > maxPrompt {
			return usagef("the prompt is longer than %d KiB", maxPrompt>>10)
		}
		prompt = string(b)
	}
	if strings.TrimSpace(prompt) == "" {
		return usagef("the prompt is empty")
	}
	sb, err := c.sandbox(rest[0])
	if err != nil {
		return err
	}
	if sb.TeammateID == "" {
		return fmt.Errorf("%s has no teammate to ask; give it one in Studio", sb.Name)
	}

	chatID, turnID := o.chat, ""
	body := map[string]string{"text": prompt}
	if chatID == "" {
		p, err := c.path("/sandboxes/%s/chats", sb.ID)
		if err != nil {
			return err
		}
		var created store.Chat
		if err := c.client.Do(c.ctx, http.MethodPost, p, body, &created); err != nil {
			return err
		}
		chatID = created.ID
	} else {
		p, err := c.path("/chats/%s/prompt", chatID)
		if err != nil {
			return err
		}
		var turn chat.Turn
		if err := c.client.Do(c.ctx, http.MethodPost, p, body, &turn); err != nil {
			return err
		}
		turnID = turn.ID
	}
	if !c.json && !o.quiet {
		fmt.Fprintf(c.opts.Stderr, "chat %s\n", chatID)
	}
	return c.follow(chatID, turnID, o.quiet)
}

// stream is what follow has printed: the agent text per item, the items named on stderr, and
// whether stdout is in the middle of a line.
type stream struct {
	printed map[string]int
	seen    map[string]bool
	last    string // the agent item printed last
	open    bool
	quiet   bool
}

// follow polls the chat until the turn ends, printing the agent's text as it grows. Tool calls
// and what the turn waits for go to stderr, once each.
func (c *call) follow(chatID, turnID string, quiet bool) error {
	p, err := c.path("/chats/%s", chatID)
	if err != nil {
		return err
	}
	st := &stream{printed: map[string]int{}, seen: map[string]bool{}, quiet: quiet}
	for {
		var log chat.Log
		if err := c.client.Do(c.ctx, http.MethodGet, p, nil, &log); err != nil {
			return err
		}
		turn := currentTurn(log, turnID)
		if turn != nil {
			turnID = turn.ID
			for _, it := range log.Items {
				if it.TurnID == turnID {
					c.printItem(it, st)
				}
			}
		}
		if turn != nil && turn.EndedAt != nil {
			return c.finish(log, *turn, st)
		}
		if log.Chat.State == store.ChatOrphaned {
			return failure{status: ExitFailed, msg: "the chat's agent session is gone"}
		}
		select {
		case <-c.ctx.Done():
			c.endLine(st)
			fmt.Fprintln(c.opts.Stderr, "Stopped following; the turn goes on. Follow it in the browser, or stop it there.")
			return c.ctx.Err()
		case <-time.After(pollEvery):
		}
	}
}

// currentTurn is the turn with id, or the chat's last turn when the id isn't known yet.
func currentTurn(log chat.Log, id string) *chat.Turn {
	for i := len(log.Turns) - 1; i >= 0; i-- {
		if id == "" || log.Turns[i].ID == id {
			return &log.Turns[i]
		}
	}
	return nil
}

func (c *call) printItem(it chat.Item, st *stream) {
	switch it.Kind {
	case chat.KindAgent:
		if it.Text == nil || c.json {
			return
		}
		text := it.Text.Text
		n := st.printed[it.ID]
		if len(text) <= n {
			return
		}
		if n == 0 && st.last != "" {
			c.endLine(st)
			fmt.Fprintln(c.opts.Stdout)
		}
		chunk := text[n:]
		fmt.Fprint(c.opts.Stdout, clean(chunk))
		st.printed[it.ID] = len(text)
		st.last = it.ID
		st.open = !strings.HasSuffix(chunk, "\n")
	case chat.KindTool:
		if st.quiet || c.json || it.Tool == nil || st.seen[it.ID] {
			return
		}
		st.seen[it.ID] = true
		c.endLine(st)
		fmt.Fprintf(c.opts.Stderr, "› %s\n", clean(it.Tool.Title))
	case chat.KindPermission, chat.KindQuestion:
		if st.seen[it.ID] || it.Status != chat.StatusPending {
			return
		}
		st.seen[it.ID] = true
		c.endLine(st)
		what := "a question"
		if it.Kind == chat.KindPermission {
			what = "permission to run a tool"
			if it.ApprovalID != "" {
				what += " (studio approve " + it.ApprovalID + ")"
			}
		}
		fmt.Fprintf(c.opts.Stderr, "The teammate waits for %s. You can answer it in the browser too.\n", what)
	}
}

// endLine ends a line of agent text, so stderr output starts on a line of its own when both
// share a terminal.
func (c *call) endLine(st *stream) {
	if st.open {
		fmt.Fprintln(c.opts.Stdout)
		st.open = false
	}
}

// finish ends the output and reports how the turn ended: a turn that failed, was stopped or hit
// a limit exits with ExitFailed.
func (c *call) finish(log chat.Log, turn chat.Turn, st *stream) error {
	var answer []string
	var marker *chat.MarkerBody
	for _, it := range log.Items {
		if it.TurnID != turn.ID {
			continue
		}
		if it.Kind == chat.KindAgent && it.Text != nil {
			answer = append(answer, it.Text.Text)
		}
		if it.Kind == chat.KindMarker && it.Marker != nil {
			marker = it.Marker
		}
	}
	if c.json {
		out := map[string]any{"chat": log.Chat.ID, "turn": turn.ID, "answer": strings.Join(answer, "\n\n"), "stopReason": turn.StopReason}
		if marker != nil {
			out["marker"] = marker
		}
		if err := c.printJSON(out); err != nil {
			return err
		}
	}
	c.endLine(st)
	if marker != nil {
		switch marker.Kind {
		case chat.MarkerError, chat.MarkerStopped, chat.MarkerInterrupted, chat.MarkerLimit:
			msg := marker.Kind
			if marker.Text != "" {
				msg += ": " + marker.Text
			}
			return failure{status: ExitFailed, msg: "the turn ended with " + msg}
		}
	}
	return nil
}
