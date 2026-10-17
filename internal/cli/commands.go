package cli

import (
	"bufio"
	"flag"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/lukaskoebe/sandbox-studio/internal/api"
	"github.com/lukaskoebe/sandbox-studio/internal/runtime"
	"github.com/lukaskoebe/sandbox-studio/internal/sandboxes"
	"github.com/lukaskoebe/sandbox-studio/internal/store"
	"github.com/lukaskoebe/sandbox-studio/internal/tui"
)

// pollEvery is how often a command that waits asks Studio again.
const pollEvery = 500 * time.Millisecond

var commands []*command

func init() {
	commands = []*command{
		{name: "ls", args: "", summary: "List the sandboxes of the environment.", run: (*call).list},
		{name: "show", args: "<sandbox>", summary: "Show a sandbox: status, teammate, template, resources.", run: (*call).show},
		{name: "start", args: "<sandbox>", summary: "Start a stopped sandbox, or resume a suspended one. Waits until it runs.", run: lifecycle("start")},
		{name: "stop", args: "<sandbox>", summary: "Stop a sandbox. Its disks stay; the next start boots it again.", run: lifecycle("stop")},
		{name: "suspend", args: "<sandbox>", summary: "Suspend a sandbox: its memory is saved and it resumes where it was.", run: lifecycle("suspend")},
		{name: "resume", args: "<sandbox>", summary: "Resume a suspended sandbox.", run: lifecycle("resume")},
		{name: "create", args: "<name>", summary: "Create a sandbox and wait until it runs.", flags: createFlags, run: (*call).create},
		{name: "fork", args: "<sandbox> <name>", summary: "Fork a sandbox into a new one with a copy of its disks, and wait until it runs.", flags: waitFlag, run: (*call).fork},
		{name: "rm", args: "<sandbox>", summary: "Delete a sandbox and its disks. Asks first, unless --yes.", flags: yesFlag, run: (*call).remove},
		{name: "shell", args: "<sandbox>", summary: "Open the sandbox's terminal here. Ctrl-] detaches; the terminal keeps running.", run: (*call).shell},
		{name: "ask", args: "<sandbox> <prompt>|-", summary: "Ask the sandbox's teammate in a chat and print its answer as it comes.", flags: askFlags, run: (*call).ask},
		{name: "approvals", args: "", summary: "List the approvals waiting for you, in every environment.", run: (*call).approvals},
		{name: "approve", args: "<approval>", summary: "Allow a pending request. A network request gets a rule, unless --once.", flags: decideFlags, run: decide("allow")},
		{name: "deny", args: "<approval>", summary: "Deny a pending request. A network request gets a rule, unless --once.", flags: decideFlags, run: decide("deny")},
		{name: "checkpoints", args: "<sandbox>", summary: "List a sandbox's checkpoints.", run: (*call).checkpoints},
		{name: "checkpoint", args: "<sandbox> [name]", summary: "Save a checkpoint of a sandbox, named after the time unless a name is given.", run: (*call).checkpoint},
		{name: "restore", args: "<sandbox> <checkpoint>", summary: "Put a sandbox back to a checkpoint. Asks first, unless --yes.", flags: yesFlag, run: (*call).restore},
		{name: "envs", args: "", summary: "List the environments.", run: (*call).envs},
		{name: "completion", args: "bash|zsh|fish", summary: "Print the shell completion script.", offline: true, run: (*call).completion},
	}
}

// --- shared lookups ---

