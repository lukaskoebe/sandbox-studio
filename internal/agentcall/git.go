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

// The review tools read a pull request on a forge and draft a review of it. Nothing reaches the
// forge until the user approves the draft.
var pullRequestRef = map[string]any{
	"forge":      str("The forge's name, the segment after git.studio.internal/ in the remote's URL; optional when the environment has one forge with an API", "maxLength", 32),
	"repository": str("owner/repo on that forge", "maxLength", 300),
	"number":     map[string]any{"type": "integer", "minimum": 1, "description": "The pull request's number"},
}

// GitRepositoriesTool lists the environment's forges and the repositories their tokens reach.
var GitRepositoriesTool = Tool{agentproto.MethodGitRepositories,
	"The git forges of this environment and the repositories Studio's token for each reaches, recently active first. " +
		"Clone one through the git remote with `git clone <remote><path>.git`; the pull request tools work on the forges " +
		"marked pullRequests (Forgejo, Gitea and GitHub).",
	obj(nil, map[string]any{
		"forge": str("Only this forge", "maxLength", 32),
		"query": str("Only repositories whose path or description contains this", "maxLength", 100),
	})}

// GitPullRequestListTool lists a repository's pull requests, anyone's.
var GitPullRequestListTool = Tool{agentproto.MethodGitPullRequestList,
	"The pull requests of a repository on a Forgejo, Gitea or GitHub forge, whoever opened them, most recently updated first: " +
		"number, title, author, branches, draft, requested reviewers and labels. Read one with git_pull_request_read.",
	obj([]string{"repository"}, map[string]any{
		"forge":      pullRequestRef["forge"],
		"repository": pullRequestRef["repository"],
		"state":      map[string]any{"type": "string", "enum": []string{"open", "closed", "all"}, "description": "Default open"},
		"limit":      map[string]any{"type": "integer", "minimum": 1, "maximum": 100, "description": "Default 30"},
	})}

// GitPullRequestReadTool reads a pull request through Studio, which holds the forge's token.
var GitPullRequestReadTool = Tool{agentproto.MethodGitPullRequestRead,
	"A pull request on a Forgejo, Gitea or GitHub forge, whoever opened it: its title, description, author, branches and commits, " +
		"the changed files with their line counts, and the reviews, their line comments and the conversation's comments so far. " +
		"Diffs are left out: read the code with git, as `git fetch origin pull/<number>/head` fetches the pull request's head " +
		"through the git remote.",
	obj([]string{"repository", "number"}, pullRequestRef)}

// GitPullRequestReviewTool drafts a review; the user edits, posts or discards it.
var GitPullRequestReviewTool = Tool{agentproto.MethodGitPullRequestReview,
	"Drafts a review of a pull request, yours or anyone's: a verdict, a summary and comments on lines of its diff. To only " +
		"comment, use verdict comment. Nothing is posted yet: the user " +
		"sees the draft beside the diff, can edit it, and posts or discards it. The call answers at once with \"awaiting approval\"; " +
		"git_pull_request_review_status says what became of it. A comment must be on a line the diff shows, as added or kept (side new) " +
		"or removed (side old). A new draft for the same pull request replaces one still waiting.",
	obj([]string{"repository", "number", "verdict", "summary"}, map[string]any{
		"forge":      pullRequestRef["forge"],
		"repository": pullRequestRef["repository"],
		"number":     pullRequestRef["number"],
		"verdict":    map[string]any{"type": "string", "enum": []string{"comment", "approve", "request_changes"}},
		"summary":    str("The review's body, in Markdown", "maxLength", 20000),
		"comments": map[string]any{"type": "array", "maxItems": 50, "items": obj([]string{"path", "line", "body"}, map[string]any{
			"path": str("The file's path in the pull request", "maxLength", 500),
			"line": map[string]any{"type": "integer", "minimum": 1, "description": "The line's number in the new file, or in the old one for side old"},
			"side": map[string]any{"type": "string", "enum": []string{"new", "old"}, "description": "Default new"},
			"body": str("The comment, in Markdown", "maxLength", 5000),
		})},
	})}

// GitReviewStatusTool reports what became of the newest review drafted for a pull request.
var GitReviewStatusTool = Tool{agentproto.MethodGitReviewStatus,
	"Your newest review draft of a pull request, from Studio's records: awaiting approval, posted (with its URL), discarded " +
		"(with the user's note, if any), replaced by a newer draft, or failed (with the forge's answer).",
	obj([]string{"repository", "number"}, pullRequestRef)}

func init() {
	Tools = append(Tools, GitPushStatusTool, GitPullRequestStatusTool, GitRepositoriesTool, GitPullRequestListTool,
		GitPullRequestReadTool, GitPullRequestReviewTool, GitReviewStatusTool)
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
