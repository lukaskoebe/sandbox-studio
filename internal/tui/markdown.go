package tui

import (
	"regexp"
	"strings"
	"sync"
	"unicode"
	"unicode/utf8"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
)

// An agent's answer is markdown, and renderMarkdown draws it the way the web's renderer
// (web/src/components/chat/markdown.tsx) reads it: headings, paragraphs, lists, quotes, code
// blocks, tables, rules, and inline code, bold, italics and links. Anything it doesn't know
// stays plain text. Blocks keep the blank lines written between them.

type mdKind int

const (
	mdPara mdKind = iota
	mdCode
	mdHeading
	mdList
	mdQuote
	mdRule
	mdTable
)

type mdAlign int

const (
	alignNone mdAlign = iota
	alignLeft
	alignCenter
	alignRight
)

type mdBlock struct {
	kind  mdKind
	lang  string // a code block's language
	text  string // a paragraph's, heading's, quote's or code block's text
	items []mdItem
	align []mdAlign
	head  []string
	rows  [][]string
	gap   bool // a blank line comes before it
}

// mdItem is a list item; level is how deep it is nested.
type mdItem struct {
	marker string // "-" for a bullet, else the item's number and its . or )
	level  int
	text   string
}

var (
	mdFence     = regexp.MustCompile("^\\s{0,3}(```+|~~~+)\\s*([\\w+-]*)")
	mdHeadingRe = regexp.MustCompile(`^\s{0,3}(#{1,6})\s+(.*?)\s*#*\s*$`)
	mdRuleRe    = regexp.MustCompile(`^\s{0,3}(?:(?:-\s*){3,}|(?:\*\s*){3,}|(?:_\s*){3,})$`)
	mdQuoteRe   = regexp.MustCompile(`^\s{0,3}>\s?`)
	mdListItem  = regexp.MustCompile(`^(\s*)(?:[-*+]|(\d{1,9})([.)]))\s+(.*)$`)
	mdMore      = regexp.MustCompile(`^\s{2,}\S`)
	// A table's delimiter row, as in GitHub's markdown: |---|:---:|, with at least one pipe.
	mdDelimiter = regexp.MustCompile(`^\s{0,3}\|?\s*:?-+:?\s*(\|\s*:?-+:?\s*)*\|?\s*$`)
	mdParaEnd   = regexp.MustCompile("^\\s{0,3}(```|~~~|#{1,6}\\s|>)")
)

