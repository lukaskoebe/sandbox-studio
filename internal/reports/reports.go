// Package reports lets an agent offer the user a report of something in Studio or its
// sandbox that confused or misled it, or didn't work as Studio's instructions say
// (docs/reports.md). Studio takes the names, addresses, paths and secrets it can find out of
// the report, adds which Studio and harness it ran on, and raises a card. The user reads and
// edits the report there, and sends it as a GitHub issue on Sandbox Studio's repository, from
// their own browser and account. Nothing leaves the machine unless they do.
package reports

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/url"
	"os"
	"os/user"
	goruntime "runtime"
	"strings"
	"time"

	"github.com/lukaskoebe/sandbox-studio/internal/agentcall"
	"github.com/lukaskoebe/sandbox-studio/internal/events"
	"github.com/lukaskoebe/sandbox-studio/internal/gateway"
	"github.com/lukaskoebe/sandbox-studio/internal/integrations"
	"github.com/lukaskoebe/sandbox-studio/internal/store"
	"github.com/lukaskoebe/sandbox-studio/internal/updates"
	"github.com/lukaskoebe/sandbox-studio/internal/version"
)

// Kind is a report an agent offers to send. Allowing it means the user opened the issue on
// GitHub; dismissing it sends nothing.
const Kind = "studio.report"

// maxPending is how many reports may wait in an environment at once: more read as an agent
// stuck in a loop, and the user would have to dismiss each.
const maxPending = 3

// Payload is what a studio.report card shows.
type Payload struct {
	Title string `json:"title" doc:"The issue's title, anonymized"`
	Body  string `json:"body" doc:"What the agent found, anonymized: the issue's text above its footer"`
	// Footer is Studio's own: the versions it ran on, which name nobody.
	Footer     string `json:"footer" doc:"Which Studio, system, harness and base image the report comes from; the issue ends with it"`
	Replaced   int    `json:"replaced" doc:"How many names, addresses, paths and secrets Studio took out"`
	Repository string `json:"repository" doc:"The GitHub repository the issue goes to, owner/name"`
	IssueURL   string `json:"issueUrl" doc:"GitHub's new-issue page for the repository, without the title and text"`

	SandboxID    string `json:"sandboxId"`
	SandboxName  string `json:"sandboxName"`
	TeammateID   string `json:"teammateId,omitempty"`
	TeammateName string `json:"teammateName,omitempty"`
}

// Service raises and settles studio.report cards.
type Service struct {
	Store *store.Store
	Bus   *events.Bus
	Log   *slog.Logger
	Now   func() time.Time
	// Host is who and where Studio runs, for the names to take out: the user's login and full
	// name, the machine's name and the home folder. Nil reads them from the system.
	Host func() []Name
}

func (s *Service) ID() string                    { return "reports" }
func (s *Service) Routes() []gateway.VirtualHost { return nil }
func (s *Service) Jobs() []integrations.Job      { return nil }
func (s *Service) Tools() []integrations.Tool {
	return []integrations.Tool{{Def: agentcall.ReportTool, Handle: s.report}}
}
func (s *Service) ApprovalKinds() []integrations.ApprovalKind {
	return []integrations.ApprovalKind{{Kind: Kind, Decide: s.decide}}
}

// Result is what the agent hears back.
type Result struct {
	Approval string `json:"approval"`
	Status   string `json:"status"`
	Message  string `json:"message"`
	Replaced int    `json:"replaced"`
}

func (s *Service) report(ctx context.Context, sb store.Sandbox, params json.RawMessage) (any, error) {
	var in struct {
		Title string `json:"title"`
		Body  string `json:"body"`
	}
	if err := json.Unmarshal(params, &in); err != nil {
		return nil, integrations.ToolError("invalid arguments: %v", err)
	}
	in.Title, in.Body = strings.TrimSpace(in.Title), strings.TrimSpace(in.Body)
	if in.Title == "" || in.Body == "" {
		return nil, integrations.ToolError("give the report a title and a body: what you did, what happened and what you expected")
	}
	pending, err := s.Store.Approvals(ctx, sb.EnvironmentID, store.StatusPending, 1000)
	if err != nil {
		return nil, err
	}
	n := 0
	for _, a := range pending {
		if a.Kind == Kind {
			n++
		}
	}
	if n >= maxPending {
		return nil, integrations.ToolError("the user already has %d reports to look at; mention this one in your message instead", n)
	}
	names, tm, err := s.names(ctx, sb)
	if err != nil {
		return nil, err
	}
	title, r1 := Anonymize(in.Title, names)
	body, r2 := Anonymize(in.Body, names)
	p := Payload{
		Title: title, Body: body, Replaced: r1 + r2,
		Footer:     s.footer(sb, tm),
		Repository: updates.Repository,
		IssueURL:   "https://github.com/" + updates.Repository + "/issues/new",
		SandboxID:  sb.ID, SandboxName: sb.Name,
	}
	if tm != nil {
		p.TeammateID, p.TeammateName = tm.ID, tm.Name
	}
	raw, err := json.Marshal(p)
	if err != nil {
		return nil, err
	}
	a, _, err := s.Store.RequestExactApproval(ctx, store.Approval{
		EnvironmentID: sb.EnvironmentID, SandboxID: sb.ID, Kind: Kind,
		Subject: "Report: " + title, Payload: raw,
	})
	if err != nil {
		return nil, err
	}
	s.publish(sb.EnvironmentID, a.ID)
	return Result{
		Approval: a.ID, Status: "offered", Replaced: p.Replaced,
		Message: "Studio took out the names, addresses and paths it found and asks the user whether to send the report. Tell them in a sentence what you reported; don't wait for their answer.",
	}, nil
}