// environment resolves --env: by ID, then by name ignoring case; without it, the first
// environment, as the TUI and the web UI open.
func (c *call) environment() (string, error) {
	if c.envID != "" {
		return c.envID, nil
	}
	var envs []store.Environment
	if err := c.client.Do(c.ctx, http.MethodGet, "/api/environments", nil, &envs); err != nil {
		return "", err
	}
	if len(envs) == 0 {
		return "", fmt.Errorf("Studio has no environment yet")
	}
	if c.env == "" {
		c.envID = envs[0].ID
		return c.envID, nil
	}
	for _, e := range envs {
		if e.ID == c.env {
			c.envID = e.ID
			return c.envID, nil
		}
	}
	for _, e := range envs {
		if strings.EqualFold(e.Name, c.env) {
			c.envID = e.ID
			return c.envID, nil
		}
	}
	return "", fmt.Errorf("no environment %q; `studio envs` lists them", c.env)
}

func (c *call) path(format string, args ...any) (string, error) {
	env, err := c.environment()
	if err != nil {
		return "", err
	}
	escaped := make([]any, len(args))
	for i, a := range args {
		escaped[i] = url.PathEscape(fmt.Sprint(a))
	}
	return "/api/environments/" + url.PathEscape(env) + fmt.Sprintf(format, escaped...), nil
}

func (c *call) sandboxes() ([]sandboxes.View, error) {
	p, err := c.path("/sandboxes")
	if err != nil {
		return nil, err
	}
	var out []sandboxes.View
	return out, c.client.Do(c.ctx, http.MethodGet, p, nil, &out)
}

// sandbox finds a sandbox by ID or by name.
func (c *call) sandbox(ref string) (sandboxes.View, error) {
	all, err := c.sandboxes()
	if err != nil {
		return sandboxes.View{}, err
	}
	for _, sb := range all {
		if sb.ID == ref {
			return sb, nil
		}
	}
	for _, sb := range all {
		if sb.Name == ref {
			return sb, nil
		}
	}
	return sandboxes.View{}, fmt.Errorf("no sandbox %q in this environment; `studio ls` lists them", ref)
}

func (c *call) teammates() (map[string]store.Teammate, error) {
	p, err := c.path("/teammates")
	if err != nil {
		return nil, err
	}
	var list []store.Teammate
	if err := c.client.Do(c.ctx, http.MethodGet, p, nil, &list); err != nil {
		return nil, err
	}
	out := map[string]store.Teammate{}
	for _, t := range list {
		out[t.ID] = t
	}
	return out, nil
}

func args(got []string, names ...string) error {
	if len(got) < len(names) {
		return usagef("missing %s", strings.Join(names[len(got):], " and "))
	}
	if len(got) > len(names) {
		return usagef("unexpected %q", strings.Join(got[len(names):], " "))
	}
	return nil
}

func (c *call) table() *tabwriter.Writer {
	return tabwriter.NewWriter(c.opts.Stdout, 0, 0, 2, ' ', 0)
}

// confirm asks the user to type name before something that can't be undone. Without a terminal
// it refuses, so a script has to say --yes.
func (c *call) confirm(what, name string) error {
	if c.yes {
		return nil
	}
	if !c.opts.Interactive {
		return usagef("%s can't be undone: add --yes to do it without asking", what)
	}
	fmt.Fprintf(c.opts.Stderr, "%s can't be undone. Type %s to go on: ", what, name)
	line, _ := bufio.NewReader(c.opts.Stdin).ReadString('\n')
	if strings.TrimSpace(line) != name {
		return failure{status: ExitFailed, msg: "cancelled"}
	}
	return nil
}

func yesFlag(fs *flag.FlagSet, c *call) {
	fs.BoolVar(&c.yes, "yes", false, "don't ask first")
}

// --- sandboxes ---

func (c *call) list(rest []string) error {
	if err := args(rest); err != nil {
		return err
	}
	all, err := c.sandboxes()
	if err != nil {
		return err
	}
	if c.json {
		return c.printJSON(all)
	}
	if len(all) == 0 {
		fmt.Fprintln(c.opts.Stderr, "No sandboxes yet. Create one with `studio create <name>`.")
		return nil
	}
	team, err := c.teammates()
	if err != nil {
		return err
	}
	w := c.table()
	fmt.Fprintln(w, "NAME\tSTATUS\tTEAMMATE\tTEMPLATE\tUPTIME")
	for _, sb := range all {
		fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\n", clean(sb.Name), sb.Status, clean(orDash(team[sb.TeammateID].Name)),
			clean(orDash(sb.TemplateName)), uptime(sb))
	}
	return w.Flush()
}

