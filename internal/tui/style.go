package tui

import (
	"fmt"
	"image/color"
	"strings"
	"time"

	"charm.land/bubbles/v2/help"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/colorprofile"
	"github.com/charmbracelet/x/ansi"
)

// The plain look is the terminal's own: its foreground and background, one accent for focus and
// status, and dim text for secondary facts. Colors are ANSI, so the user's palette applies, and
// Bubble Tea's renderer drops them when NO_COLOR is set. The visual look (docs/design/tui-visual.md)
// adds the palette below on top of these.
var (
	styleBold   = lipgloss.NewStyle().Bold(true)
	styleDim    = lipgloss.NewStyle().Faint(true)
	styleAccent = lipgloss.NewStyle().Foreground(lipgloss.Color("6"))
	styleFailed = lipgloss.NewStyle().Foreground(lipgloss.Color("1"))
	styleAdded  = lipgloss.NewStyle().Foreground(lipgloss.Color("2")) // added lines of a diff
)

// helpStyles draws the key bar in the same palette: keys in bold, their words dim.
func helpStyles() help.Styles {
	s := help.DefaultStyles(true)
	s.ShortKey, s.ShortDesc, s.ShortSeparator, s.Ellipsis = styleBold, styleDim, styleDim, styleDim
	s.FullKey, s.FullDesc, s.FullSeparator = styleBold, styleDim, styleDim
	return s
}

// fit makes s exactly w cells wide: padded with spaces, or cut with an ellipsis.
func fit(s string, w int) string {
	if w <= 0 {
		return ""
	}
	if lipgloss.Width(s) <= w {
		return s + strings.Repeat(" ", w-lipgloss.Width(s))
	}
	runes := []rune(s)
	for len(runes) > 0 && lipgloss.Width(string(runes))+1 > w {
		runes = runes[:len(runes)-1]
	}
	return string(runes) + "…" + strings.Repeat(" ", max(w-lipgloss.Width(string(runes))-1, 0))
}

// spread puts left at the start of a line of w cells and right at its end.
func spread(left, right string, w int) string {
	gap := w - lipgloss.Width(left) - lipgloss.Width(right)
	if gap < 1 {
		return fit(left, max(w-lipgloss.Width(right)-1, 0)) + " " + right
	}
	return left + strings.Repeat(" ", gap) + right
}

// pad makes a body exactly n lines long, cutting or filling with blank lines.
func pad(lines []string, n int) []string {
	if len(lines) > n {
		return lines[:n]
	}
	for len(lines) < n {
		lines = append(lines, "")
	}
	return lines
}

// uptime is how long a VM has been up, in the concept's words: "up 12m", "up 2h 14m", "up 3d 4h".
func uptime(d time.Duration) string {
	minutes := int(d / time.Minute)
	switch {
	case minutes < 1:
		return "up <1m"
	case minutes < 60:
		return fmt.Sprintf("up %dm", minutes)
	case minutes < 48*60:
		return fmt.Sprintf("up %dh %dm", minutes/60, minutes%60)
	default:
		return fmt.Sprintf("up %dd %dh", minutes/(24*60), minutes/60%24)
	}
}

// matches reports whether any of the fields contains the query, ignoring case.
func matches(query string, fields ...string) bool {
	query = strings.ToLower(query)
	for _, f := range fields {
		if strings.Contains(strings.ToLower(f), query) {
			return true
		}
	}
	return false
}

// plural picks the word for a count.
func plural(n int, one, many string) string {
	if n == 1 {
		return one
	}
	return many
}

// fitStyled is fit for text that is styled already: it cuts by cells, keeping the escapes whole.
func fitStyled(s string, w int) string {
	if w <= 0 {
		return ""
	}
	if n := lipgloss.Width(s); n <= w {
		return s + strings.Repeat(" ", w-n)
	}
	cut := ansi.Truncate(s, w, "…")
	return cut + strings.Repeat(" ", max(w-lipgloss.Width(cut), 0))
}

// clip cuts s to w cells with an ellipsis, keeping its escapes whole, and pads nothing.
func clip(s string, w int) string {
	return ansi.Truncate(s, max(w, 0), "…")
}

