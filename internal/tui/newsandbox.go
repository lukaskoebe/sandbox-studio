package tui

import (
	"fmt"
	"regexp"
	"slices"
	"strings"

	"charm.land/bubbles/v2/key"
	tea "charm.land/bubbletea/v2"

	"github.com/lukaskoebe/sandbox-studio/internal/api"
	"github.com/lukaskoebe/sandbox-studio/internal/sandboxes"
	"github.com/lukaskoebe/sandbox-studio/internal/store"
)

// The steps of a new sandbox: whose it is, what it starts from, and its name.
const (
	stepTeammate = iota
	stepTemplate
	stepName
)

// newState is a new sandbox on its way: the choices of the form, then the create it started,
// which the progress screen follows until the sandbox is ready.
type newState struct {
	step      int
	loaded    bool // the templates arrived
	templates []api.TemplateView
	teammate  int // the cursor among the teammates, the last row being "no teammate"
	template  int // the cursor among the templates, the first row being the base image
	name      input
	creating  bool
	created   *sandboxes.View // the sandbox the create answered with, until the list has it
	listed    bool            // the list had the created sandbox once, so its absence is a delete
}

type (
	templatesLoaded struct {
		env  string
		list []api.TemplateView
		err  error
	}
	sandboxCreated struct {
		sb  sandboxes.View
		err error
	}
)

// namePattern is what a sandbox name may be, as the API and the web's form check it: it is
// also the sandbox's hostname.
var namePattern = regexp.MustCompile(`^[a-z0-9]([a-z0-9-]{0,38}[a-z0-9])?$`)

var keyCreate = key.NewBinding(key.WithKeys("enter"), key.WithHelp("enter", "create"))

// openNew starts the form of a new sandbox, at its first step.
func (m *app) openNew() tea.Cmd {
	m.newSb = newState{}
	m.screen = screenNew
	if len(m.team) == 0 {
		m.newSb.step = stepTemplate // nobody to choose: the sandbox is the environment's
	}
	ctx, client, env := m.ctx, m.client, m.env.ID
	return func() tea.Msg {
		rctx, cancel := requestContext(ctx)
		defer cancel()
		list, err := client.Templates(rctx, env)
		return templatesLoaded{env: env, list: list, err: err}
	}
}

// create starts the create of the sandbox the form describes.
func (m *app) create() tea.Cmd {
	n := &m.newSb
	name := n.name.value()
	if !namePattern.MatchString(name) {
		m.setNotice("A name is lowercase letters, digits and dashes, at most 40, with no dash at either end.", true)
		return nil
	}
	n.creating = true
	m.setNotice("Creating "+name+"…", false)
	ctx, client, env := m.ctx, m.client, m.env.ID
	teammate, template := m.newTeammate().ID, m.newTemplate().ID
	return func() tea.Msg {
		rctx, cancel := requestContext(ctx)
		defer cancel()
		sb, err := client.CreateSandbox(rctx, env, name, teammate, template)
		return sandboxCreated{sb: sb, err: err}
	}
}

// newTeammate is the teammate the form chose; its zero value is none.
func (m *app) newTeammate() store.Teammate {
	if i := m.newSb.teammate; i < len(m.team) {
		return m.team[i]
	}
	return store.Teammate{}
}

// newTemplate is the template the form chose; its zero value is the base image.
func (m *app) newTemplate() api.TemplateView {
	if i := m.newSb.template - 1; i >= 0 && i < len(m.newSb.templates) {
		return m.newSb.templates[i]
	}
	return api.TemplateView{}
}

// newUpdate applies the messages of the new sandbox screens. ok is false for other messages.
func (m *app) newUpdate(msg tea.Msg) (cmd tea.Cmd, ok bool) {
	n := &m.newSb
	switch msg := msg.(type) {
	case templatesLoaded:
		if msg.env != m.env.ID {
			return nil, true
		}
		if msg.err != nil {
			m.fail(msg.err)
		}
		n.loaded, n.templates = true, msg.list
		return nil, true
	case sandboxCreated:
		n.creating = false
		if msg.err != nil {
			m.setNotice("Could not create the sandbox: "+msg.err.Error(), true)
			return nil, true
		}
		if m.screen != screenNew {
			return m.refresh(), true // the user left the form while it was on its way
		}
		n.created = &msg.sb
		m.screen = screenProgress
		m.setNotice("", false)
		m.filter = ""
		return m.refresh(), true
	case sandboxForked:
		return m.forked(msg), true
	}
	return nil, false
}