func parseMarkdown(text string) []mdBlock {
	lines := strings.Split(strings.NewReplacer("\r\n", "\n", "\r", "\n").Replace(text), "\n")
	var out []mdBlock
	gap := false
	add := func(b mdBlock) {
		b.gap = gap && len(out) > 0
		out, gap = append(out, b), false
	}
	for i := 0; i < len(lines); {
		line := lines[i]
		if f := mdFence.FindStringSubmatch(line); f != nil {
			var body []string
			for i++; i < len(lines) && !strings.HasPrefix(strings.TrimLeft(lines[i], " \t"), f[1]); i++ {
				body = append(body, lines[i])
			}
			i++
			add(mdBlock{kind: mdCode, lang: f[2], text: strings.Join(body, "\n")})
			continue
		}
		if strings.TrimSpace(line) == "" {
			gap = true
			i++
			continue
		}
		if h := mdHeadingRe.FindStringSubmatch(line); h != nil {
			add(mdBlock{kind: mdHeading, text: h[2]})
			i++
			continue
		}
		if mdRuleRe.MatchString(line) {
			add(mdBlock{kind: mdRule})
			i++
			continue
		}
		if mdQuoteRe.MatchString(line) {
			var body []string
			for ; i < len(lines) && mdQuoteRe.MatchString(lines[i]); i++ {
				body = append(body, mdQuoteRe.ReplaceAllString(lines[i], ""))
			}
			add(mdBlock{kind: mdQuote, text: strings.Join(body, "\n")})
			continue
		}
		if tableAt(lines, i) {
			b := mdBlock{kind: mdTable, head: tableCells(line, true)}
			for _, d := range tableCells(lines[i+1], true) {
				b.align = append(b.align, alignOf(d))
			}
			for i += 2; i < len(lines) && strings.TrimSpace(lines[i]) != "" && strings.Contains(lines[i], "|"); i++ {
				cells := tableCells(lines[i], true)
				row := make([]string, len(b.head))
				copy(row, cells)
				b.rows = append(b.rows, row)
			}
			add(b)
			continue
		}
		if mdListItem.MatchString(line) {
			b := mdBlock{kind: mdList}
			var indents []int // the indent of each level open above
			for ; i < len(lines); i++ {
				m := mdListItem.FindStringSubmatch(lines[i])
				if m == nil || mdRuleRe.MatchString(lines[i]) {
					if mdMore.MatchString(lines[i]) && len(b.items) > 0 {
						// A continuation line belongs to the item above.
						b.items[len(b.items)-1].text += "\n" + strings.TrimSpace(lines[i])
						continue
					}
					break
				}
				indent := len(strings.ReplaceAll(m[1], "\t", "    "))
				for len(indents) > 1 && indent < indents[len(indents)-1] {
					indents = indents[:len(indents)-1]
				}
				if len(indents) == 0 || indent > indents[len(indents)-1]+1 {
					indents = append(indents, indent)
				}
				marker := "-"
				if m[2] != "" {
					marker = m[2] + m[3]
				}
				b.items = append(b.items, mdItem{marker: marker, level: len(indents) - 1, text: m[4]})
			}
			add(b)
			continue
		}
		var body []string
		for ; i < len(lines) && strings.TrimSpace(lines[i]) != "" && !mdParaEnd.MatchString(lines[i]) &&
			!mdListItem.MatchString(lines[i]) && !mdRuleRe.MatchString(lines[i]) && !tableAt(lines, i); i++ {
			body = append(body, lines[i])
		}
		add(mdBlock{kind: mdPara, text: strings.Join(body, "\n")})
	}
	return out
}

// tableAt reports whether lines[i] starts a table: a row with pipes over a delimiter row of as
// many cells.
func tableAt(lines []string, i int) bool {
	if i+1 >= len(lines) {
		return false
	}
	next := lines[i+1]
	return strings.Contains(lines[i], "|") && strings.Contains(next, "|") && mdDelimiter.MatchString(next) &&
		len(tableCells(lines[i], true)) == len(tableCells(next, true))
}

// tableCells splits a table row into its cells. \| is a pipe in a cell, and so is one inside a
// code span, which agents write for shell pipes; the outer pipes are optional.
func tableCells(line string, codeSpans bool) []string {
	s := strings.TrimSpace(line)
	s = strings.TrimPrefix(s, "|")
	if strings.HasSuffix(s, "|") && !strings.HasSuffix(s, `\|`) {
		s = s[:len(s)-1]
	}
	var out []string
	var cell strings.Builder
	ticks := 0 // the length of the open code span's backtick run
	for j := 0; j < len(s); j++ {
		switch c := s[j]; {
		case c == '\\' && j+1 < len(s) && s[j+1] == '|':
			cell.WriteByte('|')
			j++
		case c == '`' && codeSpans:
			n := runOf(s, j, '`')
			switch ticks {
			case 0:
				ticks = n
			case n:
				ticks = 0
			}
			cell.WriteString(s[j : j+n])
			j += n - 1
		case c == '|' && ticks == 0:
			out = append(out, strings.TrimSpace(cell.String()))
			cell.Reset()
		default:
			cell.WriteByte(c)
		}
	}
	out = append(out, strings.TrimSpace(cell.String()))
	if ticks != 0 && codeSpans {
		// A backtick that never closes isn't a code span: split at every pipe instead.
		return tableCells(line, false)
	}
	return out
}

func alignOf(delimiter string) mdAlign {
	left, right := strings.HasPrefix(delimiter, ":"), strings.HasSuffix(delimiter, ":")
	switch {
	case left && right:
		return alignCenter
	case right:
		return alignRight
	case left:
		return alignLeft
	}
	return alignNone
}

// runOf is how many c start s at i.
func runOf(s string, i int, c byte) int {
	n := 0
	for i+n < len(s) && s[i+n] == c {
		n++
	}
	return n
}

