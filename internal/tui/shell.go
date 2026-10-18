package tui

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"

	"golang.org/x/term"

	"github.com/lukaskoebe/sandbox-studio/internal/api"
)

// Shell shows a sandbox's terminal in this one, as `studio shell` does, until the user detaches
// with Ctrl-] or the sandbox ends the terminal. As in the TUI, an approval of the sandbox that
// comes in meanwhile names itself in the title and rings the bell; Ctrl-] then asks about it
// here instead of detaching, and the terminal is attached again once it is answered.
func Shell(ctx context.Context, c *Client, env, sandbox, name string, in io.Reader, out io.Writer) error {
	for {
		at := &attachment{ctx: ctx, client: c, env: env, sandbox: sandbox, name: name, in: in, out: out, watch: true}
		if err := at.Run(); err != nil {
			return err
		}
		at.mu.Lock()
		waiting, ended := at.waiting, at.ended
		at.mu.Unlock()
		if waiting == "" || ended {
			return nil
		}
		again, err := askApproval(ctx, c, waiting, in, out)
		if err != nil || !again {
			return err
		}
	}
}

// askApproval shows the approval that waits and reads one key for it: a network request is
// allowed once, allowed or denied, and any other kind is left to the TUI or the browser. again is
// whether to go back to the terminal; Ctrl-], q or Ctrl-C detach instead.
func askApproval(ctx context.Context, c *Client, id string, in io.Reader, out io.Writer) (again bool, err error) {
	rctx, cancel := requestContext(ctx)
	list, err := c.PendingApprovals(rctx)
	cancel()
	if err != nil {
		return false, err
	}
	var a *api.ApprovalView
	for i := range list {
		if list[i].ID == id {
			a = &list[i]
		}
	}
	if a == nil {
		return true, nil // answered meanwhile
	}
	// The sandbox's screen is left as it was; the terminal draws it again when it is attached.
	fmt.Fprint(out, "\x1b[0m\x1b[?25h\x1b[2J\x1b[H")
	network := isNetwork(*a)
	ch := defaultChoice(*a)
	if network {
		fmt.Fprintf(out, "▲ %s wants %s\r\n\r\n", firstNonEmpty(approvalSource(*a), "The sandbox"), hostOf(*a))
		allow := hostOf(*a)
		if pats := patterns(*a); ch.pattern < len(pats) {
			allow = pats[ch.pattern]
		}
		fmt.Fprintf(out, "  o  allow once\r\n  y  allow %s (%s)\r\n  n  deny\r\n", allow, scopeLabel(*a, scopeOf(*a, ch)))
	} else {
		fmt.Fprintf(out, "▲ %s waits\r\n\r\n", kindTitle(a.Kind))
		fmt.Fprint(out, "  Answer it in studio tui, on its card with a, or in the browser.\r\n")
	}
	fmt.Fprint(out, "\r\n  enter  back to the terminal\r\n  q      detach\r\n")

	key, err := readKey(in)
	if err != nil {
		return false, err
	}
	var d Decision
	var verb string
	switch {
	case key == detachKey || key == 'q' || key == 0x03:
		return false, nil
	case network && key == 'o':
		d, verb = decisionFor(*a, ch, "allow_once"), "Allowed "+hostOf(*a)+" once."
	case network && key == 'y':
		d, verb = decisionFor(*a, ch, "allow"), "Allowed "+hostOf(*a)+"."
	case network && key == 'n':
		d, verb = decisionFor(*a, ch, "deny"), "Denied "+hostOf(*a)+"."
	default:
		return true, nil
	}
	rctx, cancel = requestContext(ctx)
	defer cancel()
	if _, err := c.Decide(rctx, a.EnvironmentID, a.ID, d); err != nil && apiStatus(err) != http.StatusConflict {
		return false, fmt.Errorf("could not decide: %w", err)
	}
	fmt.Fprint(out, "\r\n"+verb+"\r\n")
	return true, nil
}

// readKey reads one key, the terminal raw while it waits.
func readKey(in io.Reader) (byte, error) {
	if fd, isTerm := terminalFD(in); isTerm {
		state, err := term.MakeRaw(fd)
		if err != nil {
			return 0, err
		}
		defer func() { _ = term.Restore(fd, state) }()
	}
	buf := make([]byte, 16)
	n, err := in.Read(buf)
	if n == 0 {
		return 0, err
	}
	return strings.ToLower(string(buf[:1]))[0], nil
}