// progressSandbox is the sandbox the progress screen follows: the list's copy, which the
// sandboxes topic of the event stream keeps fresh, or the create's answer until the list has it.
// found is false once it is in neither, as after a delete.
func (m *app) progressSandbox() (sb sandboxes.View, listed bool, found bool) {
	c := m.newSb.created
	if c == nil {
		return sandboxes.View{}, false, false
	}
	for _, sb := range m.sandboxes {
		if sb.ID == c.ID {
			return sb, true, true
		}
	}
	return *c, false, !m.newSb.listed
}

// followProgress checks the sandbox the progress screen follows after the list was reloaded:
// once its create is over it opens the sandbox, and a deleted one returns to the list.
func (m *app) followProgress() {
	if m.screen != screenProgress {
		return
	}
	sb, listed, found := m.progressSandbox()
	if listed {
		m.newSb.listed = true
	}
	switch {
	case !found:
		m.screen = screenHome
	case listed && phaseOf(sb) != phaseCreating && phaseOf(sb) != phaseFailed:
		m.reselect(sb.ID)
		m.screen = screenDetail
		m.setNotice("Created "+sb.Name+".", false)
	}
}

// newKey answers the form of a new sandbox: the cursor picks a teammate, then a template, and
// the name is typed. Esc returns a step.
func (m *app) newKey(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	n := &m.newSb
	if n.creating {
		return m, nil
	}
	back := func() {
		switch {
		case n.step == stepName:
			n.step = stepTemplate
		case n.step == stepTemplate && len(m.team) > 0:
			n.step = stepTeammate
		default:
			m.screen = screenHome
		}
	}
	if n.step == stepName {
		switch {
		case key.Matches(msg, keyCreate):
			return m, m.create()
		case key.Matches(msg, keyEsc):
			back()
		default:
			if n.name.key(msg) {
				n.name.set(strings.ToLower(n.name.value()))
			}
		}
		return m, nil
	}
	cursor, rows := &n.teammate, len(m.team)+1
	if n.step == stepTemplate {
		cursor, rows = &n.template, len(n.templates)+1
	}
	switch {
	case key.Matches(msg, keyBack):
		back()
	case key.Matches(msg, keyUp):
		*cursor = max(*cursor-1, 0)
	case key.Matches(msg, keyDown):
		*cursor = min(*cursor+1, rows-1)
	case key.Matches(msg, keyChoose):
		n.step++
	}
	return m, nil
}

// progressKey answers the progress screen: esc leaves the create to run on, and a failed
// create's sandbox can be deleted.
func (m *app) progressKey(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	sb, _, found := m.progressSandbox()
	switch {
	case key.Matches(msg, keyBack):
		m.screen = screenHome
		if found {
			m.reselect(sb.ID)
		}
	case key.Matches(msg, keyDelete) && found && phaseOf(sb) == phaseFailed:
		m.askDelete(&sb)
	case key.Matches(msg, keyApprovals):
		m.openApprovals()
	}
	return m, nil
}

