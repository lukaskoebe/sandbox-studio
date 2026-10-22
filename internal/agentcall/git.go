package agentcall

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"strings"

	"github.com/lukaskoebe/sandbox-studio/internal/agentproto"
)

// The git tools report what the sandbox pushed through the virtual remote. Studio answers
// them from its records only (docs/git-review.md); gitreview serves them.

// GitPushStatusTool lists the sandbox's recent pushes and their review states.
var GitPushStatusTool = Tool{agentproto.MethodGitPushStatus,
	"Your recent pushes to the git remote, newest first, with their review state: pending (waiting for the user), " +
		"pushed (approved and on the forge), rejected (declined; the reviewer's note is included), failed (approved, but the forge " +
		"refused it) or superseded (a newer push to the same branch replaced it).",
	obj(nil, map[string]any{
		"limit": map[string]any{"type": "integer", "minimum": 1, "maximum": 20, "description": "Default 10"},
	})}

// GitPullRequestStatusTool reports the pull request proposed for a pushed branch.
var GitPullRequestStatusTool = Tool{agentproto.MethodGitPullRequestStatus,
	"The pull request for a branch you pushed, from Studio's records: none, awaiting approval, opened (with its URL), " +
		"not opened (the user declined) or failed (with the reason). Studio asks the forge nothing.",
	obj([]string{"branch"}, map[string]any{
		"branch":     str("The branch you pushed", "maxLength", 255),
		"forge":      str("The forge's name, when you pushed the branch to more than one", "maxLength", 32),
		"repository": str("owner/repo on that forge, when you pushed the branch to more than one", "maxLength", 300),
	})}

func init() {
	Tools = append(Tools, GitPushStatusTool, GitPullRequestStatusTool)
}

// GitCredentialCall routes the git credential helper's question to gitreview. It stays out
// of Tools, so agents never see it as a tool.
var GitCredentialCall = Tool{agentproto.MethodGitCredential,
	"git's credential helper asks about an HTTPS host it has no credentials for.",
	obj([]string{"protocol", "host"}, map[string]any{
		"protocol": str("git's protocol, https", "maxLength", 16),
		"host":     str("The host, with its port when git names one", "maxLength", 260),
		"path":     str("The repository's path on the host", "maxLength", 500),
	})}

// RunGitCredential is `studio-agent git-credential <action>`, git's credential helper in the
// sandbox. It never answers with a credential: sandboxes hold none (docs/git-review.md). On
// get for an HTTPS host, it tells Studio, which offers the user to add a forge for it, and
// prints Studio's hint to stderr, which git shows the agent. Everything else it ignores.
func RunGitCredential(ctx context.Context, c Caller, action string, in io.Reader, stderr io.Writer) error {
	fields := map[string]string{}
	sc := bufio.NewScanner(in)
	for sc.Scan() {
		line := sc.Text()
		if line == "" {
			break
		}
		if k, v, ok := strings.Cut(line, "="); ok && (k == "protocol" || k == "host" || k == "path") {
			fields[k] = v
		}
	}
	if action != "get" || fields["protocol"] != "https" || fields["host"] == "" {
		return nil
	}
	var answer struct {
		Message string `json:"message"`
	}
	if err := c.Call(ctx, agentproto.MethodGitCredential, fields, &answer); err != nil {
		return err
	}
	if answer.Message != "" {
		_, err := fmt.Fprintln(stderr, answer.Message)
		return err
	}
	return nil
}
