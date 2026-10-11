//go:build linux

package guest

import (
	"bytes"
	"fmt"
	"strings"
	"unicode/utf8"
)

// Line diffs for the Changes panel: Myers' algorithm on the lines between the common
// prefix and suffix, giving up past maxEdits (the middle then counts as replaced).

const (
	maxEdits    = 1000
	diffContext = 3
)

// edit is one line of a diff: ' ' kept, '-' removed, '+' added.
type edit struct {
	op   byte
	line string
}

// isText reports whether b looks like text: valid UTF-8 without NUL bytes.
func isText(b []byte) bool {
	return bytes.IndexByte(b, 0) < 0 && utf8.Valid(b)
}

func splitLines(b []byte) []string {
	if len(b) == 0 {
		return nil
	}
	s := strings.TrimSuffix(string(b), "\n")
	return strings.Split(s, "\n")
}

// diffLines returns the edits that turn a into b.
func diffLines(a, b []string) []edit {
	pre := 0
	for pre < len(a) && pre < len(b) && a[pre] == b[pre] {
		pre++
	}
	suf := 0
	for suf < len(a)-pre && suf < len(b)-pre && a[len(a)-1-suf] == b[len(b)-1-suf] {
		suf++
	}
	out := make([]edit, 0, len(a)+len(b))
	for _, l := range a[:pre] {
		out = append(out, edit{' ', l})
	}
	out = append(out, myers(a[pre:len(a)-suf], b[pre:len(b)-suf])...)
	for _, l := range a[len(a)-suf:] {
		out = append(out, edit{' ', l})
	}
	return out
}

func myers(a, b []string) []edit {
	n, m := len(a), len(b)
	replace := func() []edit {
		out := make([]edit, 0, n+m)
		for _, l := range a {
			out = append(out, edit{'-', l})
		}
		for _, l := range b {
			out = append(out, edit{'+', l})
		}
		return out
	}
	if n == 0 || m == 0 {
		return replace()
	}
	max := n + m
	if max > maxEdits {
		max = maxEdits
	}
	off := max + 1
	v := make([]int, 2*max+3)
	var trace [][]int
	found := -1
	for d := 0; d <= max && found < 0; d++ {
		snap := make([]int, 2*d+1)
		for k := -d; k <= d; k += 2 {
			var x int
			if k == -d || (k != d && v[off+k-1] < v[off+k+1]) {
				x = v[off+k+1]
			} else {
				x = v[off+k-1] + 1
			}
			y := x - k
			for x < n && y < m && a[x] == b[y] {
				x++
				y++
			}
			v[off+k] = x
			if x >= n && y >= m {
				found = d
			}
		}
		for k := -d; k <= d; k++ {
			snap[k+d] = v[off+k]
		}
		trace = append(trace, snap)
	}
	if found < 0 {
		return replace()
	}
	// Walk back through the trace.
	var rev []edit
	x, y := n, m
	for d := found; d > 0; d-- {
		prev := trace[d-1]
		get := func(k int) int { return prev[k+d-1] }
		k := x - y
		var pk int
		if k == -d || (k != d && get(k-1) < get(k+1)) {
			pk = k + 1
		} else {
			pk = k - 1
		}
		px := get(pk)
		py := px - pk
		for x > px && y > py {
			x--
			y--
			rev = append(rev, edit{' ', a[x]})
		}
		if x == px {
			y--
			rev = append(rev, edit{'+', b[y]})
		} else {
			x--
			rev = append(rev, edit{'-', a[x]})
		}
	}
	for x > 0 && y > 0 {
		x--
		y--
		rev = append(rev, edit{' ', a[x]})
	}
	out := make([]edit, len(rev))
	for i, e := range rev {
		out[len(rev)-1-i] = e
	}
	return out
}

// countEdits returns the lines added and removed.
func countEdits(edits []edit) (added, removed int) {
	for _, e := range edits {
		switch e.op {
		case '+':
			added++
		case '-':
			removed++
		}
	}
	return added, removed
}

// unified renders edits as unified diff hunks of path, cut after limit bytes.
func unified(path string, edits []edit, limit int) (string, bool) {
	var sb strings.Builder
	fmt.Fprintf(&sb, "--- a/%s\n+++ b/%s\n", path, path)
	type pos struct{ a, b int }
	at := make([]pos, len(edits)+1)
	for i, e := range edits {
		at[i+1] = at[i]
		if e.op != '+' {
			at[i+1].a++
		}
		if e.op != '-' {
			at[i+1].b++
		}
	}
	for i := 0; i < len(edits); {
		if edits[i].op == ' ' {
			i++
			continue
		}
		start := max(i-diffContext, 0)
		end := i
		for j := i; j < len(edits); j++ {
			if edits[j].op != ' ' {
				end = j + 1
			} else if j-end >= 2*diffContext {
				break
			}
		}
		end = min(end+diffContext, len(edits))
		fmt.Fprintf(&sb, "@@ -%d,%d +%d,%d @@\n", at[start].a+1, at[end].a-at[start].a, at[start].b+1, at[end].b-at[start].b)
		for _, e := range edits[start:end] {
			sb.WriteByte(e.op)
			sb.WriteString(e.line)
			sb.WriteByte('\n')
			if sb.Len() > limit {
				return sb.String()[:limit], true
			}
		}
		i = end
	}
	return sb.String(), false
}