// newBody is the form: the steps done so far, and the one on show.
func (m *app) newBody(w, h int) []string {
	n := &m.newSb
	lines := []string{styleBold.Render("New sandbox") + styleDim.Render(" in "+m.env.Name), ""}
	teammate := firstNonEmpty(m.newTeammate().Name, "none")
	if n.step > stepTeammate {
		lines = append(lines, fact("Teammate", teammate))
	}
	if n.step > stepTemplate {
		lines = append(lines, fact("Template", firstNonEmpty(m.newTemplate().Name, "base image")))
	}
	if n.step > stepTeammate {
		lines = append(lines, "")
	}
	pick := func(title string, rows []string, cursor int) {
		lines = append(lines, styleBold.Render(title))
		// The cursor's row stays in view.
		room := max(h-len(lines), 1)
		offset := 0
		if cursor >= room {
			offset = cursor - room + 1
		}
		for i := offset; i < len(rows) && len(lines) < h; i++ {
			mark, row := "  ", rows[i]
			if i == cursor {
				mark, row = m.cursorMark(true), styleBold.Render(row)
			}
			lines = append(lines, mark+row)
		}
	}
	switch n.step {
	case stepTeammate:
		rows := make([]string, 0, len(m.team)+1)
		for _, t := range m.team {
			rows = append(rows, t.Name)
		}
		pick("Whose sandbox is it?", append(rows, styleDim.Render("no teammate")), n.teammate)
	case stepTemplate:
		if !n.loaded {
			return append(lines, styleBold.Render("What does it start from?"), styleDim.Render("Loading the templates…"))
		}
		rows := []string{"base image" + styleDim.Render(" · the bare image, nothing installed")}
		for _, t := range n.templates {
			rows = append(rows, t.Name+styleDim.Render(fmt.Sprintf(" · %d CPUs, %d MiB", t.Resources.CPUs, t.Resources.MemoryMiB)))
		}
		pick("What does it start from?", rows, n.template)
	case stepName:
		lines = append(lines, styleBold.Render("Name"), "  "+n.name.line("", w-4),
			"  "+styleDim.Render("Also its hostname: lowercase letters, digits and dashes."))
	}
	return lines
}

// stepLabels are the progress steps as the web words them.
var stepLabels = map[string]string{
	sandboxes.StepPull:      "Pulling the image",
	sandboxes.StepVM:        "Starting the VM",
	sandboxes.StepCopy:      "Copying the workspace",
	sandboxes.StepBoot:      "Booting the guest",
	sandboxes.StepServices:  "Starting services",
	sandboxes.StepAgent:     "Connecting the agent",
	sandboxes.StepConfigure: "Configuring secrets and CA",
}

// progressBody is the create's steps as the web's progress shows them: those done, the one
// that runs with the image pull's count, and those to come; or the step that failed, and why.
func (m *app) progressBody(w, h int) []string {
	sb, _, found := m.progressSandbox()
	if !found {
		return []string{styleDim.Render("The sandbox is gone.")}
	}
	head := styleBold.Render("Creating "+sb.Name) + styleDim.Render(" in "+m.env.Name)
	if phaseOf(sb) == phaseFailed {
		head = styleBold.Render(sb.Name+" could not be created") + styleDim.Render(" in "+m.env.Name)
	}
	details := nonEmpty(m.teammates[sb.TeammateID], sb.TemplateName)
	if len(details) > 0 {
		head += styleDim.Render(" · " + strings.Join(details, " · "))
	}
	lines := []string{head, ""}
	run := sb.Progress
	if run == nil {
		return append(lines, "  "+styleAccent.Render("◐")+" Starting…")
	}
	steps := run.Steps
	if len(steps) == 0 {
		steps = []string{run.Step}
	}
	current := max(slices.Index(steps, run.Step), 0)
	failed := run.State == sandboxes.ProgressFailed
	for i, step := range steps {
		label := firstNonEmpty(stepLabels[step], step)
		switch {
		case i < current:
			lines = append(lines, "  "+styleDim.Render("✓ "+label))
		case i == current && failed:
			lines = append(lines, "  "+styleFailed.Render("✗ "+label))
		case i == current:
			lines = append(lines, "  "+styleAccent.Render("◐")+" "+styleBold.Render(label))
			if step == sandboxes.StepPull && run.Pull != nil {
				if text := pullText(*run.Pull); text != "" {
					lines = append(lines, "    "+styleDim.Render(text))
				}
			}
		default:
			lines = append(lines, "  "+styleDim.Render("· "+label))
		}
	}
	if failed {
		lines = append(lines, "")
		for _, l := range wrapText(firstNonEmpty(run.Error, "The create failed."), w-2) {
			lines = append(lines, styleFailed.Render(l))
		}
		lines = append(lines, "", styleDim.Render("Delete it with d, then try again."))
	} else {
		lines = append(lines, "", styleDim.Render("It opens once it is ready. esc leaves it to finish on its own."))
	}
	return lines
}

