package teammates

// The default teammate. Studio creates the default teammate on a provider added to an
// environment that has no teammates (docs/teammates.md#default-teammate). Afterwards the
// default is an ordinary teammate.
const (
	DefaultTeammateName     = "Iris"
	DefaultTeammateRole     = "Generalist engineer"
	DefaultTeammateSoul     = "Curious, careful and direct. Says what it knows and what it doesn't, explains decisions briefly, prefers small verified steps, and asks before anything risky or hard to undo."
	DefaultTeammateGitName  = "Iris (agent)"
	DefaultTeammateGitEmail = "iris@agents.invalid"
)

// DefaultTeammateHarness returns the harness the default teammate runs on a provider of kind,
// and its model there. The model is empty where the provider's own model applies: a
// subscription's default, or the single model of an OpenAI-compatible endpoint.
func DefaultTeammateHarness(kind string) (harness, model string) {
	switch kind {
	case KindAnthropicAPI:
		return HarnessClaude, "claude-sonnet-5-5"
	case KindOpenAIAPI:
		return HarnessCodex, "gpt-sol-6.1"
	case KindOpenAICompatible:
		return HarnessOpenCode, ""
	case KindClaudeSubscription:
		return HarnessClaude, ""
	case KindChatGPTSubscription:
		return HarnessCodex, ""
	}
	return "", ""
}
