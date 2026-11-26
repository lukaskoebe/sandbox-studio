package agentcall

import "github.com/lukaskoebe/sandbox-studio/internal/agentproto"

// ReportTool offers the user a report, for Studio's developers, of something in Studio or the
// sandbox that confused or misled the agent; internal/reports serves it (docs/reports.md).
var ReportTool = Tool{agentproto.MethodReportIssue,
	"Offer the user a report to Sandbox Studio's developers of something in Studio or this sandbox that confused or misled you, " +
		"or didn't work as Studio's instructions say: an instruction that doesn't hold, a Studio tool or command that answered wrongly, " +
		"an error that names the wrong cause, a tool the machine should have and lacks. Not for bugs in the user's own project. " +
		"Studio takes out the names, addresses, paths and secrets it finds, and the user reads the report and decides whether to send it " +
		"as a GitHub issue. Don't wait for that: tell the user in a sentence what you reported, and go on.",
	obj([]string{"title", "body"}, map[string]any{
		"title": str("One line: what went wrong", "maxLength", 200),
		"body":  str("Markdown: what you did, what happened (the exact error or output), what you expected, and how to see it again", "maxLength", 8000),
	})}

func init() {
	Tools = append(Tools, ReportTool)
}
