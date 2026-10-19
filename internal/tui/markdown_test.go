package tui

import (
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"

	"github.com/lukaskoebe/sandbox-studio/internal/chat"
)

// plainMarkdown is renderMarkdown in the plain look, without its styles.
func plainMarkdown(text string, w int) []string {
	lines := renderMarkdown(text, w, false)
	for i, l := range lines {
		lines[i] = stripANSI(l)
	}
	return lines
}

func sameLines(t *testing.T, got, want []string) {
	t.Helper()
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Fatalf("got\n%s\nwant\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
}

func TestMarkdownTableFitsItsColumns(t *testing.T) {
	got := plainMarkdown("| Name | Size |\n|:--|--:|\n| `a|b` | 12 |\n| **long** | 3 |", 40)
	sameLines(t, got, []string{
		"┌───────┬──────┐",
		"│ Name  │ Size │",
		"├───────┼──────┤",
		"│ `a|b` │   12 │",
		"│ long  │    3 │",
		"└───────┴──────┘",
	})
}

func TestMarkdownTableWrapsCellsToTheWidth(t *testing.T) {
	got := plainMarkdown("| Step | What happens |\n|---|---|\n| 1 | the agent opens the page and reads it |", 30)
	for _, l := range got {
		if w := ansi.StringWidth(l); w > 30 {
			t.Errorf("line %q is %d cells wide", l, w)
		}
	}
	if len(got) < 6 || !strings.HasPrefix(got[3], "│ 1    │ the agent") || !strings.HasPrefix(got[4], "│      │ ") {
		t.Fatalf("wrapped table:\n%s", strings.Join(got, "\n"))
	}
}

func TestMarkdownTableTooNarrowBecomesLines(t *testing.T) {
	got := plainMarkdown("| A | B | C | D | E |\n|---|---|---|---|---|\n| one two | three | four | five | six |", 20)
	if got[0] != "A: one two" || got[1] != "B: three" || got[4] != "E: six" {
		t.Fatalf("narrow table:\n%s", strings.Join(got, "\n"))
	}
}

func TestMarkdownListsNestAndHang(t *testing.T) {
	got := plainMarkdown("- one two three four five\n  - inner\n- two\n\n1. first\n2) second", 16)
	sameLines(t, got, []string{
		"• one two three",
		"  four five",
		"  ◦ inner",
		"• two",
		"",
		"1. first",
		"2) second",
	})
}

func TestMarkdownQuotesRulesAndBlankLines(t *testing.T) {
	got := plainMarkdown("Intro\n\n> quoted **text**\n> more\n\n---\nend", 20)
	sameLines(t, got, []string{"Intro", "", "> quoted text", "> more", "", strings.Repeat("─", 20), "end"})
	ruled := renderMarkdown("> q", 20, true)
	if stripANSI(ruled[0]) != "│ q" {
		t.Errorf("visual quote %q", stripANSI(ruled[0]))
	}
}

func TestMarkdownInline(t *testing.T) {
	r := md{ruled: true}
	cases := []struct {
		in   string
		want []mdSpan
	}{
		{"a **b** c", []mdSpan{{"a ", mdStyle{}}, {"b", mdStyle{bold: true}}, {" c", mdStyle{}}}},
		{"an *em* and _em_", []mdSpan{{"an ", mdStyle{}}, {"em", mdStyle{italic: true}}, {" and ", mdStyle{}}, {"em", mdStyle{italic: true}}}},
		{"`x **y**`", []mdSpan{{"x **y**", mdStyle{code: true}}}},
		{"snake_case_name and 2*3*4", []mdSpan{{"snake_case_name and 2*3*4", mdStyle{}}}},
		{"see https://x.dev/a_b_c.", []mdSpan{{"see https://x.dev/a_b_c.", mdStyle{}}}},
		{"[docs](https://x.dev)", []mdSpan{{"docs", mdStyle{}}, {" (https://x.dev)", mdStyle{dim: true}}}},
		{`\*not\*`, []mdSpan{{"*not*", mdStyle{}}}},
		{"**`code` bold**", []mdSpan{{"code", mdStyle{bold: true, code: true}}, {" bold", mdStyle{bold: true}}}},
	}
	for _, c := range cases {
		got := r.inline(c.in, mdStyle{})
		if len(got) != len(c.want) {
			t.Errorf("%q: %+v, want %+v", c.in, got, c.want)
			continue
		}
		for i := range got {
			if got[i] != c.want[i] {
				t.Errorf("%q: %+v, want %+v", c.in, got, c.want)
				break
			}
		}
	}
	// The plain look keeps the backticks, since it has no color for code.
	if got := (md{}).inline("`x`", mdStyle{}); got[0].text != "`x`" {
		t.Errorf("plain code span %q", got[0].text)
	}
}

func TestMarkdownWrapCutsALongWordAndKeepsBreaks(t *testing.T) {
	got := plainMarkdown("abcdefghijklmnopqrst\nnext line", 8)
	sameLines(t, got, []string{"abcdefgh", "ijklmnop", "qrst", "next", "line"})
}

// A message is drawn once for its text, width and look, so scrolling draws nothing again.
func TestChatDrawsAMessageOnce(t *testing.T) {
	var c chatState
	it := item("a1", 1, chat.KindAgent)
	it.Text = &chat.TextBody{Text: "**one**"}
	first := c.markdown(it, 40, true)
	if again := c.markdown(it, 40, true); &again[0] != &first[0] {
		t.Error("the same message at the same width was drawn again")
	}
	if wider := c.markdown(it, 60, true); &wider[0] == &first[0] {
		t.Error("a new width kept the old lines")
	}
	it.Text = &chat.TextBody{Text: "**two**"}
	if got := stripANSI(c.markdown(it, 60, true)[0]); got != "two" {
		t.Errorf("a streamed message kept its old text: %q", got)
	}
}
