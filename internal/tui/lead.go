package tui

import (
	"context"
	"fmt"
	"net/http"
	"regexp"
	"strings"
	"time"

	"charm.land/bubbles/v2/key"
	tea "charm.land/bubbletea/v2"

	"github.com/lukaskoebe/sandbox-studio/internal/lead"
)

// Lead's row on Home (docs/lead.md): at the top of QUIET once a teammate worked today or the day
// is written, its reading pane the day as Markdown, and w writes the day now.

var keyWriteDay = key.NewBinding(key.WithKeys("w"), key.WithHelp("w", "write the day now"))

// leadLoaded is Lead and today's day, as read for Home.
type leadLoaded struct {
	env  string
	view *lead.LeadView
	day  *lead.LeadDayView
}

// fetchLead reads the environment's Lead and, once anything happened today, today's day. An
// older Studio without Lead answers 404, and Home goes without the row.
func fetchLead(ctx context.Context, client *Client, env string) leadLoaded {
	out := leadLoaded{env: env}
	var v lead.LeadView
	if err := client.Do(ctx, "GET", "/api/environments/"+env+"/lead", nil, &v); err != nil {
		return out
	}
	out.view = &v
	if !leadShows(&v) {
		return out
	}
	var d lead.LeadDayView
	if err := client.Do(ctx, "GET", "/api/environments/"+env+"/lead/days/"+v.Today.Day, nil, &d); err == nil {
		out.day = &d
	}
	return out
}

// loadLead reads Lead for Home's row.
func (m *app) loadLead() tea.Cmd {
	if m.client == nil || m.env.ID == "" {
		return nil
	}
	ctx, client, env := m.ctx, m.client, m.env.ID
	return func() tea.Msg {
		rctx, cancel := requestContext(ctx)
		defer cancel()
		return fetchLead(rctx, client, env)
	}
}

// takeLead keeps what fetchLead read, unless the environment changed meanwhile.
func (m *app) takeLead(l leadLoaded) {
	if l.env != m.env.ID {
		return
	}
	m.inbox.lead, m.inbox.day, m.inbox.leadEnv = l.view, l.day, l.env
}

// leadShows reports whether Home lists Lead: a teammate worked today, or the day is written.
func leadShows(v *lead.LeadView) bool {
	return v != nil && (v.Today.Turns > 0 || v.Today.WrittenAt != nil)
}

// writeDay asks Lead to write today's day now; a lead event brings it.
func (m *app) writeDay() tea.Cmd {
	v := m.inbox.lead
	if v == nil {
		return nil
	}
	if v.Writing {
		m.setNotice(v.Settings.Name+" is writing the day.", false)
		return nil
	}
	v.Writing = true
	ctx, client, env, name := m.ctx, m.client, m.env.ID, v.Settings.Name
	return func() tea.Msg {
		rctx, cancel := requestContext(ctx)
		defer cancel()
		err := client.Do(rctx, "POST", "/api/environments/"+env+"/lead/summarize", nil, nil)
		switch {
		case apiStatus(err) == http.StatusConflict:
			return actionDone{done: name + " is writing the day"}
		case err != nil:
			return actionDone{err: fmt.Errorf("write the day: %w", err)}
		}
		return actionDone{done: name + " is writing the day"}
	}
}

// leadCells are Lead's row: its glyph, the day's summary or how far it got, and when it was
// written.
func (m *app) leadCells() (glyph, what, where string, when time.Time) {
	v := m.inbox.lead
	glyph = styleAccent.Render("◆")
	t := v.Today
	switch {
	case v.Writing:
		what, where = "writing the day…", "now"
	case t.WrittenAt != nil:
		what = firstNonEmpty(oneLine(t.Summary), "the day is written")
		where, when = "written "+t.WrittenAt.Local().Format("15:04"), *t.WrittenAt
	default:
		what = fmt.Sprintf("today so far · %d %s, %d %s", t.Threads, plural(t.Threads, "thread", "threads"),
			t.Turns, plural(t.Turns, "turn", "turns"))
		if t.Waiting > 0 {
			what += fmt.Sprintf(" · %d %s for you", t.Waiting, plural(t.Waiting, "waits", "wait"))
		}
		where = "so far"
	}
	return glyph, what, where, when
}

// readingLead is today's day in the reading pane, as Copy as Markdown has it, from the scroll on.
func (m *app) readingLead(rc rect) []string {
	in := &m.inbox
	v, d := in.lead, in.day
	name := v.Settings.Name
	head := "Today so far"
	if d != nil && d.Written {
		head = "Today"
	}
	lines := []string{
		spread(styleBold.Render(head), styleDim.Render(name), rc.w),
		styleDim.Render(strings.Repeat("─", rc.w)),
	}
	if d == nil {
		lines = append(lines, styleDim.Render("Reading the day…"))
	} else {
		if d.Note != "" {
			lines = append(lines, wrapText(d.Note, rc.w)...)
			lines = append(lines, "")
		}
		lines = append(lines, markdownLines(d.Markdown, rc.w)...)
	}
	in.scroll = min(in.scroll, max(len(lines)-rc.h, 0))
	return lines[in.scroll:]
}

// mdEscaped is a character the day's Markdown escapes in a text that isn't Studio's own.
var mdEscaped = regexp.MustCompile("\\\\([\\\\`*_\\[\\]<>#])")

// markdownLines is the day's Markdown in w cells: headings bold without their marks, list items
// wrapped under their text, and escaped characters as they are.
func markdownLines(md string, w int) []string {
	un := func(s string) string { return mdEscaped.ReplaceAllString(s, "$1") }
	var out []string
	for _, line := range strings.Split(strings.TrimSpace(md), "\n") {
		switch {
		case strings.HasPrefix(line, "# "):
			out = append(out, styleBold.Render(clip(un(strings.TrimPrefix(line, "# ")), w)))
		case strings.HasPrefix(line, "## "), strings.HasPrefix(line, "### "):
			out = append(out, styleAccent.Render(clip(un(strings.TrimLeft(line, "# ")), w)))
		case strings.HasPrefix(line, "- "):
			for i, l := range wrapText(un(strings.TrimPrefix(line, "- ")), w-2) {
				if i == 0 {
					out = append(out, "• "+l)
				} else {
					out = append(out, "  "+l)
				}
			}
		case strings.TrimSpace(line) == "":
			out = append(out, "")
		default:
			out = append(out, wrapText(un(line), w)...)
		}
	}
	return out
}
