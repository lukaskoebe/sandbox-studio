package gitreview

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/lukaskoebe/sandbox-studio/internal/integrations"
	"github.com/lukaskoebe/sandbox-studio/internal/store"
)

// callTool runs one of the git tools for sb and returns its result as JSON text.
func callTool(t *testing.T, h *harness, sb store.Sandbox, name, args string) (string, error) {
	t.Helper()
	for _, tool := range h.svc.Tools() {
		if tool.Def.Name != name {
			continue
		}
		res, err := tool.Handle(h.ctx, sb, json.RawMessage(args))
		if err != nil {
			return "", err
		}
		b, _ := json.Marshal(res)
		return string(b), nil
	}
	t.Fatalf("no tool %s", name)
	return "", nil
}

func TestPushStatusFollowsTheReview(t *testing.T) {
	h := newHarness(t)
	g := h.guest(t, h.env.ID, "one")
	repo := g.clone(t)
	commitFile(t, repo, "x.txt", "x\n", "Add x")
	if err := push(repo, "refs/heads/main:refs/heads/main", false); err != nil {
		t.Fatalf("push: %v", err)
	}
	out, err := callTool(t, h, g.sb, "git_push_status", `{}`)
	if err != nil || !strings.Contains(out, `"state":"pending"`) || !strings.Contains(out, `"branch":"main"`) || !strings.Contains(out, `"repository":"owner/repo"`) {
		t.Fatalf("push status while pending = %s, %v", out, err)
	}
	a := h.approvals(t, KindPush, store.StatusPending)[0]
	h.decide(t, a, integrations.Deny, "please add tests")
	out, err = callTool(t, h, g.sb, "git_push_status", `{"limit":1}`)
	if err != nil || !strings.Contains(out, `"state":"rejected"`) || !strings.Contains(out, `"note":"please add tests"`) {
		t.Fatalf("push status after rejection = %s, %v", out, err)
	}
	// Another sandbox of the environment sees none of these pushes.
	other := h.guest(t, h.env.ID, "two")
	out, err = callTool(t, h, other.sb, "git_push_status", `{}`)
	if err != nil || out != `{"pushes":[]}` {
		t.Fatalf("another sandbox's push status = %s, %v", out, err)
	}
	if _, err := callTool(t, h, g.sb, "git_push_status", `{"sandbox":"two"}`); !integrations.IsToolError(err) {
		t.Fatalf("an unknown argument = %v, want a tool error", err)
	}
}

func TestPullRequestStatusReadsTheStoredRecord(t *testing.T) {
	h := newHarness(t)
	g := h.guest(t, h.env.ID, "one")
	repo := g.clone(t)
	commitFile(t, repo, "a.txt", "a\n", "Add a")
	if _, err := callTool(t, h, g.sb, "git_pull_request_status", `{"branch":"feature"}`); !integrations.IsToolError(err) {
		t.Fatalf("status of a branch never pushed = %v, want a tool error", err)
	}
	if err := push(repo, "refs/heads/main:refs/heads/feature", false); err != nil {
		t.Fatalf("push: %v", err)
	}
	out, err := callTool(t, h, g.sb, "git_pull_request_status", `{"branch":"feature"}`)
	if err != nil || !strings.Contains(out, `"pushState":"pending"`) || !strings.Contains(out, `"pullRequest":{"status":"none"}`) {
		t.Fatalf("status while the push waits = %s, %v", out, err)
	}
	h.decide(t, h.approvals(t, KindPush, store.StatusPending)[0], integrations.Allow, "")
	out, err = callTool(t, h, g.sb, "git_pull_request_status", `{"branch":"feature"}`)
	if err != nil || !strings.Contains(out, `"status":"awaiting approval"`) {
		t.Fatalf("status while the pull request waits = %s, %v", out, err)
	}
	h.decide(t, h.approvals(t, KindPR, store.StatusPending)[0], integrations.Allow, "")
	out, err = callTool(t, h, g.sb, "git_pull_request_status", `{"branch":"feature","repository":"owner/repo"}`)
	if err != nil || !strings.Contains(out, `"status":"opened"`) || !strings.Contains(out, `"url":"`) {
		t.Fatalf("status once opened = %s, %v", out, err)
	}
	if _, err := callTool(t, h, g.sb, "git_pull_request_status", `{"branch":"feature","forge":"elsewhere"}`); !integrations.IsToolError(err) {
		t.Fatalf("status for another forge = %v, want a tool error", err)
	}
}
