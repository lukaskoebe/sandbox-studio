package tui

import (
	"slices"
	"strconv"
	"strings"

	"github.com/lukaskoebe/sandbox-studio/internal/browser"
	"github.com/lukaskoebe/sandbox-studio/internal/runtime"
	"github.com/lukaskoebe/sandbox-studio/internal/sandboxes"
)

// The Browser tab is a teammate's browser, to look at: the page it is on, who drove it last,
// what stopped it loading and, on terminals with 256 colors or more, the page itself in half
// blocks, as large as the pane takes. It is read every pictureEvery while it shows, as Home's
// reading pane reads it. Driving the browser stays in the web, whose address the tab shows.

// tabOnShow is the computer whose tab t is on show in the frame.
func (m *app) tabOnShow(t computerTab) *sandboxes.View {
	if m.screen != screenMain || m.comp.tab != t {
		return nil
	}
	sb := m.computerOn()
	if sb == nil || m.mainPane() != paneComputer && m.sidePane() != paneComputer || !slices.Contains(m.computerTabs(), t) {
		return nil
	}
	return sb
}

// vmWords says what a browser that isn't running is doing, in the web's words.
func vmWords(vm string) string {
	switch vm {
	case "starting", "created":
		return "Waking up…"
	case "draining":
		return "Going to sleep…"
	case "crashed":
		return "Stopped unexpectedly"
	case "absent":
		return "No browser yet"
	}
	return "Asleep"
}

// browserTab is the Browser tab of a teammate's computer in w cells and h rows.
func (m *app) browserTab(sb sandboxes.View, w, h int) []string {
	p := &m.picture
	mate := "The teammate"
	if t, ok := m.mateOf(sb.TeammateID); ok {
		mate = t.Name
	}
	if h <= 0 {
		return nil
	}
	if p.teammate != sb.TeammateID || !p.loaded {
		return []string{"", " " + styleDim.Render("Reading…")}
	}
	st := p.status
	web := m.browserURL("/e/" + m.env.ID + "/sandboxes/" + sb.ID + "?tab=browser")
	if st.VM != string(runtime.StatusRunning) {
		lines := []string{"", " " + styleBold.Render(vmWords(st.VM))}
		say := mate + "'s browser wakes when an agent opens a page, or in the browser at " + web
		if st.VM == "absent" {
			say = mate + "'s browser starts when an agent first opens a page, or in the browser at " + web
		}
		return append(append(lines, ""), sayDim(say, w)...)
	}
	lines := []string{" " + clip(styleBold.Render(firstNonEmpty(st.URL, "No page yet")), w-1)}
	if about := browserAbout(st); about != "" {
		lines = append(lines, " "+styleDim.Render(clip(about, w-1)))
	}
	lines = append(lines, " "+styleDim.Render(clip("Drive it in the browser: "+web, w-1)))
	switch {
	case st.Blocked != nil:
		lines = append(lines, sayDim("The page didn't load: a rule for "+st.Blocked.RuleHost+" blocks "+st.Blocked.Host+". Settings, then Network edits it.", w)...)
	case st.SignIn != nil && st.SignIn.Refused:
		lines = append(lines, sayDim(st.SignIn.Host+" refused "+mate+"'s login. The browser asks for another.", w)...)
	case st.SignIn != nil:
		lines = append(lines, sayDim(st.SignIn.Host+" asks for a username and password. The browser signs in.", w)...)
	}
	body := h - len(lines) - 1
	if p.img == nil || !m.pictures() || body < 3 {
		return lines
	}
	b := p.img.Bounds()
	cols := min(w-2, max(body*2*b.Dx()/max(b.Dy(), 1), 1))
	if p.drawn == nil || p.drawnW != cols {
		p.drawn, p.drawnW = halfBlocks(p.img, cols), cols
	}
	lines = append(lines, "")
	for _, l := range p.drawn[:min(len(p.drawn), body)] {
		lines = append(lines, " "+l)
	}
	return lines
}

// browserAbout is who drives a browser, and the pages its agents opened besides.
func browserAbout(st browser.BrowserStatus) string {
	var about string
	switch {
	case st.Takeover:
		about = "You're driving."
	case st.Driver != "":
		about = st.Driver + " drove it last."
	}
	switch n := len(st.Sessions); n {
	case 0:
	case 1:
		about += " An agent has a page of its own open."
	default:
		about += " Agents have " + strconv.Itoa(n) + " pages of their own open."
	}
	return strings.TrimSpace(about)
}