func (c *call) show(rest []string) error {
	if err := args(rest, "sandbox"); err != nil {
		return err
	}
	sb, err := c.sandbox(rest[0])
	if err != nil {
		return err
	}
	if c.json {
		return c.printJSON(sb)
	}
	team, err := c.teammates()
	if err != nil {
		return err
	}
	w := c.table()
	fmt.Fprintf(w, "Name\t%s\n", clean(sb.Name))
	fmt.Fprintf(w, "ID\t%s\n", sb.ID)
	fmt.Fprintf(w, "Status\t%s\n", sb.Status)
	if sb.Progress != nil && sb.Progress.Error != "" {
		fmt.Fprintf(w, "Error\t%s\n", clean(sb.Progress.Error))
	}
	fmt.Fprintf(w, "Uptime\t%s\n", uptime(sb))
	fmt.Fprintf(w, "Teammate\t%s\n", clean(orDash(team[sb.TeammateID].Name)))
	fmt.Fprintf(w, "Template\t%s\n", clean(orDash(sb.TemplateName)))
	fmt.Fprintf(w, "Resources\t%d CPU · %s memory · %s workspace\n", sb.CPUs, mib(sb.MemoryMiB), mib(sb.WorkspaceMiB))
	fmt.Fprintf(w, "Created\t%s\n", sb.CreatedAt.Local().Format("2006-01-02 15:04"))
	return w.Flush()
}

// lifecycle is start, stop, suspend or resume. Studio answers once the VM is in its new state.
func lifecycle(action string) func(*call, []string) error {
	return func(c *call, rest []string) error {
		if err := args(rest, "sandbox"); err != nil {
			return err
		}
		sb, err := c.sandbox(rest[0])
		if err != nil {
			return err
		}
		if action == "start" && sb.Status == runtime.StatusSuspended {
			action = "resume"
		}
		p, err := c.path("/sandboxes/%s/%s", sb.ID, action)
		if err != nil {
			return err
		}
		var out sandboxes.View
		if err := c.client.Do(c.ctx, http.MethodPost, p, nil, &out); err != nil {
			return err
		}
		if c.json {
			return c.printJSON(out)
		}
		fmt.Fprintf(c.opts.Stdout, "%s is %s\n", clean(out.Name), out.Status)
		return nil
	}
}

type createOpts struct {
	teammate, template string
	cpus, memoryGiB    int
	noWait             bool
}

func createFlags(fs *flag.FlagSet, c *call) {
	o := &createOpts{}
	c.extra = o
	fs.StringVar(&o.teammate, "teammate", "", "the teammate that owns the sandbox, by name")
	fs.StringVar(&o.template, "template", "", "the template to start from, by name (default: the base image)")
	fs.IntVar(&o.cpus, "cpus", 0, "CPUs (default: Studio's)")
	fs.IntVar(&o.memoryGiB, "memory", 0, "memory in GiB (default: Studio's)")
	fs.BoolVar(&o.noWait, "no-wait", false, "return once Studio has accepted it")
}

func waitFlag(fs *flag.FlagSet, c *call) {
	o := &createOpts{}
	c.extra = o
	fs.BoolVar(&o.noWait, "no-wait", false, "return once Studio has accepted it")
}

