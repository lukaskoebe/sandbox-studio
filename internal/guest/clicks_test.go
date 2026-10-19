package guest

import (
	"testing"
	"time"
)

func TestKeysAfterAClickLeaveCopyMode(t *testing.T) {
	t0 := time.Date(2026, 10, 19, 12, 0, 0, 0, time.UTC)
	const (
		press   = "\x1b[<0;3;2M"
		release = "\x1b[<0;3;2m"
		L       = leaveCopyMode
	)
	var c afterClick
	steps := []struct {
		after time.Duration
		in    string
		want  string
	}{
		{0, "ls", "ls"},                       // no click yet
		{0, "\x1b[<64;3;2M", "\x1b[<64;3;2M"}, // the wheel is not a click
		{0, "x", "x"},
		{0, press + release + "ab", press + release + L + "ab"},                 // a click, then keys
		{300 * time.Millisecond, "c", L + "c"},                                  // still within clickWindow
		{2 * time.Second, "d", L + "d"},                                         // the first key after it
		{2 * time.Second, "e", "e"},                                             // and no more
		{2 * time.Second, "\x1b[<32;4;2M" + release, "\x1b[<32;4;2M" + release}, // a drag ends
		{2 * time.Second, "\x1b[I", "\x1b[I"},                                   // focus is not typing
		{2 * time.Second, "\x1b", "\x1b"},                                       // Esc clears the selection
		{4 * time.Second, "f", L + "f"},
		{4 * time.Second, "g", "g"},
		{4 * time.Second, "\x1b[<35;5;2M", "\x1b[<35;5;2M"}, // moving without a button
		{4 * time.Second, "h", "h"},
	}
	for i, s := range steps {
		if got := string(c.input([]byte(s.in), t0.Add(s.after))); got != s.want {
			t.Errorf("step %d: %q became %q, want %q", i, s.in, got, s.want)
		}
	}
}
