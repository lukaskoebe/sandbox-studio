// Package integrations is the seam between Studio's core and the services it offers
// sandboxes on the host (docs/git-review.md). An integration serves virtual hosts under
// studio.internal through the gateway and settles the approvals it raises; the core
// knows nothing else about it. The git review remote is the first one.
package integrations

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/lukaskoebe/sandbox-studio/internal/agentcall"
	"github.com/lukaskoebe/sandbox-studio/internal/gateway"
	"github.com/lukaskoebe/sandbox-studio/internal/store"
)

// Decision actions, as the inbox sends them.
const (
	Allow   = "allow"
	Deny    = "deny"
	Dismiss = "dismiss"
)

// ErrAction marks a decision an approval kind does not support.
var ErrAction = errors.New("unsupported action")

// Decision is the user's answer to an approval.
type Decision struct {
	Action string // Allow, Deny or Dismiss
	Note   string // optional, passed on to the sandbox where the integration can
	// Remember asks to keep the answer for similar requests, where the kind supports
	// it (browser.action: allow or deny the action on the origin from now on).
	Remember bool
	// PullRequest, for an approved git.push the review offers a pull request with, says
	// whether to open it along with the push. Nil leaves it to a git.pr approval.
	PullRequest *PullRequestChoice
	// Force approves a git.push that rewrites its branch, which allow alone doesn't.
	Force bool
	// Review, for a git.review approved with allow, is the review as the user edited it.
	// Nil posts the draft as it is.
	Review *ReviewEdit
}

// ReviewEdit is a pull request review as the user posts it.
type ReviewEdit struct {
	Verdict  string // comment, approve or request_changes
	Summary  string
	Comments []ReviewEditComment
	// Attribution ends the review with "Review by <teammate> (agent)".
	Attribution bool
}

// ReviewEditComment is a comment on one line of the pull request's diff.
type ReviewEditComment struct {
	Path string
	Line int
	Side string // new or old
	Body string
}

// PullRequestChoice is whether a push opens its pull request, and under what title.
type PullRequestChoice struct {
	Open  bool
	Title string // empty keeps the proposed title
}

// ApprovalKind settles approvals of one kind. Decide moves a pending approval out of
// pending (store.ErrNotFound if it is not pending) and does whatever the decision implies.
type ApprovalKind struct {
	Kind   string
	Decide func(ctx context.Context, a store.Approval, d Decision) error
}

// Tool is an MCP tool an integration serves to agents. Def is its definition in the
// guest's `studio-agent mcp` (agentcall.Tools); a call the agent makes comes back to Handle
// under Def.Name, with the sandbox that made it, as the channel named it. Handle returns a
// result, which the agent reads as JSON, or an error: a ToolError is shown to the agent as it
// is, any other error is logged and the agent gets only "<tool> failed".
type Tool struct {
	Def    agentcall.Tool
	Handle func(ctx context.Context, sb store.Sandbox, params json.RawMessage) (any, error)
}

// Job is periodic background work of an integration. Run goes every Every until the
// context ends; a failed run is logged and the next one still goes on time.
type Job struct {
	Name  string
	Every time.Duration
	Run   func(ctx context.Context) error
}

// toolError is a tool error the agent reads as it is.
type toolError struct{ msg string }

func (e toolError) Error() string { return e.msg }

// ToolError is an error from a tool's handler whose message is meant for the agent.
func ToolError(format string, args ...any) error {
	return toolError{fmt.Sprintf(format, args...)}
}

// IsToolError reports whether err is a ToolError.
func IsToolError(err error) bool {
	var te toolError
	return errors.As(err, &te)
}

// Integration is a host-side service for sandboxes. An integration without MCP tools or
// jobs returns nil from Tools or Jobs.
type Integration interface {
	ID() string
	Routes() []gateway.VirtualHost
	ApprovalKinds() []ApprovalKind
	Tools() []Tool
	Jobs() []Job
}

// Set is the integrations Studio runs.
type Set []Integration

// Routes collects every integration's virtual hosts.
func (s Set) Routes() []gateway.VirtualHost {
	var out []gateway.VirtualHost
	for _, i := range s {
		out = append(out, i.Routes()...)
	}
	return out
}

// Kind returns the settler for an approval kind, if an integration owns it.
func (s Set) Kind(kind string) (ApprovalKind, bool) {
	for _, i := range s {
		for _, k := range i.ApprovalKinds() {
			if k.Kind == kind {
				return k, true
			}
		}
	}
	return ApprovalKind{}, false
}

// Tool returns the MCP tool a guest's call names, if an integration serves it.
func (s Set) Tool(name string) (Tool, bool) {
	for _, i := range s {
		for _, t := range i.Tools() {
			if t.Def.Name == name {
				return t, true
			}
		}
	}
	return Tool{}, false
}

// RunJobs runs every integration's jobs until ctx ends. Jobs with a non-positive interval
// are not run.
func (s Set) RunJobs(ctx context.Context, log *slog.Logger) {
	var wg sync.WaitGroup
	for _, i := range s {
		for _, j := range i.Jobs() {
			if j.Every <= 0 {
				continue
			}
			wg.Add(1)
			go func() {
				defer wg.Done()
				runJob(ctx, j, log)
			}()
		}
	}
	wg.Wait()
}

func runJob(ctx context.Context, j Job, log *slog.Logger) {
	t := time.NewTicker(j.Every)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
		if err := j.Run(ctx); err != nil && ctx.Err() == nil {
			log.Warn("integration job failed", "job", j.Name, "err", err)
		}
	}
}