func (c *call) create(rest []string) error {
	if err := args(rest, "name"); err != nil {
		return err
	}
	o := c.extra.(*createOpts)
	req := sandboxes.CreateRequest{Name: rest[0], CPUs: o.cpus, MemoryMiB: o.memoryGiB * 1024}
	if o.teammate != "" {
		team, err := c.teammates()
		if err != nil {
			return err
		}
		for _, t := range team {
			if t.ID == o.teammate || strings.EqualFold(t.Name, o.teammate) {
				req.TeammateID = t.ID
			}
		}
		if req.TeammateID == "" {
			return fmt.Errorf("no teammate %q in this environment", o.teammate)
		}
	}
	p, err := c.path("/sandboxes")
	if err != nil {
		return err
	}
	var body any = req
	if o.template != "" {
		id, err := c.templateID(o.template)
		if err != nil {
			return err
		}
		if req.CPUs != 0 || req.MemoryMiB != 0 {
			return usagef("a template sets the resources: --cpus and --memory don't go with --template")
		}
		if p, err = c.path("/templates/%s/sandboxes", id); err != nil {
			return err
		}
		body = map[string]string{"name": req.Name, "teammateId": req.TeammateID}
	}
	var out sandboxes.View
	if err := c.client.Do(c.ctx, http.MethodPost, p, body, &out); err != nil {
		return err
	}
	return c.settle(out, o.noWait)
}

func (c *call) templateID(ref string) (string, error) {
	p, err := c.path("/templates")
	if err != nil {
		return "", err
	}
	var list []api.TemplateView
	if err := c.client.Do(c.ctx, http.MethodGet, p, nil, &list); err != nil {
		return "", err
	}
	for _, t := range list {
		if t.ID == ref || t.Name == ref {
			return t.ID, nil
		}
	}
	return "", fmt.Errorf("no template %q in this environment", ref)
}

func (c *call) fork(rest []string) error {
	if err := args(rest, "sandbox", "name"); err != nil {
		return err
	}
	sb, err := c.sandbox(rest[0])
	if err != nil {
		return err
	}
	p, err := c.path("/sandboxes/%s/fork", sb.ID)
	if err != nil {
		return err
	}
	var out sandboxes.View
	if err := c.client.Do(c.ctx, http.MethodPost, p, map[string]string{"name": rest[1]}, &out); err != nil {
		return err
	}
	return c.settle(out, c.extra.(*createOpts).noWait)
}

// settle waits for a new sandbox to run, unless noWait. Ctrl-C stops the waiting only: the
// sandbox is created all the same.
func (c *call) settle(sb sandboxes.View, noWait bool) error {
	if !noWait && !c.json {
		fmt.Fprintf(c.opts.Stderr, "Creating %s…\n", clean(sb.Name))
	}
	for !noWait && (sb.Status == sandboxes.StatusCreating || sb.Status == runtime.StatusStarting || sb.Status == runtime.StatusCreated) {
		select {
		case <-c.ctx.Done():
			return c.ctx.Err()
		case <-time.After(pollEvery):
		}
		p, err := c.path("/sandboxes/%s", sb.ID)
		if err != nil {
			return err
		}
		if err := c.client.Do(c.ctx, http.MethodGet, p, nil, &sb); err != nil {
			return err
		}
	}
	if c.json {
		if err := c.printJSON(sb); err != nil {
			return err
		}
	} else {
		fmt.Fprintf(c.opts.Stdout, "%s is %s\n", clean(sb.Name), sb.Status)
	}
	if sb.Status == sandboxes.StatusFailed {
		msg := ""
		if sb.Progress != nil {
			msg = sb.Progress.Error
		}
		return failure{status: ExitFailed, msg: msg}
	}
	return nil
}

func (c *call) remove(rest []string) error {
	if err := args(rest, "sandbox"); err != nil {
		return err
	}
	sb, err := c.sandbox(rest[0])
	if err != nil {
		return err
	}
	if err := c.confirm("Deleting "+sb.Name+" and its disks", sb.Name); err != nil {
		return err
	}
	p, err := c.path("/sandboxes/%s", sb.ID)
	if err != nil {
		return err
	}
	if err := c.client.Do(c.ctx, http.MethodDelete, p, nil, nil); err != nil {
		return err
	}
	if c.json {
		return c.printJSON(map[string]string{"deleted": sb.ID})
	}
	fmt.Fprintf(c.opts.Stdout, "Deleted %s\n", clean(sb.Name))
	return nil
}