// md draws parsed markdown. ruled is the visual look: code blocks get a dim left rule and their
// language as a label, quotes a rule, and inline code the accent color; the plain look indents
// code blocks, marks quotes with > and keeps inline code's backticks.
type md struct{ ruled bool }

// renderMarkdown draws an agent's answer w cells wide.
func renderMarkdown(text string, w int, ruled bool) []string {
	return md{ruled}.blocks(parseMarkdown(strings.TrimRight(text, "\n")), max(w, 8))
}

func (r md) blocks(blocks []mdBlock, w int) []string {
	var lines []string
	for _, b := range blocks {
		if b.gap {
			lines = append(lines, "")
		}
		switch b.kind {
		case mdCode:
			lines = append(lines, r.code(b, w)...)
		case mdHeading:
			lines = append(lines, r.wrap(r.inline(b.text, mdStyle{bold: true}), w, "", "")...)
		case mdList:
			lines = append(lines, r.list(b.items, w)...)
		case mdQuote:
			mark := r.dim("> ")
			if r.ruled {
				mark = r.dim("│ ")
			}
			for _, l := range r.blocks(parseMarkdown(b.text), w-2) {
				lines = append(lines, mark+l)
			}
		case mdRule:
			lines = append(lines, r.dim(strings.Repeat("─", w)))
		case mdTable:
			lines = append(lines, r.table(b, w)...)
		default:
			lines = append(lines, r.wrap(r.inline(b.text, mdStyle{}), w, "", "")...)
		}
	}
	return lines
}

// code draws a code block as it is written, its tabs as four spaces.
func (r md) code(b mdBlock, w int) []string {
	var lines []string
	if r.ruled && b.lang != "" {
		lines = append(lines, r.dim("  │ "+b.lang))
	}
	for _, l := range strings.Split(b.text, "\n") {
		l = strings.ReplaceAll(l, "\t", "    ")
		if r.ruled {
			lines = append(lines, r.dim("  │ ")+l)
		} else {
			lines = append(lines, "    "+l)
		}
	}
	return lines
}

// list draws bullets as • (◦ nested), numbers as written, and hangs an item's next lines under
// its text.
func (r md) list(items []mdItem, w int) []string {
	var lines []string
	indents := []int{0} // where each level's items start
	for _, it := range items {
		indents = indents[:min(it.level+1, len(indents))]
		for len(indents) <= it.level {
			indents = append(indents, indents[len(indents)-1]+2)
		}
		indent := indents[it.level]
		marker := it.marker
		if marker == "-" {
			marker = "•"
			if it.level > 0 {
				marker = "◦"
			}
		}
		mark := strings.Repeat(" ", indent) + r.dim(marker) + " "
		hang := strings.Repeat(" ", indent+ansi.StringWidth(marker)+1)
		// A nested list starts under this item's text.
		indents = append(indents[:it.level+1], indent+ansi.StringWidth(marker)+1)
		lines = append(lines, r.wrap(r.inline(strings.ReplaceAll(it.text, "\n", " "), mdStyle{}), w, mark, hang)...)
	}
	return lines
}