// pullText counts an image pull: by bytes once the total is known, else by layers.
func pullText(p sandboxes.PullProgress) string {
	switch {
	case p.TotalBytes > 0:
		return fmt.Sprintf("%s of %s", bytesText(p.Bytes), bytesText(p.TotalBytes))
	case p.Layers > 0:
		return fmt.Sprintf("%d of %d layers", p.LayersDone, p.Layers)
	}
	return ""
}

// bytesText is a size in the largest unit that keeps it at least 1.
func bytesText(n int64) string {
	const unit = 1024
	if n < unit {
		return fmt.Sprintf("%d B", n)
	}
	v, units := float64(n)/unit, "KMGT"
	i := 0
	for v >= unit && i < len(units)-1 {
		v /= unit
		i++
	}
	return fmt.Sprintf("%.1f %ciB", v, units[i])
}

// newBindings are the keys the key bar shows on the new sandbox screens.
func (m *app) newBindings() []key.Binding {
	if m.screen == screenProgress {
		if sb, _, found := m.progressSandbox(); found && phaseOf(sb) == phaseFailed {
			return []key.Binding{keyDelete, keyBack}
		}
		return []key.Binding{keyBack}
	}
	if m.newSb.step == stepName {
		return []key.Binding{keyCreate, keyStepBack}
	}
	return []key.Binding{keyUp, keyChoose, keyStepBack}
}

var keyStepBack = key.NewBinding(key.WithKeys("esc"), key.WithHelp("esc", "back"))

// The keys of a fork.
var (
	keyFork     = key.NewBinding(key.WithKeys("f"), key.WithHelp("f", "fork"))
	keyForkName = key.NewBinding(key.WithKeys("enter"), key.WithHelp("enter", "fork"))
)

// sandboxForked is the answer to a fork: the new sandbox, in state creating.
type sandboxForked struct {
	sb  sandboxes.View
	err error
}

// askFork asks for the name of a fork of the sandbox on the notice line, starting from the
// web's suggestion. The fork copies the sandbox's disks into a new sandbox, which the progress
// screen then follows as it does a create.
func (m *app) askFork(sb *sandboxes.View) {
	if sb == nil {
		return
	}
	if ph := phaseOf(*sb); ph == phaseCreating || ph == phaseFailed {
		m.setNotice(sb.Name+" can't be forked until it is created.", true)
		return
	}
	p := &linePrompt{
		label: "Fork " + sb.Name + " as: ",
		enter: keyForkName,
		name:  true,
		submit: func(name string) (string, tea.Cmd) {
			if !namePattern.MatchString(name) {
				return "lowercase letters, digits and dashes", nil
			}
			m.setNotice("Forking "+sb.Name+" as "+name+"…", false)
			return "", m.fork(*sb, name)
		},
	}
	p.text.set(forkName(sb.Name))
	m.ask(p)
}

// forkName is the name a fork starts from: the sandbox's, with -fork, short enough to be a name.
func forkName(name string) string {
	runes := []rune(name)
	if len(runes) > 35 {
		runes = runes[:35]
	}
	return strings.TrimRight(string(runes), "-") + "-fork"
}

// fork asks Studio for the fork, which answers at once.
func (m *app) fork(sb sandboxes.View, name string) tea.Cmd {
	ctx, client, env := m.ctx, m.client, m.env.ID
	return func() tea.Msg {
		rctx, cancel := requestContext(ctx)
		defer cancel()
		forked, err := client.Fork(rctx, env, sb.ID, name)
		if err != nil {
			err = fmt.Errorf("fork %s: %w", sb.Name, err)
		}
		return sandboxForked{sb: forked, err: err}
	}
}

// forked follows a fork Studio accepted on the progress screen, unless the user went on to
// something other than the sandboxes.
func (m *app) forked(msg sandboxForked) tea.Cmd {
	if msg.err != nil {
		m.setNotice(msg.err.Error(), true)
		return nil
	}
	if m.screen != screenHome && m.screen != screenDetail {
		m.setNotice("Forking "+msg.sb.Name+"…", false)
		return m.refresh()
	}
	m.newSb = newState{created: &msg.sb}
	m.screen = screenProgress
	m.setNotice("", false)
	m.filter = ""
	return m.refresh()
}
