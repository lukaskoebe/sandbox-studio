package tui

import (
	"strings"
	"unicode"

	"charm.land/lipgloss/v2"
)

// maxWordTokens bounds the words of a line the word diff compares; longer lines keep their
// line's color only.
const maxWordTokens = 300

// wordTokens splits a line into words, runs of spaces and single other characters, so a diff of
// two lines marks whole words.
func wordTokens(s string) []string {
	var out []string
	runes := []rune(s)
	for i := 0; i < len(runes); {
		j := i + 1
		switch r := runes[i]; {
		case isWordRune(r):
			for j < len(runes) && isWordRune(runes[j]) {
				j++
			}
		case unicode.IsSpace(r):
			for j < len(runes) && unicode.IsSpace(runes[j]) {
				j++
			}
		}
		out = append(out, string(runes[i:j]))
		i = j
	}
	return out
}

func isWordRune(r rune) bool { return r == '_' || unicode.IsLetter(r) || unicode.IsDigit(r) }

// wordDiff compares a removed line with the added line paired with it and marks, on each side,
// the words that are not in their longest common sequence. It marks nothing when the lines share
// no word, since then the whole line changed.
func wordDiff(old, new string) (oldChanged, newChanged []bool, oldToks, newToks []string) {
	oldToks, newToks = wordTokens(old), wordTokens(new)
	if len(oldToks) > maxWordTokens || len(newToks) > maxWordTokens {
		return nil, nil, oldToks, newToks
	}
	// lcs[i][j] is the length of the longest common sequence of oldToks[i:] and newToks[j:].
	lcs := make([][]int, len(oldToks)+1)
	for i := range lcs {
		lcs[i] = make([]int, len(newToks)+1)
	}
	for i := len(oldToks) - 1; i >= 0; i-- {
		for j := len(newToks) - 1; j >= 0; j-- {
			if oldToks[i] == newToks[j] {
				lcs[i][j] = lcs[i+1][j+1] + 1
			} else {
				lcs[i][j] = max(lcs[i+1][j], lcs[i][j+1])
			}
		}
	}
	oldChanged, newChanged = make([]bool, len(oldToks)), make([]bool, len(newToks))
	shared := false
	i, j := 0, 0
	for i < len(oldToks) && j < len(newToks) {
		switch {
		case oldToks[i] == newToks[j]:
			shared = shared || strings.TrimSpace(oldToks[i]) != ""
			i++
			j++
		case lcs[i+1][j] >= lcs[i][j+1]:
			oldChanged[i] = true
			i++
		default:
			newChanged[j] = true
			j++
		}
	}
	for ; i < len(oldToks); i++ {
		oldChanged[i] = true
	}
	for ; j < len(newToks); j++ {
		newChanged[j] = true
	}
	if !shared {
		return nil, nil, oldToks, newToks
	}
	return oldChanged, newChanged, oldToks, newToks
}

// markWords draws a changed line in style with its changed words in reverse video. Spaces at
// either end of a changed run stay plain, so the highlight sits on the words.
func markWords(toks []string, changed []bool, style lipgloss.Style) string {
	if changed == nil {
		return style.Render(strings.Join(toks, ""))
	}
	marked := style.Reverse(true)
	var b strings.Builder
	for i := 0; i < len(toks); {
		j := i + 1
		for j < len(toks) && changed[j] == changed[i] {
			j++
		}
		run := strings.Join(toks[i:j], "")
		if !changed[i] {
			b.WriteString(style.Render(run))
		} else {
			core := strings.TrimSpace(run)
			lead := run[:strings.Index(run, core)]
			trail := run[len(lead)+len(core):]
			if lead != "" {
				b.WriteString(style.Render(lead))
			}
			if core != "" {
				b.WriteString(marked.Render(core))
			}
			if trail != "" {
				b.WriteString(style.Render(trail))
			}
		}
		i = j
	}
	return b.String()
}

// pairedLines draws the removed and added lines of a run, each pair with its changed words
// marked; lines without a partner keep their line's color only.
func pairedLines(dels, adds []string, removed, added lipgloss.Style) (left, right []string) {
	for i := range max(len(dels), len(adds)) {
		switch {
		case i < len(dels) && i < len(adds):
			oc, nc, ot, nt := wordDiff(dels[i], adds[i])
			left = append(left, markWords(ot, oc, removed))
			right = append(right, markWords(nt, nc, added))
		case i < len(dels):
			left = append(left, removed.Render(dels[i]))
		default:
			right = append(right, added.Render(adds[i]))
		}
	}
	return left, right
}