func (c *call) shell(rest []string) error {
	if err := args(rest, "sandbox"); err != nil {
		return err
	}
	if !c.opts.Interactive {
		return usagef("shell needs an interactive terminal")
	}
	sb, err := c.sandbox(rest[0])
	if err != nil {
		return err
	}
	if sb.Status != runtime.StatusRunning {
		return fmt.Errorf("%s is %s; start it with `studio start %s`", sb.Name, sb.Status, sb.Name)
	}
	env, err := c.environment()
	if err != nil {
		return err
	}
	fmt.Fprintf(c.opts.Stderr, "Attached to %s. Ctrl-] detaches.\r\n", clean(sb.Name))
	return tui.AttachTerminal(c.ctx, c.client, env, sb.ID)
}

// --- approvals ---

func (c *call) approvals(rest []string) error {
	if err := args(rest); err != nil {
		return err
	}
	var list []api.ApprovalView
	if err := c.client.Do(c.ctx, http.MethodGet, "/api/approvals", nil, &list); err != nil {
		return err
	}
	if c.json {
		return c.printJSON(list)
	}
	if len(list) == 0 {
		fmt.Fprintln(c.opts.Stderr, "Nothing is waiting for you.")
		return nil
	}
	names := map[string]string{}
	w := c.table()
	fmt.Fprintln(w, "ID\tKIND\tFROM\tREQUEST\tAGE")
	for _, a := range list {
		from, ok := names[a.EnvironmentID+"/"+a.SandboxID]
		if !ok {
			from = c.sandboxName(a.EnvironmentID, a.SandboxID)
			names[a.EnvironmentID+"/"+a.SandboxID] = from
		}
		fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\n", a.ID, a.Kind, clean(orDash(from)), clean(a.Subject), age(a.CreatedAt))
	}
	return w.Flush()
}

// sandboxName is the name of a sandbox of any environment, or its ID when it can't be found.
func (c *call) sandboxName(env, id string) string {
	if id == "" {
		return ""
	}
	var sb sandboxes.View
	if c.client.Do(c.ctx, http.MethodGet, "/api/environments/"+url.PathEscape(env)+"/sandboxes/"+url.PathEscape(id), nil, &sb) != nil {
		return id
	}
	return sb.Name
}

type decideOpts struct {
	once      bool
	scope     string
	sandboxes bool
}

func decideFlags(fs *flag.FlagSet, c *call) {
	o := &decideOpts{}
	c.extra = o
	fs.BoolVar(&o.once, "once", false, "for a network request: decide this one connection, without a rule")
	fs.StringVar(&o.scope, "scope", "", "for a network rule: sandbox, teammate or environment (default: the teammate's usual scope)")
	fs.BoolVar(&o.sandboxes, "sandboxes", false, "for a browser request: the rule also covers the sandboxes' own connections")
}

