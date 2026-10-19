package guest

import (
	"bytes"
	"regexp"
	"strconv"
	"time"
)

// leaveCopyMode is the key tmux.conf names User99 and binds to leaving copy mode, and doing
// nothing otherwise.
const leaveCopyMode = "\x1b[9999~"

// clickWindow is how long after a click every key typed leaves copy mode first: tmux takes two
// clicks for a double click, which selects a word, only once 300ms pass without a third.
const clickWindow = time.Second

var (
	sgrMouse = regexp.MustCompile(`\x1b\[<(\d+);\d+;\d+[Mm]`)
	focusIO  = regexp.MustCompile(`\x1b\[[IO]`)
)

// afterClick has typing after a mouse selection reach the program in the terminal, as in a
// desktop terminal. A selection keeps tmux in copy mode, so that it stays in view, and copy mode
// would take the keys; so the keys typed after a click with the left button go in behind
// leaveCopyMode: those within clickWindow, and the first after it. A lone Esc goes in as it is,
// so that it clears the selection as before.
type afterClick struct {
	at time.Time // of the last left-button event, while keys go in behind leaveCopyMode
}

// input is p, a terminal's input, with leaveCopyMode before the keys that need it.
func (c *afterClick) input(p []byte, now time.Time) []byte {
	if c.at.IsZero() && !bytes.Contains(p, []byte("\x1b[<")) {
		return p
	}
	var out []byte
	for len(p) > 0 {
		m := sgrMouse.FindSubmatchIndex(p)
		keys := p
		if m != nil {
			keys = p[:m[0]]
		}
		if !c.at.IsZero() && string(keys) != "\x1b" && len(focusIO.ReplaceAll(keys, nil)) > 0 {
			out = append(out, leaveCopyMode...)
			if now.Sub(c.at) > clickWindow {
				c.at = time.Time{}
			}
		}
		out = append(out, keys...)
		if m == nil {
			break
		}
		// The low bits are the button, 0 for the left one; 64 marks the wheel.
		if b, err := strconv.Atoi(string(p[m[2]:m[3]])); err == nil && b&3 == 0 && b&64 == 0 {
			c.at = now
		}
		out = append(out, p[m[0]:m[1]]...)
		p = p[m[1]:]
	}
	return out
}