// table draws a table in boxes, its columns as wide as their cells up to a fair share of w, and
// cells wrapping within them. Too narrow for its columns, each row becomes lines of
// "heading: cell".
func (r md) table(b mdBlock, w int) []string {
	n := len(b.head)
	cell := func(row []string, j int, st mdStyle) []mdSpan { return r.inline(row[j], st) }
	natural := make([]int, n)
	for j := range n {
		natural[j] = spansWidth(cell(b.head, j, mdStyle{bold: true}))
		for _, row := range b.rows {
			natural[j] = max(natural[j], spansWidth(cell(row, j, mdStyle{})))
		}
		natural[j] = max(natural[j], 1)
	}
	widths, ok := fitColumns(natural, w-3*n-1)
	if !ok {
		var lines []string
		for i, row := range b.rows {
			if i > 0 {
				lines = append(lines, "")
			}
			for j := range n {
				spans := append(cell(b.head, j, mdStyle{bold: true}), mdSpan{": ", mdStyle{bold: true}})
				lines = append(lines, r.wrap(append(spans, cell(row, j, mdStyle{})...), w, "", "  ")...)
			}
		}
		return lines
	}
	border := func(left, mid, right string) string {
		parts := make([]string, n)
		for j, cw := range widths {
			parts[j] = strings.Repeat("─", cw+2)
		}
		return r.dim(left + strings.Join(parts, mid) + right)
	}
	bar := r.dim("│")
	row := func(cells []string, st mdStyle) []string {
		cols := make([][]string, n)
		height := 1
		for j := range n {
			cols[j] = r.wrap(cell(cells, j, st), widths[j], "", "")
			height = max(height, len(cols[j]))
		}
		lines := make([]string, height)
		for k := range height {
			var sb strings.Builder
			sb.WriteString(bar)
			for j := range n {
				text := ""
				if k < len(cols[j]) {
					text = cols[j][k]
				}
				sb.WriteString(" " + alignCell(text, widths[j], b.align[j]) + " " + bar)
			}
			lines[k] = sb.String()
		}
		return lines
	}
	lines := []string{border("┌", "┬", "┐")}
	lines = append(lines, row(b.head, mdStyle{bold: true})...)
	lines = append(lines, border("├", "┼", "┤"))
	for _, cells := range b.rows {
		lines = append(lines, row(cells, mdStyle{})...)
	}
	return append(lines, border("└", "┴", "┘"))
}

// fitColumns shares avail cells among columns of the natural widths: a column that needs less
// than a fair share keeps its width, and the rest split what is left. ok is false when a column
// would get fewer than four cells.
func fitColumns(natural []int, avail int) (widths []int, ok bool) {
	widths = append([]int(nil), natural...)
	total := 0
	for _, n := range natural {
		total += n
	}
	if total <= avail {
		return widths, true
	}
	open := make([]bool, len(natural))
	for j := range open {
		open[j] = true
	}
	left, count := avail, len(natural)
	for changed := true; changed && count > 0; {
		changed = false
		for j, n := range natural {
			if open[j] && n <= left/count {
				open[j], left, count, changed = false, left-n, count-1, true
			}
		}
	}
	if count == 0 {
		return widths, true
	}
	share, extra := left/count, left%count
	if share < 4 {
		return nil, false
	}
	for j := range widths {
		if open[j] {
			widths[j] = share
			if extra > 0 {
				widths[j]++
				extra--
			}
		}
	}
	return widths, true
}

// alignCell pads a cell's line to w cells as its column aligns.
func alignCell(s string, w int, a mdAlign) string {
	gap := max(w-ansi.StringWidth(s), 0)
	switch a {
	case alignRight:
		return strings.Repeat(" ", gap) + s
	case alignCenter:
		return strings.Repeat(" ", gap/2) + s + strings.Repeat(" ", gap-gap/2)
	}
	return s + strings.Repeat(" ", gap)
}

// mdStyle is how a run of inline text looks.
type mdStyle struct{ bold, italic, code, dim bool }

type mdSpan struct {
	text string
	st   mdStyle
}

func spansWidth(spans []mdSpan) int {
	w := 0
	for _, s := range spans {
		w += ansi.StringWidth(s.text)
	}
	return w
}

// mdCodes holds each style's escape codes around a run, as Style.Render draws them: rendering
// every run with it is most of the cost of a message.
var mdCodes sync.Map // mdCodeKey → [2]string

type mdCodeKey struct {
	st    mdStyle
	ruled bool
}

func (r md) render(s mdSpan) string {
	if s.text == "" {
		return ""
	}
	key := mdCodeKey{s.st, r.ruled}
	codes, ok := mdCodes.Load(key)
	if !ok {
		before, after, _ := strings.Cut(r.style(s.st).Render("x"), "x")
		codes, _ = mdCodes.LoadOrStore(key, [2]string{before, after})
	}
	c := codes.([2]string)
	return c[0] + s.text + c[1]
}

// dim draws s as styleDim does.
func (r md) dim(s string) string { return r.render(mdSpan{s, mdStyle{dim: true}}) }