// footer names what the report ran on: nothing in it identifies the user.
func (s *Service) footer(sb store.Sandbox, tm *store.Teammate) string {
	parts := []string{"Studio " + version.Version, goruntime.GOOS + "/" + goruntime.GOARCH}
	if tm != nil && tm.Harness != "" {
		parts = append(parts, "harness "+tm.Harness)
	}
	if sb.BaseVersion != "" {
		parts = append(parts, "base image "+sb.BaseVersion)
	}
	return "Reported by an agent in Sandbox Studio and reviewed by its user before sending. " + strings.Join(parts, " · ")
}

// names are what Studio knows could identify the user: the environment's computers,
// teammates and their git identities, forges and local network hosts, the
// repositories the sandbox pushed, and who and where Studio runs.
func (s *Service) names(ctx context.Context, sb store.Sandbox) ([]Name, *store.Teammate, error) {
	var out []Name
	add := func(label string, texts ...string) {
		for _, t := range texts {
			if t = strings.TrimSpace(t); t != "" {
				out = append(out, Name{t, label})
			}
		}
	}
	env, err := s.Store.Environment(ctx, sb.EnvironmentID)
	if err != nil {
		return nil, nil, err
	}
	add("environment", env.Name)
	sbs, err := s.Store.Sandboxes(ctx, sb.EnvironmentID)
	if err != nil {
		return nil, nil, err
	}
	for _, o := range sbs {
		add("computer", o.Name)
	}
	tms, err := s.Store.Teammates(ctx, sb.EnvironmentID)
	if err != nil {
		return nil, nil, err
	}
	var tm *store.Teammate
	for i, t := range tms {
		add("teammate", t.Name, t.GitName)
		add("email", t.GitEmail)
		if t.ID == sb.TeammateID {
			tm = &tms[i]
		}
	}
	forges, err := s.Store.Forges(ctx, sb.EnvironmentID)
	if err != nil {
		return nil, nil, err
	}
	for _, f := range forges {
		add("forge", f.Name, f.Username)
		if u, err := url.Parse(f.BaseURL); err == nil && u.Hostname() != "" {
			add("host", u.Hostname())
		}
	}
	hosts, err := s.Store.PrivateHosts(ctx, sb.EnvironmentID)
	if err != nil {
		return nil, nil, err
	}
	for _, h := range hosts {
		add("host", strings.TrimPrefix(h.Host, "*."))
	}
	pushes, err := s.Store.SandboxGitPushes(ctx, sb.EnvironmentID, sb.ID, "", 200)
	if err != nil {
		return nil, nil, err
	}
	for _, p := range pushes {
		add("repository", p.Owner+"/"+p.Repo, p.Repo, p.Owner)
		add("branch", strings.TrimPrefix(p.Ref, "refs/heads/"))
	}
	host := s.Host
	if host == nil {
		host = SystemNames
	}
	out = append(out, host()...)
	return out, tm, nil
}

// SystemNames are the user Studio runs as and the machine it runs on.
func SystemNames() []Name {
	var out []Name
	if u, err := user.Current(); err == nil {
		out = append(out, Name{u.Username, "user"})
		for _, w := range strings.Fields(u.Name) {
			out = append(out, Name{w, "user"})
		}
	}
	if h, err := os.Hostname(); err == nil {
		out = append(out, Name{h, "machine"}, Name{strings.Split(h, ".")[0], "machine"})
	}
	return out
}

// decide settles a report: allow once the user opened it on GitHub, dismiss when they don't
// send it. Either way nothing goes back to the sandbox but the status.
func (s *Service) decide(ctx context.Context, a store.Approval, d integrations.Decision) error {
	var st string
	switch d.Action {
	case integrations.Allow:
		st = store.StatusApproved
	case integrations.Deny, integrations.Dismiss:
		st = store.StatusDismissed
	default:
		return fmt.Errorf("%w %q", integrations.ErrAction, d.Action)
	}
	if err := s.Store.DecideApproval(ctx, a.EnvironmentID, a.ID, st, ""); err != nil {
		return err
	}
	s.publish(a.EnvironmentID, a.ID)
	return nil
}

func (s *Service) publish(envID, approvalID string) {
	if s.Bus != nil {
		s.Bus.Publish(events.Event{Topic: events.TopicApprovals, EnvironmentID: envID, ID: approvalID})
	}
}