// decide answers an approval of any environment: the ID is enough to find it. A browser
// request's rule covers only the browsers, as in the web card, unless --sandboxes.
func decide(action string) func(*call, []string) error {
	return func(c *call, rest []string) error {
		if err := args(rest, "approval"); err != nil {
			return err
		}
		o := c.extra.(*decideOpts)
		var list []api.ApprovalView
		if err := c.client.Do(c.ctx, http.MethodGet, "/api/approvals", nil, &list); err != nil {
			return err
		}
		var found *api.ApprovalView
		for i := range list {
			if list[i].ID == rest[0] {
				found = &list[i]
			}
		}
		if found == nil {
			return fmt.Errorf("no pending approval %q; `studio approvals` lists them", rest[0])
		}
		body := map[string]any{"action": action}
		network := found.Network != nil
		browser := network && found.Network.Method != ""
		switch {
		case o.once && !network:
			return usagef("--once is for network requests")
		case o.sandboxes && (!browser || o.once):
			return usagef("--sandboxes is for a browser request's rule")
		case o.once:
			body["action"] = action + "_once"
		case browser:
			body["scope"] = browserScope(found, o)
			if !o.sandboxes {
				body["browserOnly"] = true
			}
		case network:
			if o.scope != "" {
				body["scope"] = o.scope
			} else if found.Teammate != nil && found.Teammate.ApprovalScope != "" {
				body["scope"] = found.Teammate.ApprovalScope
			}
		}
		var out api.ApprovalView
		p := "/api/environments/" + url.PathEscape(found.EnvironmentID) + "/approvals/" + url.PathEscape(found.ID) + "/decide"
		if err := c.client.Do(c.ctx, http.MethodPost, p, body, &out); err != nil {
			return err
		}
		if c.json {
			return c.printJSON(out)
		}
		fmt.Fprintf(c.opts.Stdout, "%s: %s\n", out.Status, clean(out.Subject))
		return nil
	}
}

// browserScope is the scope of a browser request's rule, as the web card starts it: the
// teammate's browser (the sandbox's, without a teammate), every browser when the teammate
// decides for the environment, and the environment with --sandboxes. --scope wins.
func browserScope(a *api.ApprovalView, o *decideOpts) string {
	switch {
	case o.scope != "":
		return o.scope
	case o.sandboxes:
		return "environment"
	case a.Teammate == nil:
		return "sandbox"
	case a.Teammate.ApprovalScope == "environment":
		return "environment"
	}
	return "teammate"
}

// --- checkpoints ---

func (c *call) checkpoints(rest []string) error {
	if err := args(rest, "sandbox"); err != nil {
		return err
	}
	sb, err := c.sandbox(rest[0])
	if err != nil {
		return err
	}
	list, err := c.checkpointList(sb.ID)
	if err != nil {
		return err
	}
	if c.json {
		return c.printJSON(list)
	}
	if len(list) == 0 {
		fmt.Fprintf(c.opts.Stderr, "%s has no checkpoints. Save one with `studio checkpoint %s`.\n", clean(sb.Name), clean(sb.Name))
		return nil
	}
	w := c.table()
	fmt.Fprintln(w, "NAME\tSTATE\tSAVED\tID")
	for _, cp := range list {
		fmt.Fprintf(w, "%s\t%s\t%s\t%s\n", clean(orDash(cp.Name)), cp.State, cp.CreatedAt.Local().Format("2006-01-02 15:04"), cp.ID)
	}
	return w.Flush()
}

func (c *call) checkpointList(sandbox string) ([]store.Checkpoint, error) {
	p, err := c.path("/sandboxes/%s/checkpoints", sandbox)
	if err != nil {
		return nil, err
	}
	var list []store.Checkpoint
	return list, c.client.Do(c.ctx, http.MethodGet, p, nil, &list)
}

func (c *call) checkpoint(rest []string) error {
	if len(rest) == 1 {
		rest = append(rest, "")
	}
	if err := args(rest, "sandbox", "name"); err != nil {
		return err
	}
	sb, err := c.sandbox(rest[0])
	if err != nil {
		return err
	}
	name := strings.TrimSpace(rest[1])
	if name == "" {
		list, err := c.checkpointList(sb.ID)
		if err != nil {
			return err
		}
		name = suggestedCheckpointName(time.Now(), list)
	}
	p, err := c.path("/sandboxes/%s/checkpoints", sb.ID)
	if err != nil {
		return err
	}
	var out store.Checkpoint
	if err := c.client.Do(c.ctx, http.MethodPost, p, map[string]string{"name": name}, &out); err != nil {
		return err
	}
	if c.json {
		return c.printJSON(out)
	}
	fmt.Fprintf(c.opts.Stdout, "Checkpoint \"%s\" of %s is %s\n", clean(orDash(out.Name)), clean(sb.Name), out.State)
	return nil
}

