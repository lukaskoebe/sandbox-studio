package tui

import (
	"strings"
	"testing"
)

func TestWordDiffMarksTheChangedWords(t *testing.T) {
	changed := func(toks []string, marks []bool) string {
		var out []string
		for i, tok := range toks {
			if marks[i] {
				out = append(out, tok)
			}
		}
		return strings.Join(out, "|")
	}
	oc, nc, ot, nt := wordDiff("token := r.URL.Query().Get(\"token\")", "token, err := readToken(r)")
	if oc == nil || nc == nil {
		t.Fatal("lines sharing words got no marks")
	}
	if got := changed(nt, nc); got != ",|err| |readToken|r" {
		t.Errorf("added side marks %q", got)
	}
	if got := changed(ot, oc); !strings.HasPrefix(got, "r|.|URL") {
		t.Errorf("removed side marks %q; the shared first word and := stay plain", got)
	}

	if oc, nc, _, _ := wordDiff("alpha beta", "gamma delta"); oc != nil || nc != nil {
		t.Error("lines sharing no word got marks; the whole line changed")
	}
}
