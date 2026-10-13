package agentcall

import "github.com/lukaskoebe/sandbox-studio/internal/agentproto"

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