// look is how the screens are drawn: today's plain look, or the visual one with the web's area
// hues and status colors, the tab header, cards, wide layouts and the mouse. The visual look needs
// a terminal with colors; STUDIO_TUI=plain, NO_COLOR or a terminal without colors keep the plain
// one.
type look struct {
	plain   bool                 // today's look
	forced  bool                 // STUDIO_TUI=plain asked for it
	profile colorprofile.Profile // the colors the terminal shows
	light   bool                 // the terminal's background is light
}

// lookFor is the look for a terminal's color profile; forced is STUDIO_TUI=plain.
func lookFor(profile colorprofile.Profile, forced, light bool) look {
	return look{plain: forced || profile < colorprofile.ANSI, forced: forced, profile: profile, light: light}
}

// plainSetting reports whether the STUDIO_TUI setting asks for the plain look.
func plainSetting(setting string) bool {
	return strings.EqualFold(strings.TrimSpace(setting), "plain")
}

// area is the part of Studio a thing belongs to, which gives it its hue, as on the web.
type area int

const (
	areaNone area = iota
	areaNetwork
	areaGit
	areaBrowser
	areaMemory
	areaChat
)

// palette is the visual look's colors, resolved for the terminal: the web's oklch values as sRGB
// on truecolor terminals (light or dark, as the background is), the nearest xterm color on 256
// colors, and the ANSI colors on 16, so the user's palette applies there.
type palette struct {
	areas   [areaChat + 1]lipgloss.Style
	running lipgloss.Style
	waiting lipgloss.Style
	failed  lipgloss.Style
	stopped lipgloss.Style
	brand   lipgloss.Style // Studio's green, for the ◆ of the header
	added   lipgloss.Style
	removed lipgloss.Style
	keycap  lipgloss.Style
	capBg   bool // keycaps sit on a background; on 16 colors they are bracketed instead
}

// newPalette resolves the palette for a look.
func newPalette(l look) palette {
	complete := lipgloss.Complete(l.profile)
	// hue picks a role's color: the ANSI color, the xterm color, and the web's light and dark values.
	hue := func(ansi, x256 int, light, dark string) color.Color {
		tc := dark
		if l.light {
			tc = light
		}
		return complete(lipgloss.Color(fmt.Sprint(ansi)), lipgloss.Color(fmt.Sprint(x256)), lipgloss.Color(tc))
	}
	fg := func(c color.Color) lipgloss.Style { return lipgloss.NewStyle().Foreground(c) }
	p := palette{
		running: fg(hue(2, 77, "#0fa44d", "#47c86e")),
		waiting: fg(hue(3, 221, "#e2af27", "#f0c551")).Bold(true),
		failed:  fg(hue(1, 203, "#db2c2b", "#fa6863")),
		stopped: fg(hue(7, 245, "#9a9fa5", "#6e7277")),
		brand:   fg(hue(2, 77, "#008238", "#3fc168")).Bold(true),
		keycap:  lipgloss.NewStyle().Bold(true),
	}
	p.areas[areaNone] = styleDim
	p.areas[areaNetwork] = fg(hue(4, 74, "#3a89b3", "#6eb9dc"))
	p.areas[areaGit] = fg(hue(5, 141, "#795bbf", "#b09de9"))
	p.areas[areaBrowser] = fg(hue(3, 215, "#c67447", "#eea471"))
	p.areas[areaMemory] = fg(hue(6, 72, "#35877d", "#76c5bb"))
	p.areas[areaChat] = fg(hue(2, 114, "#3b834e", "#76c788"))
	p.added, p.removed = p.running, p.failed
	if l.profile == colorprofile.ANSI {
		p.stopped = styleDim // the user's palette: dim, not a gray of ours
	}
	if l.profile >= colorprofile.ANSI256 {
		bg := "#33363b"
		if l.light {
			bg = "#e4e6ea"
		}
		x256 := 237
		if l.light {
			x256 = 254
		}
		p.keycap = p.keycap.Background(complete(nil, lipgloss.Color(fmt.Sprint(x256)), lipgloss.Color(bg)))
		p.capBg = true
	}
	return p
}

// hue is the style of an area.
func (p *palette) hue(a area) lipgloss.Style {
	return p.areas[a]
}