// style is the Lip Gloss style of a run.
func (r md) style(s mdStyle) lipgloss.Style {
	st := lipgloss.NewStyle()
	if s.bold {
		st = st.Bold(true)
	}
	if s.italic {
		st = st.Italic(true)
	}
	if s.dim {
		st = st.Faint(true)
	}
	if s.code && r.ruled {
		st = st.Foreground(lipgloss.Color("6"))
	}
	return st
}

// inline splits text into its runs: code spans first, so markup inside backticks stays
// literal, then bold, italics and links. A link shows its text and then, dim, its address.
func (r md) inline(s string, st mdStyle) []mdSpan {
	var out []mdSpan
	var lit strings.Builder
	flush := func() {
		if lit.Len() > 0 {
			out = append(out, mdSpan{lit.String(), st})
			lit.Reset()
		}
	}
	for i := 0; i < len(s); {
		c := s[i]
		if c == '\\' && i+1 < len(s) && strings.IndexByte("!\"#$%&'()*+,-./:;<=>?@[\\]^_`{|}~", s[i+1]) >= 0 {
			lit.WriteByte(s[i+1])
			i += 2
			continue
		}
		if c == '`' {
			n := runOf(s, i, '`')
			if j := closingTicks(s, i+n, n); j >= 0 {
				flush()
				code := s[i+n : j]
				if t := strings.TrimSpace(code); t != "" {
					code = t
				}
				if !r.ruled {
					code = s[i:i+n] + code + s[i:i+n]
				}
				cs := st
				cs.code = true
				out = append(out, mdSpan{code, cs})
				i = j + n
				continue
			}
			lit.WriteString(s[i : i+n])
			i += n
			continue
		}
		if (c == '*' || c == '_') && strings.HasPrefix(s[i+1:], string(c)) {
			if j := strings.Index(s[i+2:], s[i:i+2]); j > 0 {
				flush()
				bs := st
				bs.bold = true
				out = append(out, r.inline(s[i+2:i+2+j], bs)...)
				i += j + 4
				continue
			}
		}
		if c == '*' || c == '_' {
			if j := closingEmphasis(s, i); j > 0 {
				flush()
				es := st
				es.italic = true
				out = append(out, r.inline(s[i+1:j], es)...)
				i = j + 1
				continue
			}
		}
		if c == '[' {
			if text, url, n := linkAt(s[i:]); n > 0 {
				flush()
				out = append(out, r.inline(text, st)...)
				if url != text && (strings.HasPrefix(url, "http://") || strings.HasPrefix(url, "https://") || strings.HasPrefix(url, "mailto:")) {
					ds := st
					ds.dim = true
					out = append(out, mdSpan{" (" + url + ")", ds})
				}
				i += n
				continue
			}
		}
		if (c == 'h' && (strings.HasPrefix(s[i:], "http://") || strings.HasPrefix(s[i:], "https://"))) && !wordBefore(s, i) {
			// A bare address stays as it is, its underscores and stars too.
			n := strings.IndexFunc(s[i:], func(r rune) bool { return unicode.IsSpace(r) || strings.ContainsRune("<>()", r) })
			if n < 0 {
				n = len(s) - i
			}
			url := strings.TrimRight(s[i:i+n], ".,;:!?'\"")
			lit.WriteString(url)
			i += len(url)
			continue
		}
		lit.WriteByte(c)
		i++
	}
	flush()
	return out
}

// closingTicks is where a run of exactly n backticks starts in s from i, or -1.
func closingTicks(s string, i, n int) int {
	for i < len(s) {
		j := strings.IndexByte(s[i:], '`')
		if j < 0 {
			return -1
		}
		j += i
		if m := runOf(s, j, '`'); m == n {
			return j
		} else {
			i = j + m
		}
	}
	return -1
}