// suggestedCheckpointName names a checkpoint saved without a name as the web's dialog does:
// "Checkpoint YYYY-MM-DD HH:MM:SS UTC", with " (2)" and up when a checkpoint has that name.
func suggestedCheckpointName(now time.Time, existing []store.Checkpoint) string {
	base := "Checkpoint " + now.UTC().Format("2006-01-02 15:04:05") + " UTC"
	taken := map[string]bool{}
	for _, cp := range existing {
		taken[cp.Name] = true
	}
	name := base
	for n := 2; taken[name]; n++ {
		name = fmt.Sprintf("%s (%d)", base, n)
	}
	return name
}

func (c *call) restore(rest []string) error {
	if err := args(rest, "sandbox", "checkpoint"); err != nil {
		return err
	}
	sb, err := c.sandbox(rest[0])
	if err != nil {
		return err
	}
	list, err := c.checkpointList(sb.ID)
	if err != nil {
		return err
	}
	var cp *store.Checkpoint
	for i := range list {
		if list[i].ID == rest[1] || list[i].Name == rest[1] {
			cp = &list[i]
			break
		}
	}
	if cp == nil {
		return fmt.Errorf("%s has no checkpoint %q; `studio checkpoints %s` lists them", sb.Name, rest[1], sb.Name)
	}
	if err := c.confirm("Restoring "+sb.Name+" drops what changed since the checkpoint, which", sb.Name); err != nil {
		return err
	}
	p, err := c.path("/sandboxes/%s/checkpoints/%s/restore", sb.ID, cp.ID)
	if err != nil {
		return err
	}
	var out sandboxes.View
	if err := c.client.Do(c.ctx, http.MethodPost, p, nil, &out); err != nil {
		return err
	}
	if c.json {
		return c.printJSON(out)
	}
	fmt.Fprintf(c.opts.Stdout, "%s is back at %s and %s\n", clean(sb.Name), clean(orDash(cp.Name)), out.Status)
	return nil
}

// --- environments ---

func (c *call) envs(rest []string) error {
	if err := args(rest); err != nil {
		return err
	}
	var list []store.Environment
	if err := c.client.Do(c.ctx, http.MethodGet, "/api/environments", nil, &list); err != nil {
		return err
	}
	if c.json {
		return c.printJSON(list)
	}
	w := c.table()
	fmt.Fprintln(w, "NAME\tID")
	for _, e := range list {
		fmt.Fprintf(w, "%s\t%s\n", clean(e.Name), e.ID)
	}
	return w.Flush()
}

// --- formatting ---

func orDash(s string) string {
	if s == "" {
		return "–"
	}
	return s
}

func uptime(sb sandboxes.View) string {
	if sb.Status != runtime.StatusRunning || sb.StartedAt == nil {
		return "–"
	}
	return duration(time.Since(*sb.StartedAt))
}

func age(t time.Time) string { return duration(time.Since(t)) }

func duration(d time.Duration) string {
	switch {
	case d < time.Minute:
		return fmt.Sprintf("%ds", int(d.Seconds()))
	case d < time.Hour:
		return fmt.Sprintf("%dm", int(d.Minutes()))
	case d < 48*time.Hour:
		return fmt.Sprintf("%dh %dm", int(d.Hours()), int(d.Minutes())%60)
	default:
		return fmt.Sprintf("%dd", int(d.Hours()/24))
	}
}

func mib(n int) string {
	if n >= 1024 && n%1024 == 0 {
		return fmt.Sprintf("%d GiB", n/1024)
	}
	if n >= 1024 {
		return fmt.Sprintf("%.1f GiB", float64(n)/1024)
	}
	return fmt.Sprintf("%d MiB", n)
}
