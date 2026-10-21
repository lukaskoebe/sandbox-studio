package tui

import (
	"fmt"
	"strings"

	"charm.land/bubbles/v2/key"
	tea "charm.land/bubbletea/v2"

	"github.com/lukaskoebe/sandbox-studio/internal/store"
)

// The Settings screen lists the same sections as the web's Settings, grouped as there, each with
// what it is set to. Activity opens the TUI's own log; the other sections are edited in the
// browser, whose address enter prints.

var keySettings = key.NewBinding(key.WithKeys(","), key.WithHelp(",", "settings"))

// settingsSection is one section of Settings.
type settingsSection struct {
	group, name, path string
}

// settingsSections are the sections, in the web's order and groups.
var settingsSections = []settingsSection{
	{"THIS ENVIRONMENT", "General", "general"},
	{"THIS ENVIRONMENT", "Network", "network"},
	{"THIS ENVIRONMENT", "Secrets", "secrets"},
	{"THIS ENVIRONMENT", "Shared memory", "memory"},
	{"THIS ENVIRONMENT", "Activity", "activity"},
	{"ACCOUNTS", "AI providers", "providers"},
	{"ACCOUNTS", "Git forges", "forges"},
	{"THIS COMPUTER", "Templates", "templates"},
	{"THIS COMPUTER", "Runtime", "runtime"},
	{"THIS COMPUTER", "Ports and LAN", "ports"},
	{"THIS COMPUTER", "Updates", "updates"},
}

// settingsState is the Settings screen: its cursor, and what each section is set to.
type settingsState struct {
	back   screen
	cursor int
	values map[string]string // by the section's path
	gen    int
}

type settingsLoaded struct {
	gen    int
	values map[string]string
}

// openSettings shows Settings and reads what its sections are set to.
func (m *app) openSettings() tea.Cmd {
	s := &m.settings
	if m.screen != screenSettings {
		s.back = m.screen
		if s.back == screenHelp {
			s.back = screenHome
		}
	}
	s.gen++
	m.screen = screenSettings
	if m.client == nil {
		return nil
	}
	ctx, client, env, gen := m.ctx, m.client, m.env, s.gen
	return func() tea.Msg {
		rctx, cancel := requestContext(ctx)
		defer cancel()
		base := "/api/environments/" + env.ID
		values := map[string]string{"activity": "one log of every decision", "general": "start at login, the environment file"}
		type named struct {
			Name string `json:"name"`
		}
		var rules []store.Rule
		if client.Do(rctx, "GET", base+"/rules", nil, &rules) == nil {
			values["network"] = fmt.Sprintf("%s · %d %s", levelWords(env.SecurityLevel), len(rules), plural(len(rules), "rule", "rules"))
		}
		var secrets []named
		if client.Do(rctx, "GET", base+"/secrets", nil, &secrets) == nil {
			values["secrets"] = fmt.Sprintf("%d %s", len(secrets), plural(len(secrets), "secret", "secrets"))
		}
		var facts []named
		if client.Do(rctx, "GET", base+"/memory/facts?scope=shared&status=active", nil, &facts) == nil {
			values["memory"] = fmt.Sprintf("%d %s", len(facts), plural(len(facts), "fact", "facts"))
		}
		names := func(list []named) string {
			var out []string
			for _, n := range list {
				out = append(out, n.Name)
			}
			if len(out) == 0 {
				return "none yet"
			}
			return strings.Join(out, ", ")
		}
		var providers, forges []named
		if client.Do(rctx, "GET", base+"/providers", nil, &providers) == nil {
			values["providers"] = names(providers)
		}
		if client.Do(rctx, "GET", base+"/forges", nil, &forges) == nil {
			values["forges"] = names(forges)
		}
		if list, err := client.Templates(rctx, env.ID); err == nil {
			values["templates"] = fmt.Sprintf("%d %s", len(list), plural(len(list), "template", "templates"))
		}
		return settingsLoaded{gen: gen, values: values}
	}
}

// settingsUpdate applies the messages of Settings. ok is false for other messages.
func (m *app) settingsUpdate(msg tea.Msg) (cmd tea.Cmd, ok bool) {
	if msg, is := msg.(settingsLoaded); is {
		if msg.gen == m.settings.gen {
			m.settings.values = msg.values
		}
		return nil, true
	}
	return nil, false
}

// settingsKey answers Settings: the cursor moves over the sections, and enter opens one.
func (m *app) settingsKey(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	s := &m.settings
	switch {
	case key.Matches(msg, keyBack):
		m.screen = s.back
	case key.Matches(msg, keyUp):
		s.cursor = max(s.cursor-1, 0)
	case key.Matches(msg, keyDown):
		s.cursor = min(s.cursor+1, len(settingsSections)-1)
	case key.Matches(msg, keyOpen):
		sec := settingsSections[s.cursor]
		if sec.path == "activity" {
			m.screen = s.back
			return m, m.openActivity("")
		}
		m.setNotice("open in browser: "+m.browserURL("/e/"+m.env.ID+"/settings/"+sec.path), false)
	case key.Matches(msg, keyActivity):
		m.screen = s.back
		return m, m.openActivity("")
	case key.Matches(msg, keyHelp):
		m.openHelp()
	}
	return m, nil
}

// settingsBody lists the sections in their groups, with what each is set to.
func (m *app) settingsBody(w, h int) []string {
	s := &m.settings
	lines := []string{spread(styleBold.Render("Settings")+styleDim.Render(" · "+m.env.Name), styleDim.Render("the same sections as the web"), w), ""}
	group := ""
	for i, sec := range settingsSections {
		if sec.group != group {
			if group != "" {
				lines = append(lines, "")
			}
			group = sec.group
			lines = append(lines, styleDim.Render(group))
		}
		name := fit(sec.name, 16)
		if i == s.cursor {
			name = styleBold.Render(name)
		}
		m.hitRow(len(lines), 0, w, rowSetting, i)
		lines = append(lines, m.cursorMark(i == s.cursor)+name+styleDim.Render(clip(s.values[sec.path], max(w-18, 0))))
	}
	lines = append(lines, "", styleDim.Render(clip("Enter opens the section; all but Activity are edited in the browser, at "+m.browserURL("/")+".", w)))
	return lines[:min(len(lines), h)]
}