// closingEmphasis is where the * or _ at s[i] closes, or -1: it opens before a non-space and
// not inside a word, and closes after a non-space and not before a word, outside a ** or __.
func closingEmphasis(s string, i int) int {
	c := s[i]
	if i+1 >= len(s) || s[i+1] == ' ' || s[i+1] == '\t' || s[i+1] == '\n' || wordBefore(s, i) || (i > 0 && s[i-1] == c) {
		return -1
	}
	for k := i + 2; k < len(s); k++ {
		if s[k] != c {
			continue
		}
		if s[k-1] == ' ' || s[k-1] == '\t' || s[k-1] == '\n' || s[k-1] == c || (k+1 < len(s) && s[k+1] == c) || wordAt(s, k+1) {
			continue
		}
		return k
	}
	return -1
}

// linkAt reads a [text](url) at the start of s: its text, address and length, or n 0.
func linkAt(s string) (text, url string, n int) {
	k := strings.IndexByte(s, ']')
	if k < 2 || !strings.HasPrefix(s[k+1:], "(") {
		return "", "", 0
	}
	end := strings.IndexByte(s[k+2:], ')')
	if end < 1 || strings.ContainsAny(s[k+2:k+2+end], " \t\n") {
		return "", "", 0
	}
	return s[1:k], s[k+2 : k+2+end], k + 3 + end
}

// wordBefore reports whether a letter, digit or _ comes right before s[i].
func wordBefore(s string, i int) bool {
	r, n := utf8.DecodeLastRuneInString(s[:i])
	return n > 0 && isWordRune(r)
}

// wordAt reports whether a letter, digit or _ starts s at i.
func wordAt(s string, i int) bool {
	r, n := utf8.DecodeRuneInString(s[i:])
	return n > 0 && isWordRune(r)
}

// wrap lays runs out in lines of w cells, breaking at spaces and at the text's line breaks,
// and cutting a word too long for a line. Lines start with first, and then with rest.
func (r md) wrap(spans []mdSpan, w int, first, rest string) []string {
	var lines []string
	var line, word []mdSpan
	lineW, wordW := 0, 0
	space := false // a space comes before the next word
	var spaceSt mdStyle
	prefix := first
	avail := func() int { return max(w-ansi.StringWidth(prefix), 1) }
	emit := func() {
		lines = append(lines, prefix+r.join(line))
		line, lineW, prefix, space = nil, 0, rest, false
	}
	place := func() {
		if wordW == 0 {
			return
		}
		gap := 0
		if space && lineW > 0 {
			gap = 1
		}
		if lineW > 0 && lineW+gap+wordW > avail() {
			emit()
			gap = 0
		}
		if gap == 1 {
			line = append(line, mdSpan{" ", spaceSt})
			lineW++
		}
		if wordW <= avail()-lineW {
			line, lineW = append(line, word...), lineW+wordW
			word, wordW, space = nil, 0, false
			return
		}
		for _, piece := range word {
			for _, ch := range piece.text {
				cw := ansi.StringWidth(string(ch))
				if lineW > 0 && lineW+cw > avail() {
					emit()
				}
				line = append(line, mdSpan{string(ch), piece.st})
				lineW += cw
			}
		}
		word, wordW, space = nil, 0, false
	}
	for _, sp := range spans {
		start := 0
		for i, ch := range sp.text {
			if ch != ' ' && ch != '\t' && ch != '\n' {
				continue
			}
			if i > start {
				word = append(word, mdSpan{sp.text[start:i], sp.st})
				wordW += ansi.StringWidth(sp.text[start:i])
			}
			start = i + 1
			place()
			if ch == '\n' {
				emit()
				continue
			}
			space, spaceSt = true, sp.st
		}
		if start < len(sp.text) {
			word = append(word, mdSpan{sp.text[start:], sp.st})
			wordW += ansi.StringWidth(sp.text[start:])
		}
	}
	place()
	if lineW > 0 || len(lines) == 0 {
		emit()
	}
	return lines
}

// join draws a line's runs, those of one style together.
func (r md) join(spans []mdSpan) string {
	var sb, run strings.Builder
	var st mdStyle
	for i, s := range spans {
		if i > 0 && s.st != st {
			sb.WriteString(r.render(mdSpan{run.String(), st}))
			run.Reset()
		}
		st = s.st
		run.WriteString(s.text)
	}
	if run.Len() > 0 {
		sb.WriteString(r.render(mdSpan{run.String(), st}))
	}
	return sb.String()
}
